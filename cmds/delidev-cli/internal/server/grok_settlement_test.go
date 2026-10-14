// SPDX-License-Identifier: Apache-2.0
package server

import (
	"errors"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// failAt makes the settlement of a refresh report an error at point, as a failed
// write (after_stage) or as a commit whose acknowledgement was lost although the
// transaction committed (after_persist).
func (g *grokLoginFixture) failAt(point grokFaultPoint) {
	g.service.grokFaultHook = func(p grokFaultPoint) error {
		if p == point {
			return errors.New("injected settlement failure")
		}
		return nil
	}
}

func (g *grokLoginFixture) requireGrokDiagnostic(t *testing.T, operation string, phase pb.GrokDiagnosticPhase, code string) {
	t.Helper()
	p := g.progressFor(operation)
	if p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != phase || p.GrokDiagnostic.Code != code || p.GrokDiagnostic.CorrelationId != operation || p.GrokDiagnostic.Message == "" || p.GrokDiagnostic.Guidance == "" {
		t.Fatalf("progress lacks the %v %s diagnostic: %+v", phase, code, p.GrokDiagnostic)
	}
}

// A claimed refresh whose settlement cannot be written stays fenced, and its
// progress carries the refresh diagnostic (not only the Codex one).
func TestGrokRefreshSettlementFailureAfterTheClaimRecordsARefreshDiagnostic(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.failAt(grokFaultAfterStage)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	g.service.grokFaultHook = nil
	_, a := g.record()
	st := a.Subscription
	if !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Pending == nil || st.Generation != before.Subscription.Generation || !st.ServerOperation.RefreshClaimed || st.ServerOperation.State != domain.SubscriptionRecovery || grokRefreshNeedsRecovery(a) {
		t.Fatal("a claimed refresh with an unwritten settlement is not fenced as recovery-required")
	}
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatalf("progress state = %v", p.State)
	}
	g.requireGrokDiagnostic(t, op.OperationId, pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH, string(domain.RecoveryRequired))
	if g.idp.TokenRequests() != 2 || len(g.idp.RefreshTokensPresented()) != 1 {
		t.Fatal("the claimed refresh token was sent more than once")
	}
}

// A lost commit acknowledgement after the refresh committed is the committed
// outcome: the account stays published and is not rewritten to recovery-required.
func TestGrokRefreshAmbiguousCommitIsReconciledNotFenced(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.failAt(grokFaultAfterPersist)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	g.service.grokFaultHook = nil
	_, a := g.record()
	st := a.Subscription
	if st.RecoveryRequired || st.Pending != nil || a.Health != domain.AccountReady || st.Generation == before.Subscription.Generation || st.ServerOperation.State != domain.SubscriptionSucceeded || st.ServerOperation.GrokDiagnostic != nil {
		t.Fatal("a committed refresh was fenced or rewritten after a lost commit acknowledgement")
	}
	if got := refreshTokenOf(t, g.storedBundle(t, st.Generation)); got == "fake-refresh-token" {
		t.Fatal("the published generation is not the rotated credential")
	}
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.GrokDiagnostic != nil {
		t.Fatal("progress does not report the committed refresh")
	}
	// The account refreshes again: nothing is fenced.
	g.refresh(t)
	if _, a = g.record(); a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("the next refresh did not run")
	}
}

// The login finish has the same rule: an exchange was claimed, the settlement
// cannot be written, and the fence carries the login diagnostic.
func TestGrokLoginSettlementFailureAfterTheClaimRecordsALoginDiagnostic(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	g.secrets.mu.Lock()
	g.secrets.putError = domain.Fail(domain.Unavailable, "The vault write failed.", "")
	g.secrets.mu.Unlock()
	_, _ = callback(run.port, http.MethodGet, run.good(grant.Code), "")
	g.awaitDone(t, run)
	_, a := g.record()
	st := a.Subscription
	if !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Pending == nil || a.Connection != nil || st.ServerOperation.State != domain.SubscriptionRecovery || !st.ServerOperation.CodeConsumed {
		t.Fatal("a claimed login with an unwritten settlement is not fenced as recovery-required")
	}
	g.requireGrokDiagnostic(t, run.op.OperationId, pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN, string(domain.RecoveryRequired))
	if g.idp.TokenRequests() != 1 {
		t.Fatal("the authorization code was sent more than once")
	}
}

// A lease whose commit acknowledgement was lost left the operation leased with no
// owner. The runner fences it, so the scanner settles it instead of waiting for a
// restart; nothing was sent to the provider.
func TestGrokLeaseWithALostAcknowledgementIsFencedForTheScanner(t *testing.T) {
	t.Run("refresh", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		before := g.connected(t)
		g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
		g.failAt(grokFaultAfterLease)
		g.service.runServerSubscription(ownerContext(), g.input.AccountID)
		g.service.grokFaultHook = nil
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.RefreshClaimed || !grokRefreshNeedsRecovery(a) || g.idp.TokenRequests() != 1 {
			t.Fatal("an unowned refresh lease is not fenced for recovery")
		}
		if err := g.service.recoverGrokRefresh(ownerContext(), g.input.AccountID, a.Subscription.ServerOperation.ID); err != nil {
			t.Fatal(err)
		}
		if _, a = g.record(); a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Subscription.Generation != before.Subscription.Generation {
			t.Fatal("the fenced refresh lease was not settled")
		}
	})
	t.Run("login", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
		g.failAt(grokFaultAfterLease)
		g.service.runServerSubscription(ownerContext(), g.input.AccountID)
		g.service.grokFaultHook = nil
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.CodeConsumed || !grokLoginNeedsRecovery(a) || g.idp.TokenRequests() != 0 {
			t.Fatal("an unowned login lease is not fenced for recovery")
		}
		if err := g.service.recoverGrokLogin(ownerContext(), g.input.AccountID, a.Subscription.ServerOperation.ID); err != nil {
			t.Fatal(err)
		}
		if _, a = g.record(); a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Connection != nil {
			t.Fatal("the fenced login lease was not settled")
		}
	})
}
