// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// These tests make the cells of the queued-operation lifecycle explicit: a
// server-owned logout or refresh accepted while an execution lease holds the
// account, against every way that lease can end. The contract document lists
// the same cells. The execution lease is seeded directly; the execution layer
// grants it through Take.

// queuedKind is one queued server-owned operation.
type queuedKind struct {
	name   string
	action pb.SubscriptionAction
}

var queuedKinds = []queuedKind{
	{"logout", logoutAction},
	{"refresh", pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH},
}

// holdExecution connects the account and seeds the execution lease of the
// Worker the fixture authenticates as, then accepts the queued operation. It
// returns the lease the Worker reports Finish for and the queued operation.
func (g *grokLoginFixture) holdExecution(t *testing.T, action pb.SubscriptionAction) (*pb.TakeSubscriptionResponse, domain.ID, domain.ID) {
	t.Helper()
	generation := g.login("synthetic-principal-1")
	lease := domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: g.input.MachineID, InstanceID: g.instance, DeviceID: g.device, Epoch: g.service.subscriptionServerEpoch(), Generation: generation, StartedAt: time.Now().UTC()}
	g.mutateServerOperation(func(a *domain.Account) {
		a.Subscription.Lease = &lease
		a.Subscription.OwnerMachineID = g.input.MachineID
	})
	op := g.serverStart(action)
	return &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), GenerationId: string(generation), LeaseRevision: lease.Revision}, generation, domain.ID(op.OperationId)
}

// requireQueued proves that the accepted operation is untouched and unclaimed.
func (g *grokLoginFixture) requireQueued(t *testing.T, op domain.ID) {
	t.Helper()
	_, a := g.record()
	st := a.Subscription
	if st.ServerOperation == nil || st.ServerOperation.ID != op || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.ServerOperation.GrokRevocation != "" || st.ServerOperation.RefreshClaimed || st.Pending == nil || st.Pending.ID != op || st.Pending.Phase != domain.SubscriptionQueued {
		t.Fatalf("the accepted operation was claimed, settled or lost: %+v", st.ServerOperation)
	}
}

// requireProceeded runs the operation that waited and proves that it reached its
// settled outcome on the account's current generation.
func (g *grokLoginFixture) requireProceeded(t *testing.T, kind queuedKind, generation domain.ID) {
	t.Helper()
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	_, a := g.record()
	st := a.Subscription
	switch kind.action {
	case logoutAction:
		if g.idp.RevokeRequests() != 1 {
			t.Fatalf("the logout sent %d revocation requests, want 1", g.idp.RevokeRequests())
		}
		g.requireDisconnected(t)
	default:
		if st.Pending != nil || st.ServerOperation.State == domain.SubscriptionPreparing || st.Lease != nil || st.RecoveryRequired || st.Generation == "" {
			t.Fatalf("the refresh did not run after the lease was released: %+v", st.ServerOperation)
		}
		if g.idp.TokenRequests() != 1 {
			t.Fatalf("the refresh sent %d token requests, want 1", g.idp.TokenRequests())
		}
	}
	_ = generation
}

// An execution whose Finish confirmed the exit and the private-home cleanup
// releases its lease whatever it produced, so the operation accepted behind it
// proceeds. A logout cancels the execution it waited for, which ends with
// Succeeded=false; that must still release the lease.
func TestGrokQueuedOperationProceedsAfterTheExecutionFinishReleasesTheLease(t *testing.T) {
	for _, kind := range queuedKinds {
		for _, finish := range []struct {
			name    string
			success bool
			rotated bool
		}{
			{"succeeded with a rotated credential", true, true},
			{"canceled by the logout", false, false},
		} {
			t.Run(kind.name+" "+finish.name, func(t *testing.T) {
				g := newGrokLoginFixture(t)
				lease, generation, op := g.holdExecution(t, kind.action)
				g.service.runServerSubscription(ownerContext(), g.input.AccountID)
				g.requireQueued(t, op)
				var bundle []byte
				current, request := generation, domain.NewID()
				if finish.rotated {
					current = request
					bundle = g.grokRotatedEnvelope(current)
				}
				if _, err := g.finishWith(lease, request, bundle, finish.success, false, true); err != nil {
					t.Fatalf("the confirmed execution cleanup was refused: %v", err)
				}
				_, a := g.record()
				if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Generation != current {
					t.Fatalf("the execution lease was not released cleanly: lease=%v fenced=%v", a.Subscription.Lease != nil, a.Subscription.RecoveryRequired)
				}
				g.requireQueued(t, op)
				g.requireProceeded(t, kind, current)
			})
		}
	}
}

// The queued operation's actor does not own the execution lease, so its
// revocation or cancellation cannot refuse the execution's Finish.
func TestGrokExecutionFinishDoesNotDependOnTheQueuedOperationsActor(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, _, op := g.holdExecution(t, kind.action)
			g.mutateServerOperation(func(a *domain.Account) {
				gone := domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()}
				a.Subscription.Pending.Actor = gone
				a.Subscription.Pending.Canceled = true
			})
			if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
				t.Fatalf("the queued operation's actor blocked the execution Finish: %v", err)
			}
			_, a := g.record()
			if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Subscription.Pending.ID != op {
				t.Fatal("the execution lease stayed held, or the queued operation was dropped")
			}
		})
	}
}
