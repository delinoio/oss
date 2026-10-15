// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// failSettlementAndFence makes the next settlement fail and every fence write
// fail until the returned function is called.
func (g *grokLoginFixture) failSettlementAndFence(settlement grokFaultPoint) (restore func()) {
	g.service.grokFaultHook = func(p grokFaultPoint) error {
		if p == settlement || p == grokFaultFenceWrite {
			return errors.New("injected failure")
		}
		return nil
	}
	return func() { g.service.grokFaultHook = nil }
}

func (g *grokLoginFixture) runScanner(t *testing.T) {
	t.Helper()
	ctx, stop := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	go func() { defer close(done); g.service.runServerSubscriptions(ctx) }()
	t.Cleanup(func() { stop(); awaitServerFixture(t, done) })
}

// The operation is neither settled nor fenced when both its settlement and its
// first fence write fail. No selection matched it and nothing retried until a
// restart, so the scanner now repeats the fence.
func TestGrokOrphanedRefreshIsFencedByTheScanner(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	restore := g.failSettlementAndFence(grokFaultAfterStage)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	restore()
	_, a := g.record()
	if a.Subscription.RecoveryRequired || !grokOrphanedOperation(a, g.service.subscriptionServerEpoch()) {
		t.Fatal("the failed fence did not leave an orphaned native-started refresh")
	}
	g.runScanner(t)
	waitUntil(t, "the scanner to fence the orphaned refresh", func() bool {
		_, a := g.record()
		return a.Subscription.RecoveryRequired
	})
	_, a = g.record()
	if a.Health != domain.AccountFailed || a.Subscription.ServerOperation.State != domain.SubscriptionRecovery || grokOrphanedOperation(a, g.service.subscriptionServerEpoch()) || g.idp.TokenRequests() != 2 {
		t.Fatal("the orphaned refresh was not fenced without another token request")
	}
	g.requireGrokDiagnostic(t, op.OperationId, pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH, string(domain.RecoveryRequired))
}

// A logout whose proven revocation cleanup fails and whose fence write fails
// keeps its lease, claim and revocation; the scanner fences it and the existing
// cleanup recovery then completes it without a second revocation.
func TestGrokOrphanedLogoutIsFencedThenItsCleanupCompletes(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.secrets.mu.Lock()
	g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
	g.secrets.mu.Unlock()
	op := g.serverStart(logoutAction)
	restore := g.failSettlementAndFence(grokFaultFenceWrite)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	restore()
	_, a := g.record()
	if a.Subscription.RecoveryRequired || !grokOrphanedOperation(a, g.service.subscriptionServerEpoch()) || a.Subscription.ServerOperation.GrokRevocation != domain.GrokRevocationConfirmed {
		t.Fatal("the failed fence did not leave an orphaned logout with a proven revocation")
	}
	g.runScanner(t)
	waitUntil(t, "the scanner to fence the orphaned logout", func() bool {
		_, a := g.record()
		return a.Subscription.RecoveryRequired
	})
	g.secrets.mu.Lock()
	g.secrets.deleteError = nil
	g.secrets.mu.Unlock()
	waitUntil(t, "the fenced logout to finish its cleanup", func() bool {
		_, a := g.record()
		return a.Connection == nil && !a.Subscription.RecoveryRequired
	})
	if g.idp.RevokeRequests() != 1 {
		t.Fatalf("the orphaned logout revoked %d times", g.idp.RevokeRequests())
	}
	g.requireDisconnected(t)
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatalf("progress = %v", p.State)
	}
}

func TestGrokOrphanedOperationSelectionIsExact(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	epoch := g.service.subscriptionServerEpoch()
	_, queued := g.record()
	if grokOrphanedOperation(queued, epoch) {
		t.Fatal("a queued, unclaimed refresh was selected as orphaned")
	}
	claimed := queued
	st := *claimed.Subscription
	o := *st.ServerOperation
	p := *st.Pending
	o.NativeStarted, p.Phase = true, domain.SubscriptionClaimed
	st.ServerOperation, st.Pending = &o, &p
	claimed.Subscription = &st
	if !grokOrphanedOperation(claimed, epoch) {
		t.Fatal("a claimed, native-started, unfenced refresh was not selected")
	}
	for name, mutate := range map[string]func(*domain.Account){
		"fenced":        func(a *domain.Account) { a.Subscription.RecoveryRequired = true },
		"other epoch":   func(a *domain.Account) { a.Subscription.ServerOperation.Epoch = domain.NewID() },
		"settled":       func(a *domain.Account) { a.Subscription.ServerOperation.State = domain.SubscriptionFailed },
		"no pending":    func(a *domain.Account) { a.Subscription.Pending = nil },
		"leased":        func(a *domain.Account) { a.Subscription.Lease = &domain.SubscriptionLease{} },
		"not started":   func(a *domain.Account) { a.Subscription.ServerOperation.NativeStarted = false },
		"removal":       func(a *domain.Account) { a.Removal = &domain.AccountRemoval{} },
		"other service": func(a *domain.Account) { a.SubscriptionService = domain.SubscriptionChatGPT },
	} {
		t.Run(name, func(t *testing.T) {
			a := claimed
			s := *claimed.Subscription
			oo := *s.ServerOperation
			pp := *s.Pending
			s.ServerOperation, s.Pending = &oo, &pp
			a.Subscription = &s
			mutate(&a)
			if grokOrphanedOperation(a, epoch) {
				t.Fatal("an operation that is not an orphan was selected")
			}
		})
	}
}
