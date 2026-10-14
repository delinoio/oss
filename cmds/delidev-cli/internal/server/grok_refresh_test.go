// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokCrash is the sentinel a fault hook panics with to end the refresh at an
// injected point, as a process crash would.
type grokCrash struct{}

func ownerContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

// connected logs the account in and returns its connected state.
func (g *grokLoginFixture) connected(t *testing.T) domain.Account {
	t.Helper()
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	// The login closes its listener as soon as the callback is accepted, so a
	// loaded host can reset the callback connection after the code was taken. The
	// login outcome below is what this fixture asserts, not the browser response.
	_, _ = callback(run.port, http.MethodGet, run.good(grant.Code), "")
	g.awaitDone(t, run)
	_, a := g.record()
	if a.Connection == nil || a.Subscription.Generation == "" || g.idp.TokenRequests() != 1 {
		t.Fatal("login did not connect the account")
	}
	return a
}

// refresh requests a refresh and runs it to its settled outcome in this goroutine.
func (g *grokLoginFixture) refresh(t *testing.T) *pb.RequestSubscriptionResponse {
	t.Helper()
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	return op
}

// crashAt runs a requested refresh and ends it at the named point.
func (g *grokLoginFixture) crashAt(t *testing.T, point grokFaultPoint) *pb.RequestSubscriptionResponse {
	t.Helper()
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.service.grokFaultHook = func(p grokFaultPoint) error {
		if p == point {
			panic(grokCrash{})
		}
		return nil
	}
	crashed := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(grokCrash); !ok {
					panic(r)
				}
				crashed = true
			}
		}()
		g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	}()
	g.service.grokFaultHook = nil
	if !crashed {
		t.Fatalf("the refresh never reached %s", point)
	}
	return op
}

// restart simulates a server restart: a new epoch and the startup fence.
func (g *grokLoginFixture) restart(t *testing.T) {
	t.Helper()
	g.service.subscriptionEpoch = domain.NewID()
	if err := g.service.initializeServerSubscriptions(ownerContext()); err != nil {
		t.Fatal(err)
	}
}

func (g *grokLoginFixture) storedBundle(t *testing.T, generation domain.ID) subscription.GrokBundle {
	t.Helper()
	raw, err := g.secrets.Get(context.Background(), credentials.Ref{Owner: g.input.AccountID, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenGrokBundle(raw, g.service.Identity.ServerID, g.input.AccountID, generation)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func refreshTokenOf(t *testing.T, b subscription.GrokBundle) string {
	t.Helper()
	token := b.RefreshTokenBytes()
	defer clear(token)
	return string(token)
}

// requireNoRefreshLeak scans the structured logs and the durable metadata for
// every token, identity and path value of the refresh.
func (g *grokLoginFixture) requireNoRefreshLeak(t *testing.T, op *pb.RequestSubscriptionResponse) {
	t.Helper()
	secrets := []string{"fake-access-token", "fake-refresh-token", "eyJ", "fake-user-1", "fixture@example.invalid", "Fixture", g.service.Store.Root(), "oauth2/token", "oauth2/userinfo", "refresh_token=", "grant_type"}
	logs := g.logs.String()
	if !strings.Contains(logs, `"phase":"REFRESH"`) || !strings.Contains(logs, op.OperationId) || !strings.Contains(logs, `"correlation_id":"`+op.OperationId+`"`) {
		t.Fatal("logs lack the REFRESH phase or the operation correlation id")
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if strings.Contains(line, `"phase":"REFRESH"`) && !strings.Contains(line, `"operation_id"`) {
			t.Fatal("a REFRESH record lacks the operation id")
		}
		for _, secret := range secrets {
			if strings.Contains(line, secret) && strings.Contains(line, "REFRESH") {
				t.Fatalf("a secret, identity or path value reached a REFRESH log record: %.8s", secret)
			}
		}
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(g.service.Store.Root(), name))
		if err != nil {
			continue
		}
		for _, secret := range []string{"fake-access-token", "fake-refresh-token", "refresh_token="} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("a token reached durable metadata: %.8s", secret)
			}
		}
	}
}

func TestGrokRefreshReplacesTheBundleWithRotation(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	old := g.storedBundle(t, before.Subscription.Generation)
	op := g.refresh(t)
	_, a := g.record()
	st := a.Subscription
	o := st.ServerOperation
	if o.ID != domain.ID(op.OperationId) || o.Action != domain.SubscriptionRefresh || o.State != domain.SubscriptionSucceeded || !o.RefreshClaimed || o.NativeStarted || o.CodeConsumed || o.GrokDiagnostic != nil {
		t.Fatalf("operation did not settle as a claimed, succeeded refresh: %+v", o)
	}
	if st.Pending != nil || st.Lease != nil || st.RecoveryRequired || a.Connection == nil || a.Health != domain.AccountReady || st.Generation != o.FinishID || st.Generation == before.Subscription.Generation || st.IdentityCommitment != before.Subscription.IdentityCommitment {
		t.Fatal("account document does not carry the refreshed generation under the same identity")
	}
	next := g.storedBundle(t, st.Generation)
	if err := subscription.GrokRefreshed(old, next); err != nil {
		t.Fatal("the stored bundle is not a refresh of the original")
	}
	if got := refreshTokenOf(t, next); got != g.idp.CurrentRefreshToken() || got == "fake-refresh-token" {
		t.Fatal("rotation was not stored")
	}
	if g.refs(t) != 1 || g.idp.TokenRequests() != 2 {
		t.Fatalf("unexpected protected generations (%d) or token requests (%d)", g.refs(t), g.idp.TokenRequests())
	}
	form := g.idp.LastTokenForm()
	if len(form) != 3 || form.Get("grant_type") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || form.Get("refresh_token") != "fake-refresh-token" || form.Get("client_secret") != "" || g.idp.LastTokenHeaders().Get("Authorization") != "" {
		t.Fatal("the request was not a public-client refresh_token grant")
	}
	if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != string(st.Generation) || p.GrokDiagnostic != nil || p.SuggestedName != "" {
		t.Fatalf("progress is wrong: %+v", p)
	}
	g.requireNoRefreshLeak(t, op)
}

func TestGrokRefreshWithoutRotationKeepsTheRefreshToken(t *testing.T) {
	for name, mode := range map[string]grokidp.RefreshMode{"omitted": grokidp.RefreshOmit, "unchanged": grokidp.RefreshUnchanged} {
		t.Run(name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			before := g.connected(t)
			g.idp.RefreshMode = mode
			op := g.refresh(t)
			_, a := g.record()
			if a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded || a.Subscription.Generation == before.Subscription.Generation {
				t.Fatal("an unrotated refresh was not published")
			}
			if refreshTokenOf(t, g.storedBundle(t, a.Subscription.Generation)) != "fake-refresh-token" {
				t.Fatal("the current refresh token was not kept")
			}
			if g.refs(t) != 1 || g.idp.TokenRequests() != 2 {
				t.Fatal("unexpected protected generations or token requests")
			}
			g.requireNoRefreshLeak(t, op)
		})
	}
}

func TestGrokRefreshWorksWithoutAnIDToken(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.idp.OmitRefreshIDToken = true
	g.refresh(t)
	_, a := g.record()
	if a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded || a.Subscription.Generation == before.Subscription.Generation {
		t.Fatal("a refresh verified through userinfo was not published")
	}
}

// Every failure leaves the stored bundle untouched and sends exactly one
// request. A provider refusal is a plain failure; everything that leaves the
// rotation unknown is RECOVERY_REQUIRED, and the claimed token is never resent.
func TestGrokRefreshFailuresNeverReplaceTheBundle(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(*grokidp.IdP)
		recovery  bool
		health    domain.AccountHealth
		diagnosis domain.Code
	}{
		{"invalid_grant", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 400, `{"error":"invalid_grant"}` }, false, domain.AccountExpired, domain.Unauthenticated},
		{"server error", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, "unavailable" }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"truncated body", func(i *grokidp.IdP) { i.TruncateTokenBody = true }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"malformed success", func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"identity mismatch", func(i *grokidp.IdP) { i.Subject = "someone-else" }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"principal mismatch", func(i *grokidp.IdP) { i.PrincipalType, i.PrincipalID = "team-member", "p-2" }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"unverifiable ID token", func(i *grokidp.IdP) { i.SignWithWrongKey = true }, true, domain.AccountFailed, domain.RecoveryRequired},
		{"userinfo failure", func(i *grokidp.IdP) { i.UserInfoStatus = 500 }, true, domain.AccountFailed, domain.RecoveryRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			before := g.connected(t)
			c.setup(g.idp)
			op := g.refresh(t)
			_, a := g.record()
			st := a.Subscription
			o := st.ServerOperation
			p := g.progressFor(op.OperationId)
			if st.Generation != before.Subscription.Generation || a.Connection == nil || g.refs(t) != 1 || !o.RefreshClaimed || st.IdentityCommitment != before.Subscription.IdentityCommitment {
				t.Fatal("the stored bundle was replaced or the claim is missing")
			}
			if refreshTokenOf(t, g.storedBundle(t, st.Generation)) != "fake-refresh-token" {
				t.Fatal("the stored refresh token changed")
			}
			if c.recovery {
				if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || !st.RecoveryRequired || st.Pending == nil || !o.NativeStarted || a.Health != c.health {
					t.Fatalf("an uncertain refresh did not require recovery: %v", p.State)
				}
			} else if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED || st.RecoveryRequired || st.Pending != nil || o.NativeStarted || a.Health != c.health {
				t.Fatalf("a provider refusal did not settle as a plain failure: %v", p.State)
			}
			if p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(c.diagnosis) || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH || p.GrokDiagnostic.CorrelationId != op.OperationId {
				t.Fatalf("diagnostic is wrong: %+v", p.GrokDiagnostic)
			}
			// Nothing resends the claimed token: not the scanner entry point, not recovery.
			g.service.runServerSubscription(ownerContext(), g.input.AccountID)
			if grokRefreshNeedsRecovery(a) || c.recovery && g.service.recoverGrokRefresh(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)) == nil {
				t.Fatal("a claimed refresh was released automatically")
			}
			if g.idp.TokenRequests() != 2 || len(g.idp.RefreshTokensPresented()) != 1 {
				t.Fatal("the refresh token was sent more than once")
			}
			g.requireNoRefreshLeak(t, op)
		})
	}
}

func TestGrokRefreshProviderFailureBeforeTheClaimSendsNothing(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.idp.DiscoveryIssuer = "https://other.example"
	op := g.refresh(t)
	_, a := g.record()
	st := a.Subscription
	if st.ServerOperation.State != domain.SubscriptionFailed || st.ServerOperation.RefreshClaimed || st.Pending != nil || st.RecoveryRequired || st.Generation != before.Subscription.Generation || a.Health != domain.AccountReady || g.idp.TokenRequests() != 1 {
		t.Fatal("a refresh that never reached the claim was not a plain failure with no token request")
	}
	if p := g.progressFor(op.OperationId); p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.Unavailable) || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH {
		t.Fatal("the failure lacks its REFRESH diagnostic")
	}
	// The unspent credential can be refreshed by a later request.
	g.idp.DiscoveryIssuer = ""
	g.refresh(t)
	if _, a = g.record(); a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("the credential was not refreshable after a pre-claim failure")
	}
}

func TestGrokRefreshWritesTheClaimBeforeTheTokenRequestAndHoldsTheLease(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.idp.TokenGate = make(chan struct{})
	g.idp.TokenEntered = make(chan struct{}, 1)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.service.runServerSubscription(ownerContext(), g.input.AccountID)
	}()
	select {
	case <-g.idp.TokenEntered:
	case <-done:
		t.Fatal("the refresh ended without a token request")
	}
	// The token endpoint is entered: the durable claim and the exclusive lease
	// are already committed.
	_, a := g.record()
	st := a.Subscription
	if o := st.ServerOperation; !o.RefreshClaimed || !o.NativeStarted || o.Action != domain.SubscriptionRefresh || st.Pending == nil || st.Pending.Phase != domain.SubscriptionClaimed || st.Pending.MachineID != "" {
		t.Fatal("the exchange claim was not durable before the token request")
	}
	// The lease is exclusive: no other operation can start or take the account.
	r, _ := g.record()
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); err == nil {
		t.Fatal("a second refresh was accepted while one holds the lease")
	}
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT})); err == nil {
		t.Fatal("logout was accepted while a refresh holds the lease")
	}
	close(g.idp.TokenGate)
	awaitServerFixture(t, done)
	_, a = g.record()
	if a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded || a.Subscription.Generation == before.Subscription.Generation || g.idp.TokenRequests() != 2 {
		t.Fatal("the refresh did not complete")
	}
	_ = op
}

// A crash at each injected point ends with exactly one token request. After the
// claim and after the response the rotation is unknown, so the account needs
// recovery; after staging the vault holds an unreferenced generation and the
// account still needs recovery; after the commit the outcome is proven.
func TestGrokRefreshFaultInjectionSendsOneRequestAndFencesUncertainOutcomes(t *testing.T) {
	for _, point := range []grokFaultPoint{grokFaultAfterClaim, grokFaultAfterResponse, grokFaultAfterStage} {
		t.Run(string(point), func(t *testing.T) {
			g := newGrokLoginFixture(t)
			before := g.connected(t)
			op := g.crashAt(t, point)
			g.restart(t)
			_, a := g.record()
			st := a.Subscription
			if !st.RecoveryRequired || a.Health != domain.AccountFailed || st.Generation != before.Subscription.Generation || !st.ServerOperation.RefreshClaimed || grokRefreshNeedsRecovery(a) {
				t.Fatal("an uncertain refresh is not retained as recovery-required")
			}
			if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
				t.Fatalf("progress state = %v", p.State)
			}
			// Neither the scanner nor recovery resend the claim.
			g.service.runServerSubscription(ownerContext(), g.input.AccountID)
			if g.service.recoverGrokRefresh(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)) == nil {
				t.Fatal("recovery released a claimed refresh")
			}
			// A crash right after the claim ended before the request left the
			// process, so no refresh was sent at all; every later point sent one.
			sent := 1
			if point == grokFaultAfterClaim {
				sent = 0
			}
			if g.idp.TokenRequests() != 1+sent || len(g.idp.RefreshTokensPresented()) != sent {
				t.Fatal("the refresh token was sent more than once, or again after the restart")
			}
			// Whatever is staged is never the account's current generation.
			if point == grokFaultAfterStage && g.refs(t) != 2 {
				t.Fatal("the staged generation is missing")
			}
			if refreshTokenOf(t, g.storedBundle(t, st.Generation)) != "fake-refresh-token" {
				t.Fatal("the current generation changed")
			}
			g.requireNoRefreshLeak(t, op)
		})
	}
	t.Run(string(grokFaultAfterPersist), func(t *testing.T) {
		g := newGrokLoginFixture(t)
		before := g.connected(t)
		op := g.crashAt(t, grokFaultAfterPersist)
		g.restart(t)
		_, a := g.record()
		st := a.Subscription
		if st.RecoveryRequired || st.Pending != nil || st.Generation == before.Subscription.Generation || st.ServerOperation.State != domain.SubscriptionSucceeded || a.Health != domain.AccountReady {
			t.Fatal("a refresh that committed before the crash is not published")
		}
		if g.idp.TokenRequests() != 2 || g.refs(t) != 2 {
			t.Fatal("expected one refresh request and the superseded generation awaiting cleanup")
		}
		if p := g.progressFor(op.OperationId); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
			t.Fatal("progress does not report the committed refresh")
		}
		// The next refresh removes the superseded generation left by the crash.
		g.refresh(t)
		if g.refs(t) != 1 || g.idp.TokenRequests() != 3 {
			t.Fatal("the next refresh did not clean up the superseded generation")
		}
		if presented := g.idp.RefreshTokensPresented(); len(presented) != 2 || presented[0] == presented[1] {
			t.Fatal("the rotated token was not the one sent second")
		}
	})
}

// A restart before the claim sent nothing: the credential is intact and the
// refresh settles as a failed one, after which the account refreshes normally.
func TestGrokRefreshRestartBeforeTheClaimFreesTheOperation(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	g.restart(t)
	_, a := g.record()
	if !a.Subscription.RecoveryRequired || !grokRefreshNeedsRecovery(a) {
		t.Fatal("an unclaimed interrupted refresh is not recoverable")
	}
	if err := g.service.recoverGrokRefresh(ownerContext(), g.input.AccountID, domain.ID(op.OperationId)); err != nil {
		t.Fatal(err)
	}
	_, a = g.record()
	if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Health != domain.AccountReady || a.Subscription.Generation != before.Subscription.Generation || a.Subscription.ServerOperation.State != domain.SubscriptionFailed || g.idp.TokenRequests() != 1 {
		t.Fatal("the unclaimed refresh was not freed")
	}
	if p := g.progressFor(op.OperationId); p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH {
		t.Fatal("the recovered refresh lacks its diagnostic")
	}
	g.refresh(t)
	if _, a = g.record(); a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("the account could not refresh after recovery")
	}
}

func TestGrokRefreshCleanupFailureAfterThePublishDoesNotUndoIt(t *testing.T) {
	g := newGrokLoginFixture(t)
	before := g.connected(t)
	g.secrets.mu.Lock()
	g.secrets.deleteError = domain.Fail(domain.Unavailable, "The vault removal failed.", "")
	g.secrets.mu.Unlock()
	g.refresh(t)
	_, a := g.record()
	if a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded || a.Subscription.Generation == before.Subscription.Generation || a.Subscription.RecoveryRequired {
		t.Fatal("a failed removal undid or fenced a committed refresh")
	}
	if g.refs(t) != 2 {
		t.Fatal("the superseded generation should remain unreferenced awaiting cleanup")
	}
	if !strings.Contains(g.logs.String(), "grok_refresh_cleanup_pending") {
		t.Fatal("the pending cleanup was not logged")
	}
}

func TestGrokRefreshIsAvailableOnlyToAConnectedGrokAccount(t *testing.T) {
	g := newGrokLoginFixture(t)
	r, _ := g.record()
	request := func(action pb.SubscriptionAction) error {
		r, _ = g.record()
		_, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
		return err
	}
	if request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH) == nil {
		t.Fatal("an unconnected account accepted a refresh")
	}
	g.connected(t)
	if request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT) == nil {
		t.Fatal("server-owned Grok logout is not available yet")
	}
	if err := request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
		t.Fatal(err)
	}
	_, a := g.record()
	st := a.Subscription
	if st.Pending == nil || st.Pending.MachineID != "" || st.Pending.DeviceCode || st.ServerOperation.Action != domain.SubscriptionRefresh || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.RefreshClaimed || g.idp.TokenRequests() != 1 {
		t.Fatal("the request did not queue an unclaimed server refresh")
	}
	// A refresh cannot be canceled by the login-only cancellation RPC.
	if _, err := g.client.CancelSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err == nil {
		t.Fatal("a queued refresh was canceled through the login cancellation path")
	}
}

func TestGrokRefreshDiagnosticsUseFixedSafeText(t *testing.T) {
	op := domain.NewID()
	for _, code := range []domain.Code{domain.RecoveryRequired, domain.Unauthenticated, domain.Unavailable, domain.Conflict, domain.Internal} {
		message := grokDiagnosticMessage(&domain.GrokDiagnostic{Phase: domain.GrokDiagnosticRefresh, Code: code, CorrelationID: op})
		if message == nil || message.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH || message.Message == "" || message.Guidance == "" || strings.Contains(message.Message, "sign-in did not complete") && code != domain.Internal {
			t.Fatalf("refresh diagnostic for %s is wrong: %+v", code, message)
		}
	}
}
