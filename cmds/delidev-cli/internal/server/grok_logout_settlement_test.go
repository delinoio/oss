// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The final disconnect committed but reported a lost commit acknowledgement. The
// account is already disconnected with its generation and revocation marker
// cleared, so the fallback must reconcile it as the committed outcome instead of
// rewriting it to RECOVERY_REQUIRED with a pending placeholder.
func TestGrokLogoutAmbiguousDisconnectCommitIsReconciledNotFenced(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.connected(t)
	g.failAt(grokFaultAfterLogoutDisconnect)
	op := g.logout(t)
	g.service.grokFaultHook = nil
	g.requireDisconnected(t)
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.GrokDiagnostic != nil {
		t.Fatalf("progress does not report the committed logout: %v", p.State)
	}
	if g.idp.RevokeRequests() != 1 {
		t.Fatal("the token was revoked more than once")
	}
	// Nothing blocks the next login, deletion or logout request.
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err != nil {
		t.Fatal("a reconciled logout still blocks a new login", err)
	}
}

// The revocation claim committed but reported a lost commit acknowledgement. The
// durable claim decides: the logout is uncertain and fenced, so no later logout
// can overwrite the claim and send the same refresh token again.
func TestGrokLogoutAmbiguousClaimCommitStaysClaimedAndFenced(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.failAt(grokFaultAfterRevokeClaimCommit)
	op := g.logout(t)
	g.service.grokFaultHook = nil
	if g.idp.RevokeRequests() != 0 {
		t.Fatal("a revocation was sent after a claim error")
	}
	g.requireRetained(t, before, domain.GrokRevocationClaimed)
	p := g.progressFor(op.OperationId)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
		t.Fatalf("progress lacks the LOGOUT recovery diagnostic: %v", p.State)
	}
	_, a := g.record()
	if grokLogoutRecoveryKind(a) != grokLogoutNone {
		t.Fatal("a claimed revocation is automatically recoverable")
	}
	// The record describes what was stored, not the stale unclaimed failure.
	finished := ""
	for _, line := range strings.Split(g.logs.String(), "\n") {
		if strings.Contains(line, `"msg":"grok_logout_finished"`) {
			finished = line
		}
	}
	if !strings.Contains(finished, `"state":"recovery`) && !strings.Contains(finished, "RECOVERY") || !strings.Contains(finished, `"claimed":true`) {
		t.Fatalf("the finish record does not describe the reconciled outcome: %s", finished)
	}
	// A new logout cannot overwrite the claim while the fence stands.
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: logoutAction})); err == nil {
		t.Fatal("a second logout was accepted over a claimed revocation")
	}
	g.restart(t)
	if g.idp.RevokeRequests() != 0 {
		t.Fatal("a restart sent the claimed revocation")
	}
}

// A logout lease whose commit acknowledgement was lost is fenced so that the
// scanner settles it; nothing was sent and the credential is intact.
func TestGrokLogoutLeaseWithALostAcknowledgementIsFencedForTheScanner(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(logoutAction)
	g.failAt(grokFaultAfterLease)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	g.service.grokFaultHook = nil
	_, a := g.record()
	if !a.Subscription.RecoveryRequired || grokLogoutRecoveryKind(a) != grokLogoutUnclaimed || g.idp.RevokeRequests() != 0 {
		t.Fatal("an unowned logout lease is not fenced for recovery")
	}
	if err := g.service.recoverGrokLogout(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	_, a = g.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.Generation != before.Subscription.Generation || g.refs(t) != 1 {
		t.Fatal("the fenced logout lease was not settled with its credential intact")
	}
}

// A logout that failed before its revocation claim leaves the account connected
// and revoked. A refresh would publish a generation that cannot restore readiness,
// so only another logout may follow, and it settles normally.
func TestGrokFailedLogoutAllowsOnlyAnotherLogout(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.secrets.mu.Lock()
	saved := g.secrets.values
	g.secrets.values = map[credentials.Ref][]byte{}
	g.secrets.mu.Unlock()
	g.logout(t)
	_, a := g.record()
	if a.Health != domain.AccountRevoked || a.Subscription.Pending != nil || a.Connection == nil || a.Subscription.ServerOperation.State != domain.SubscriptionFailed {
		t.Fatal("the failed logout did not leave the account connected and revoked")
	}
	before2 := g.idp.TokenRequests()
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: g.mutation(), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); err == nil {
		t.Fatal("a refresh was accepted while the account is revoked by a failed logout")
	}
	if _, a = g.record(); a.Subscription.Pending != nil || a.Subscription.Generation != before.Subscription.Generation || g.idp.TokenRequests() != before2 {
		t.Fatal("a refused refresh changed the account or reached the provider")
	}
	g.secrets.mu.Lock()
	g.secrets.values = saved
	g.secrets.mu.Unlock()
	g.logout(t)
	g.requireDisconnected(t)
}
