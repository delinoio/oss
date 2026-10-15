// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// These tests run the queued-operation cells of the lifecycle contract against a
// real execute Take, so the lease, the canceled job and the Worker's Finish are
// the ones the Worker produces. The seeded-lease variants live in
// grok_queued_operations_test.go.

// queuedExecution is a Grok account whose execution lease was Taken and behind
// which a server-owned logout or refresh was accepted.
type queuedExecution struct {
	*grokLoginFixture
	execution *grokExecutionFixture
	lease     *pb.TakeSubscriptionResponse
	op        domain.ID
	job       memberJob
}

func newQueuedExecution(t *testing.T, action pb.SubscriptionAction) *queuedExecution {
	t.Helper()
	g := newGrokLoginFixture(t)
	g.service.grokExecution = true
	execution := &grokExecutionFixture{grokFixture: g.grokFixture}
	execution.setCapabilities(t, domain.ManagedGrokSubscriptionsV1)
	execution.generation = g.login("synthetic-principal-1")
	jobs := execution.prepareJobs(0)
	lease, err := execution.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	op := g.serverStart(action)
	return &queuedExecution{grokLoginFixture: g, execution: execution, lease: lease, op: domain.ID(op.OperationId), job: jobs[0]}
}

func (q *queuedExecution) proceeds(t *testing.T, action pb.SubscriptionAction) {
	t.Helper()
	q.service.runServerSubscription(ownerContext(), q.input.AccountID)
	_, a := q.record()
	if action == logoutAction {
		if q.idp.RevokeRequests() != 1 {
			t.Fatalf("the logout sent %d revocation requests, want 1", q.idp.RevokeRequests())
		}
		q.requireDisconnected(t)
		return
	}
	if q.idp.TokenRequests() != 1 || a.Subscription.Pending != nil || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
		t.Fatalf("the refresh did not run after the execution: %+v", a.Subscription.ServerOperation)
	}
}

// A logout accepted during the execution cancels it; the Worker stops the native
// process and reports an unsuccessful Finish with confirmed cleanup. That Finish
// releases the lease and the logout reaches revocation.
func TestGrokLogoutCancelsTheExecutionAndReachesRevocationAfterItsCleanup(t *testing.T) {
	q := newQueuedExecution(t, logoutAction)
	q.service.runServerSubscription(ownerContext(), q.input.AccountID)
	q.requireQueued(t, q.op)
	if _, err := q.finishWith(q.lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatalf("the canceled execution's confirmed cleanup was refused: %v", err)
	}
	if _, a := q.record(); a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountRevoked {
		t.Fatal("the canceled execution did not release its lease")
	}
	q.proceeds(t, logoutAction)
}

// Every Finish outcome of a real execute lease leaves the queued operation able
// to proceed on the generation the account then references.
func TestGrokQueuedOperationProceedsAfterEveryConfirmedExecuteFinish(t *testing.T) {
	for _, kind := range queuedKinds {
		for _, finish := range []string{"unchanged credential", "native rotation", "failed run"} {
			t.Run(kind.name+" "+finish, func(t *testing.T) {
				q := newQueuedExecution(t, kind.action)
				request := domain.NewID()
				var bundle []byte
				success := true
				switch finish {
				case "unchanged credential":
					bundle = q.execution.sealedCopy(t, q.lease, request)
				case "native rotation":
					bundle = q.grokRotatedEnvelope(request)
				default:
					success = false
				}
				if _, err := q.finishWith(q.lease, request, bundle, success, false, true); err != nil {
					t.Fatalf("the confirmed Finish was refused: %v", err)
				}
				_, a := q.record()
				if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired {
					t.Fatal("the lease was not released cleanly")
				}
				q.requireQueued(t, q.op)
				q.proceeds(t, kind.action)
			})
		}
	}
}

// A rotation that is published and then fails its superseded removal fences the
// account without a lease. The queued operation survives it.
func TestGrokQueuedOperationSurvivesAFailedCleanupAfterARealExecuteFinish(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			q := newQueuedExecution(t, kind.action)
			rotated := domain.NewID()
			q.secrets.afterPut = func() {
				q.secrets.mu.Lock()
				q.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
				q.secrets.mu.Unlock()
			}
			if _, err := q.finishWith(q.lease, rotated, q.grokRotatedEnvelope(rotated), true, false, true); err == nil {
				t.Fatal("a failed removal was reported as success")
			}
			_, a := q.record()
			if !grokQueuedFenceNeedsReconcile(a) || a.Subscription.Generation != rotated {
				t.Fatal("the committed Finish did not leave the leaseless queued fence")
			}
			q.secrets.mu.Lock()
			q.secrets.deleteError = nil
			q.secrets.mu.Unlock()
			if err := q.service.reconcileGrokQueuedFence(ownerContext(), q.input.AccountID, q.op); err != nil {
				t.Fatal(err)
			}
			q.requireQueued(t, q.op)
			q.proceeds(t, kind.action)
		})
	}
}

// A lost Worker fences the execute lease; the original Worker's recovery Finish
// releases it, and the health returns to what the queued operation decides.
func TestGrokExecuteRecoveryRestoresTheHealthOfAQueuedOperation(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			q := newQueuedExecution(t, kind.action)
			if err := q.service.retainLostSubscriptionLeases(q.input.MachineID, q.instance, true); err != nil {
				t.Fatal(err)
			}
			q.service.runServerSubscription(ownerContext(), q.input.AccountID)
			q.requireQueued(t, q.op)
			if _, err := q.finishWith(q.lease, domain.NewID(), nil, false, false, true); err != nil {
				t.Fatalf("the original Worker's recovery was refused: %v", err)
			}
			_, a := q.record()
			want := domain.AccountRevoked
			if kind.action != logoutAction {
				want = domain.AccountReady
			}
			if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != want {
				t.Fatalf("recovery left health %q", a.Health)
			}
			q.requireQueued(t, q.op)
			q.proceeds(t, kind.action)
		})
	}
}

// A restart settles the queued operation without sending anything and leaves the
// stale execute lease fenced; the original Worker's recovery then restores the
// health the account had, so a refresh that failed because of the restart does
// not leave the account failed and a logout leaves it revoked.
func TestGrokRestartThenExecuteRecoveryRestoresTheHealthOfASettledOperation(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			q := newQueuedExecution(t, kind.action)
			// The account was not ready when the refresh was admitted.
			if kind.action != logoutAction {
				q.mutateServerOperation(func(a *domain.Account) { a.Subscription.ServerOperation.HealthBefore = domain.AccountExpired })
			}
			q.restart(t)
			if kind.action == logoutAction {
				if err := q.service.recoverGrokLogout(ownerContext(), q.input.AccountID, q.op); err != nil {
					t.Fatal(err)
				}
			} else if err := q.service.recoverGrokRefresh(context.Background(), q.input.AccountID, q.op); err != nil {
				t.Fatal(err)
			}
			_, a := q.record()
			if a.Subscription.Pending != nil || a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.State != domain.SubscriptionFailed {
				t.Fatal("the restarted operation was not settled with the stale lease fenced")
			}
			if _, err := q.finishWith(q.lease, domain.NewID(), nil, false, false, true); err != nil {
				t.Fatalf("the original Worker's recovery was refused: %v", err)
			}
			_, a = q.record()
			want := domain.AccountRevoked
			if kind.action != logoutAction {
				want = domain.AccountExpired
			}
			if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != want {
				t.Fatalf("recovery left health %q, want %q", a.Health, want)
			}
		})
	}
}
