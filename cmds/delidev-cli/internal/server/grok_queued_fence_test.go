// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// A Finish that released its lease and then failed (the removal of the
// superseded generation, or a lost acknowledgement) fences the account without
// a lease. The accepted operation survives that fence: the scanner finishes the
// owed cleanup, clears the fence and lets the operation proceed.
func TestGrokQueuedOperationSurvivesAFailedCleanupAfterTheExecutionFinish(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, generation, op := g.holdExecution(t, kind.action)
			published := domain.NewID()
			g.secrets.afterPut = func() {
				g.secrets.mu.Lock()
				g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
				g.secrets.mu.Unlock()
			}
			if _, err := g.finishWith(lease, published, g.grokRotatedEnvelope(published), true, false, true); err == nil {
				t.Fatal("a failed removal of the superseded generation was reported as success")
			}
			_, a := g.record()
			if a.Subscription.Lease != nil || a.Subscription.Generation != published || !a.Subscription.RecoveryRequired || !grokQueuedFenceNeedsReconcile(a) {
				t.Fatal("the committed Finish did not leave the leaseless queued fence")
			}
			g.requireQueued(t, op)
			// The fenced account is not claimed, and the reconcile cannot clear the
			// fence while the removal keeps failing.
			g.service.runServerSubscription(ownerContext(), g.input.AccountID)
			g.requireQueued(t, op)
			if err := g.service.reconcileGrokQueuedFence(ownerContext(), g.input.AccountID, op); err == nil {
				t.Fatal("the fence was cleared although the superseded generation could not be removed")
			}
			if _, a := g.record(); !a.Subscription.RecoveryRequired || !g.references()[generation] {
				t.Fatal("a failed reconcile cleared the fence or lost the superseded generation")
			}
			g.secrets.mu.Lock()
			g.secrets.deleteError = nil
			g.secrets.mu.Unlock()
			if err := g.service.reconcileGrokQueuedFence(ownerContext(), g.input.AccountID, op); err != nil {
				t.Fatal(err)
			}
			_, a = g.record()
			want := domain.AccountRevoked
			if kind.action != logoutAction {
				want = a.Subscription.ServerOperation.HealthBefore
			}
			if a.Subscription.RecoveryRequired || a.Health != want || a.Subscription.Generation != published {
				t.Fatalf("the reconcile left the fence or the wrong health %v", a.Health)
			}
			if refs := g.references(); len(refs) != 1 || !refs[published] {
				t.Fatal("the reconcile kept a superseded generation or removed the published one")
			}
			g.requireQueued(t, op)
			g.requireProceeded(t, kind, published)
		})
	}
}

// The subscription scanner performs the reconcile itself with bounded backoff,
// so no caller has to invoke recovery for the stranded cell.
func TestGrokSubscriptionScannerReconcilesAQueuedFenceUntilTheRemovalSucceeds(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.service.serverCleanupRetryBase = 10 * time.Millisecond
	lease, _, op := g.holdExecution(t, logoutAction)
	published := domain.NewID()
	g.secrets.afterPut = func() {
		g.secrets.mu.Lock()
		g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
		g.secrets.mu.Unlock()
	}
	if _, err := g.finishWith(lease, published, g.grokRotatedEnvelope(published), true, false, true); err == nil {
		t.Fatal("a failed removal was reported as success")
	}
	ctx, stop := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	go func() { defer close(done); g.service.runServerSubscriptions(ctx) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	time.Sleep(200 * time.Millisecond)
	if _, a := g.record(); !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.NativeStarted || g.idp.RevokeRequests() != 0 {
		t.Fatal("the scanner advanced the logout although the removal still failed")
	}
	g.secrets.mu.Lock()
	g.secrets.deleteError = nil
	g.secrets.mu.Unlock()
	waitUntil(t, "the scanner to complete the queued logout", func() bool {
		_, a := g.record()
		return a.Connection == nil && a.Subscription.ServerOperation.ID == op && a.Subscription.ServerOperation.State == domain.SubscriptionSucceeded
	})
	if g.idp.RevokeRequests() != 1 {
		t.Fatalf("the logout sent %d revocation requests, want 1", g.idp.RevokeRequests())
	}
	g.requireDisconnected(t)
}

// An execution whose cleanup the Worker could not confirm keeps its lease fenced,
// and the queued operation keeps waiting: nothing is revoked or refreshed while a
// private home may still hold the credential.
func TestGrokQueuedOperationWaitsForAnUnconfirmedExecutionCleanup(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, generation, op := g.holdExecution(t, kind.action)
			if _, err := g.finishWith(lease, domain.NewID(), nil, false, false, false); err != nil {
				t.Fatal(err)
			}
			_, a := g.record()
			if a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired || a.Subscription.Generation != generation || grokQueuedFenceNeedsReconcile(a) {
				t.Fatal("the unconfirmed cleanup did not keep the lease fenced")
			}
			g.service.runServerSubscription(ownerContext(), g.input.AccountID)
			if err := g.service.reconcileGrokQueuedFence(ownerContext(), g.input.AccountID, op); err == nil {
				t.Fatal("a fence that still holds a lease was reconciled")
			}
			g.requireQueued(t, op)
			if g.idp.RevokeRequests() != 0 || g.idp.TokenRequests() != 0 || g.refs(t) != 1 {
				t.Fatal("the queued operation acted while the lease was still fenced")
			}
		})
	}
}

// A Worker instance that is lost while it holds the execution lease fences the
// lease. The queued operation waits for it exactly as for an unconfirmed cleanup.
func TestGrokQueuedOperationWaitsForALostWorker(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			_, generation, op := g.holdExecution(t, kind.action)
			if err := g.service.retainLostSubscriptionLeases(g.input.MachineID, g.instance, true); err != nil {
				t.Fatal(err)
			}
			_, a := g.record()
			if a.Subscription.Lease == nil || !a.Subscription.RecoveryRequired || a.Subscription.Generation != generation {
				t.Fatal("the lost Worker's lease was not fenced")
			}
			g.service.runServerSubscription(ownerContext(), g.input.AccountID)
			g.requireQueued(t, op)
			if g.idp.RevokeRequests() != 0 || g.idp.TokenRequests() != 0 {
				t.Fatal("the queued operation acted while the lost Worker's lease was fenced")
			}
		})
	}
}

// A restart while the operation waits behind the execution lease fences both.
// The operation never reached its claim, so it settles as a failed operation with
// the credential intact; the lease predates the restart, so it stays fenced for
// its original Worker.
func TestGrokRestartSettlesAnOperationQueuedBehindAnExecutionLease(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			_, generation, op := g.holdExecution(t, kind.action)
			g.restart(t)
			ctx := ownerContext()
			switch kind.action {
			case logoutAction:
				if err := g.service.recoverGrokLogout(ctx, g.input.AccountID, op); err != nil {
					t.Fatal(err)
				}
			default:
				if _, a := g.record(); !grokRefreshNeedsRecovery(a) {
					t.Fatal("a restarted refresh waiting for an execution lease is not recoverable")
				}
				if err := g.service.recoverGrokRefresh(ctx, g.input.AccountID, op); err != nil {
					t.Fatal(err)
				}
			}
			_, a := g.record()
			st := a.Subscription
			if st.Pending != nil || st.ServerOperation.State != domain.SubscriptionFailed || st.Lease == nil || !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Generation != generation || g.idp.RevokeRequests() != 0 || g.idp.TokenRequests() != 0 {
				t.Fatalf("the restarted operation was not settled with the stale lease fenced: %+v", st.ServerOperation)
			}
		})
	}
}

// A restart that finds the leaseless fence of a Finish that released its lease
// and then failed settles the queued operation like any other unclaimed one.
func TestGrokRestartSettlesAnOperationFencedAfterTheExecutionFinish(t *testing.T) {
	for _, kind := range queuedKinds {
		t.Run(kind.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			lease, _, op := g.holdExecution(t, kind.action)
			published := domain.NewID()
			g.secrets.afterPut = func() {
				g.secrets.mu.Lock()
				g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
				g.secrets.mu.Unlock()
			}
			if _, err := g.finishWith(lease, published, g.grokRotatedEnvelope(published), true, false, true); err == nil {
				t.Fatal("a failed removal was reported as success")
			}
			g.restart(t)
			ctx := ownerContext()
			switch kind.action {
			case logoutAction:
				if err := g.service.recoverGrokLogout(ctx, g.input.AccountID, op); err != nil {
					t.Fatal(err)
				}
			default:
				if err := g.service.recoverGrokRefresh(ctx, g.input.AccountID, op); err != nil {
					t.Fatal(err)
				}
			}
			_, a := g.record()
			st := a.Subscription
			if st.Pending != nil || st.ServerOperation.State != domain.SubscriptionFailed || st.Lease != nil || st.RecoveryRequired || st.Generation != published || g.idp.RevokeRequests() != 0 {
				t.Fatalf("the restarted operation was not settled with the credential intact: %+v", st.ServerOperation)
			}
		})
	}
}
