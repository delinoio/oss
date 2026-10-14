// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

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
