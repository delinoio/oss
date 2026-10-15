// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The cells of a lease that predates the server's epoch (grokStaleLease). The
// restart fences the account for that lease, and only its exact owner's Finish
// can release it; the server never retires the lease itself.

// recoverQueued settles the operation the restart fenced, leaving the stale
// lease fenced.
func (g *grokLoginFixture) recoverQueued(t *testing.T, kind queuedKind, op domain.ID) {
	t.Helper()
	switch kind.action {
	case logoutAction:
		if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, op); err != nil {
			t.Fatal(err)
		}
	default:
		if err := g.service.recoverGrokRefresh(ownerContext(), g.input.AccountID, op); err != nil {
			t.Fatal(err)
		}
	}
}

// holdPlainExecution seeds an execution lease with no queued operation.
func (g *grokLoginFixture) holdPlainExecution(t *testing.T) (*pb.TakeSubscriptionResponse, domain.ID) {
	t.Helper()
	generation := g.login("synthetic-principal-1")
	lease := domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: g.input.MachineID, InstanceID: g.instance, DeviceID: g.device, Epoch: g.service.subscriptionServerEpoch(), Generation: generation, StartedAt: time.Now().UTC()}
	_, err := g.service.Store.Mutate(ownerContext(), domain.NewID(), "fixture.grok.plain-lease", lease, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, g.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.Lease = &lease
		a.Subscription.OwnerMachineID = g.input.MachineID
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &pb.TakeSubscriptionResponse{LeaseId: string(lease.ID), GenerationId: string(generation), LeaseRevision: lease.Revision}, generation
}

// finishAsInstance reports Finish for lease as another instance of the same Runner Device.
func (g *grokLoginFixture) finishAsInstance(lease *pb.TakeSubscriptionResponse, instance domain.ID) error {
	_, err := g.client.FinishSubscription(context.Background(), subscriptionRequest(g.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(g.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(g.input.MachineID), InstanceId: string(instance), CleanupConfirmed: true}))
	return err
}

func requireStaleFence(t *testing.T, a domain.Account, lease *pb.TakeSubscriptionResponse, generation domain.ID) {
	t.Helper()
	st := a.Subscription
	if st.Lease == nil || string(st.Lease.ID) != lease.LeaseId || !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Generation != generation {
		t.Fatalf("the stale lease is not fenced with the credential intact: lease=%v fenced=%v health=%v", st.Lease != nil, st.RecoveryRequired, a.Health)
	}
}

// Exact owner, cleanup confirmed: the lease is released, the fence is cleared and
// the health returns to what the operation the restart settled implies.
func TestGrokStaleLeaseIsReleasedByItsExactOwnerFinish(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run("queued "+kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, generation, op := g.holdExecution(t, kind.action)
			g.restart(t)
			g.recoverQueued(t, kind, op)
			_, a := g.record()
			requireStaleFence(t, a, lease, generation)
			if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
				t.Fatalf("the original Worker could not release its stale lease: %v", err)
			}
			_, a = g.record()
			st := a.Subscription
			want := domain.AccountReady
			if kind.action == logoutAction {
				want = domain.AccountRevoked
			}
			if st.Lease != nil || st.RecoveryRequired || a.Health != want || st.Generation != generation || g.idp.RevokeRequests() != 0 || g.idp.TokenRequests() != 0 {
				t.Fatalf("the stale lease release was wrong: lease=%v fenced=%v health=%v", st.Lease != nil, st.RecoveryRequired, a.Health)
			}
		})
	}
	t.Run("plain execution lease fenced by the restart", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		lease, generation := g.holdPlainExecution(t)
		g.restart(t)
		_, a := g.record()
		requireStaleFence(t, a, lease, generation)
		if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
			t.Fatalf("the original Worker could not release its stale lease: %v", err)
		}
		_, a = g.record()
		if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Subscription.Generation != generation {
			t.Fatal("the plain stale lease was not released cleanly")
		}
	})
}

// A credential the stale execution rotated natively is the only valid one, so it
// is published under the same checks as a current Finish; the queued operation
// the restart settled stays settled.
func TestGrokStaleLeaseFinishPublishesTheRotatedCredential(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, _, op := g.holdExecution(t, kind.action)
			g.restart(t)
			g.recoverQueued(t, kind, op)
			rotated := domain.NewID()
			if _, err := g.finishWith(lease, rotated, g.grokRotatedEnvelope(rotated), true, false, true); err != nil {
				t.Fatalf("the rotated credential of the stale execution was refused: %v", err)
			}
			_, a := g.record()
			st := a.Subscription
			if st.Lease != nil || st.RecoveryRequired || st.Generation != rotated || st.Pending != nil || st.ServerOperation.State != domain.SubscriptionFailed {
				t.Fatalf("the rotated credential was not published cleanly: %+v", st.ServerOperation)
			}
		})
	}
}

// Exact owner, cleanup not confirmed: the lease and the fence stay, and the same
// owner may report again.
func TestGrokStaleLeaseStaysFencedUntilCleanupIsConfirmed(t *testing.T) {
	g := newGrokLoginFixture(t)
	lease, generation, op := g.holdExecution(t, logoutAction)
	g.restart(t)
	g.recoverQueued(t, queuedKinds[0], op)
	if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, false); err != nil {
		t.Fatalf("an unconfirmed report was refused instead of recorded: %v", err)
	}
	_, a := g.record()
	requireStaleFence(t, a, lease, generation)
	if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
		t.Fatalf("the owner could not report again: %v", err)
	}
	_, a = g.record()
	if a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Health != domain.AccountRevoked {
		t.Fatal("the confirmed report did not release the stale lease")
	}
}

// Any other Worker instance, and a stale lease on an account the restart did not
// fence, are denied before any write.
func TestGrokStaleLeaseIsDeniedToEveryoneButItsOwner(t *testing.T) {
	t.Run("another instance", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		lease, generation, op := g.holdExecution(t, logoutAction)
		g.restart(t)
		g.recoverQueued(t, queuedKinds[0], op)
		if err := g.finishAsInstance(lease, domain.NewID()); err == nil {
			t.Fatal("another instance released the stale lease")
		}
		_, a := g.record()
		requireStaleFence(t, a, lease, generation)
	})
	t.Run("not fenced", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		lease, generation := g.holdPlainExecution(t)
		g.service.subscriptionEpoch = domain.NewID() // a new epoch without the startup fence
		if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err == nil {
			t.Fatal("a stale lease on an unfenced account was released")
		}
		_, a := g.record()
		if a.Subscription.Lease == nil || a.Subscription.Generation != generation {
			t.Fatal("the denied Finish changed the account")
		}
	})
	t.Run("a current lease is unchanged by the stale path", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		lease, generation := g.holdPlainExecution(t)
		if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, true); err != nil {
			t.Fatalf("a current lease could not finish: %v", err)
		}
		_, a := g.record()
		if a.Subscription.Lease != nil || a.Subscription.Generation != generation {
			t.Fatal("a current lease finish changed more than the lease")
		}
	})
}

// The restart fences only Grok leases; the fence is idempotent and recorded once.
func TestGrokRestartFencesAStaleLeaseOnce(t *testing.T) {
	g := newGrokLoginFixture(t)
	lease, generation := g.holdPlainExecution(t)
	g.restart(t)
	firstRecord, first := g.record()
	requireStaleFence(t, first, lease, generation)
	if err := g.service.initializeServerSubscriptions(ownerContext()); err != nil {
		t.Fatal(err)
	}
	secondRecord, second := g.record()
	if secondRecord.Revision != firstRecord.Revision || second.Subscription.RecoveryRequired != first.Subscription.RecoveryRequired || second.Health != first.Health {
		t.Fatal("a second startup changed the fenced account")
	}
}
