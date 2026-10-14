// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// A restart fences an unclaimed refresh; the subscription scanner must settle it
// the way it settles an unclaimed login, without any caller invoking recovery.
func TestGrokSubscriptionScannerRecoversAnUnclaimedRefresh(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.restart(t)
	_, a := g.record()
	if !grokRefreshNeedsRecovery(a) {
		t.Fatal("an unclaimed fenced refresh is not selected for recovery")
	}
	ctx, stop := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	go func() { defer close(done); g.service.runServerSubscriptions(ctx) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	waitUntil(t, "the scanner to settle the unclaimed refresh", func() bool {
		_, a := g.record()
		return !a.Subscription.RecoveryRequired
	})
	_, a = g.record()
	st := a.Subscription
	if st.Pending != nil || st.ServerOperation.State != domain.SubscriptionFailed || a.Health != domain.AccountReady || st.Generation != before.Subscription.Generation || g.idp.TokenRequests() != 1 {
		t.Fatal("the scanner did not free the unclaimed refresh without a token request")
	}
	if p := g.progressFor(op.OperationId); p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH || p.GrokDiagnostic.Code != string(domain.Unavailable) {
		t.Fatal("the recovered refresh lacks its diagnostic")
	}
}
