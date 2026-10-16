// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const grokRefreshAction = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH

// grokRefreshRun drives one server-owned Grok refresh over the real Connect
// handlers against the fake authorization server.
type grokRefreshRun struct {
	t          *testing.T
	f          *grokFixture
	idp        *grokidptest.IdP
	logs       *grokLogCapture
	generation domain.ID
	op         string
	done       chan struct{}
	cancel     context.CancelFunc
}

// boundGrokBundle is the connected account's credential: the fake server's
// identity and tokens, created five hours ago so a refresh moves it forward.
func boundGrokBundle(t *testing.T, idp *grokidptest.IdP) subscription.GrokBundle {
	t.Helper()
	created := time.Now().UTC().Add(-5 * time.Hour)
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": idp.AccessToken, "auth_mode": "oidc", "create_time": created.Format(time.RFC3339Nano), "expires_at": created.Add(6 * time.Hour).Format(time.RFC3339Nano),
		"user_id": idp.Subject, "principal_type": idp.PrincipalType, "principal_id": idp.PrincipalID, "email": idp.Email, "refresh_token": idp.RefreshToken,
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// newGrokRefresh connects a Grok account whose bundle matches the fake server
// and requests the refresh; the runner is not started until run is called.
func newGrokRefresh(t *testing.T) *grokRefreshRun {
	t.Helper()
	f := newGrokFixture(t)
	idp := grokidptest.New()
	f.service.grokLoginTransport = idp.Transport()
	bundle := boundGrokBundle(t, idp)
	lease, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.NewID()
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, f.input.AccountID, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finishWith(lease, generation, sealed, true, false, true); err != nil {
		t.Fatal(err)
	}
	logs := f.captureLogs()
	g := &grokRefreshRun{t: t, f: f, idp: idp, logs: logs, generation: generation}
	g.op = g.request(nil)
	return g
}

func (g *grokRefreshRun) request(edit func(*pb.RequestSubscriptionRequest)) string {
	g.t.Helper()
	response, err := g.f.grokLoginRequest(g.t, func(r *pb.RequestSubscriptionRequest) {
		r.Action = grokRefreshAction
		if edit != nil {
			edit(r)
		}
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return response.OperationId
}

func (g *grokRefreshRun) run() {
	g.t.Helper()
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	g.cancel = cancel
	g.done = make(chan struct{})
	go func() {
		defer close(g.done)
		g.f.service.runGrokRefresh(ctx, g.f.input.AccountID)
	}()
	g.t.Cleanup(func() { cancel(); <-g.done })
}

func (g *grokRefreshRun) await() {
	g.t.Helper()
	select {
	case <-g.done:
	case <-time.After(30 * time.Second):
		g.t.Fatal("refresh runner did not finish")
	}
}

// settle runs the refresh to its end.
func (g *grokRefreshRun) settle() *pb.GetSubscriptionProgressResponse {
	g.t.Helper()
	g.run()
	g.await()
	return g.f.progressFor(g.op)
}

func (g *grokRefreshRun) account() domain.Account {
	g.t.Helper()
	_, a := g.f.record()
	if err := a.Subscription.Validate(a); err != nil {
		g.t.Fatalf("persisted account violates the subscription invariants: %v", err)
	}
	return a
}

// stored opens the generation the account document currently references.
func (g *grokRefreshRun) stored() subscription.GrokBundle {
	g.t.Helper()
	a := g.account()
	raw, err := g.f.secrets.Get(context.Background(), credentials.Ref{Owner: g.f.input.AccountID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		g.t.Fatal(err)
	}
	bundle, err := subscription.OpenGrokBundle(raw, g.f.service.Identity.ServerID, g.f.input.AccountID, a.Subscription.Generation)
	if err != nil {
		g.t.Fatal(err)
	}
	return bundle
}

// restart simulates a server restart: a new epoch, the startup recovery sweep and
// a short scanner run, which must never start a refresh again.
func (g *grokRefreshRun) restart() {
	g.t.Helper()
	g.f.service.subscriptionServerEpoch()
	g.f.service.subscriptionEpoch = domain.NewID()
	if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
		g.t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 700*time.Millisecond)
	defer stop()
	g.f.service.runServerSubscriptions(ctx)
}

// noLeak asserts that no credential reached a log line, a progress message or
// the persisted account document, and that the records carry the REFRESH phase.
func (g *grokRefreshRun) noLeak(old subscription.GrokBundle) {
	g.t.Helper()
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	_, a := g.f.record()
	document, _ := json.Marshal(a)
	view, _ := json.Marshal(g.f.progressFor(g.op))
	secrets := []string{"fake-access-token", "fake-refresh-token", "refresh_token", "client_id", old.RefreshToken()}
	for _, secret := range secrets {
		for name, text := range map[string]string{"logs": logs, "document": string(document), "progress": string(view)} {
			if strings.Contains(text, secret) {
				g.t.Fatalf("%s contain a credential value", name)
			}
		}
	}
	if !g.idp.Redacted(logs) || !g.idp.Redacted(string(document)) {
		g.t.Fatal("a credential reached a log or document")
	}
	if !strings.Contains(logs, `"phase":"REFRESH"`) || !strings.Contains(logs, g.op) {
		g.t.Fatal("refresh records lack the REFRESH phase or the operation correlation id")
	}
}

func TestGrokRefreshReplacesTheBundleWithoutRotation(t *testing.T) {
	g := newGrokRefresh(t)
	old := g.stored()
	before := g.account()
	if p := g.f.progressFor(g.op); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING {
		t.Fatalf("a queued refresh reports %v", p.State)
	}
	if p := g.settle(); p.State != grokSucceeds || p.Generation == "" {
		t.Fatalf("progress = %v", p)
	}
	after := g.account()
	if after.Subscription.Generation == before.Subscription.Generation || after.Subscription.IdentityCommitment != before.Subscription.IdentityCommitment || after.Health != domain.AccountReady ||
		after.Subscription.Pending != nil || after.Subscription.RecoveryRequired || after.Subscription.Lease != nil || after.Connection == nil {
		t.Fatal("the refresh did not publish a new generation for the same identity")
	}
	if o := after.Subscription.ServerOperation; o == nil || o.State != domain.SubscriptionSucceeded || !o.ExchangeClaimed || o.NativeStarted || o.Generation != after.Subscription.Generation {
		t.Fatal("the operation does not record a settled, claimed refresh")
	}
	next := g.stored()
	if err := subscription.GrokRefreshed(old, next); err != nil || next.RefreshToken() != old.RefreshToken() {
		t.Fatal("an unrotated refresh must keep the refresh token and change the credential")
	}
	if g.idp.RefreshRequests != 1 || g.idp.TokenRequests != 1 {
		t.Fatalf("token requests = %d", g.idp.TokenRequests)
	}
	form := g.idp.RefreshForms[0]
	if len(form) != 3 || form.Get("grant_type") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || form.Get("refresh_token") != old.RefreshToken() || g.idp.TokenAuthHeaders[0] != "" {
		t.Fatal("the request is not the public-client refresh_token grant")
	}
	// The superseded generation, and its credential, left the vault.
	if refs := g.f.references(); len(refs) != 1 || !refs[after.Subscription.Generation] {
		t.Fatalf("vault references = %v", refs)
	}
	g.noLeak(old)
}

func TestGrokRefreshAdoptsARotatedRefreshToken(t *testing.T) {
	g := newGrokRefresh(t)
	old := g.stored()
	g.idp.RefreshRotate = true
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p)
	}
	next := g.stored()
	if next.RefreshToken() == old.RefreshToken() || next.RefreshToken() != g.idp.RefreshToken {
		t.Fatal("the rotated refresh token was not stored")
	}
	if g.idp.RefreshRequests != 1 {
		t.Fatal("a rotation needs exactly one request")
	}
	g.noLeak(old)
}

func TestGrokRefreshWithoutAnIDTokenStillVerifiesTheIdentity(t *testing.T) {
	g := newGrokRefresh(t)
	g.idp.RefreshOmitID = true
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p)
	}
}

// grokRefreshProviderFailures are the answers after which the stored bundle must
// be untouched, with the expected settled state.
func TestGrokRefreshFailuresNeverReplaceTheBundleOrResend(t *testing.T) {
	cases := []struct {
		name     string
		edit     func(*grokidptest.IdP)
		state    pb.SubscriptionLoginState
		recovery bool
	}{
		{"invalid_grant", func(i *grokidptest.IdP) { i.RefreshStatus, i.RefreshError = http.StatusBadRequest, "invalid_grant" }, grokFailedSt, false},
		{"client error", func(i *grokidptest.IdP) { i.RefreshStatus, i.RefreshError = http.StatusUnauthorized, "invalid_client" }, grokFailedSt, false},
		{"server error", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusInternalServerError }, grokRecovery, true},
		{"bad gateway", func(i *grokidptest.IdP) { i.RefreshStatus = http.StatusBadGateway }, grokRecovery, true},
		{"malformed body", func(i *grokidptest.IdP) { i.RefreshMalformed = true }, grokRecovery, true},
		{"truncated body", func(i *grokidptest.IdP) { i.RefreshTruncated = true }, grokRecovery, true},
		{"principal changed", func(i *grokidptest.IdP) { i.PrincipalID = "another-principal" }, grokRecovery, true},
		{"subject changed", func(i *grokidptest.IdP) { i.Subject, i.UserinfoSubject = "another-user", "another-user" }, grokRecovery, true},
		{"userinfo failure", func(i *grokidptest.IdP) { i.UserinfoStatus = http.StatusInternalServerError }, grokRecovery, true},
		{"foreign signature", func(i *grokidptest.IdP) { i.SignWithOtherKey = true }, grokRecovery, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrokRefresh(t)
			old := g.stored()
			before := g.account()
			tc.edit(g.idp)
			p := g.settle()
			if p.State != tc.state {
				t.Fatalf("progress = %v, want %v", p.State, tc.state)
			}
			if p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_REFRESH {
				t.Fatal("the settled operation lacks a REFRESH diagnostic")
			}
			after := g.account()
			if after.Subscription.Generation != before.Subscription.Generation || !g.stored().Equal(old) {
				t.Fatal("a failed refresh replaced the bundle")
			}
			if after.Subscription.RecoveryRequired != tc.recovery || tc.recovery && after.Health != domain.AccountFailed || !tc.recovery && (after.Health != domain.AccountReady || after.Subscription.Pending != nil) {
				t.Fatalf("recovery fence = %v", after.Subscription.RecoveryRequired)
			}
			if refs := g.f.references(); len(refs) != 1 || !refs[before.Subscription.Generation] {
				t.Fatalf("vault references = %v", refs)
			}
			if g.idp.RefreshRequests != 1 {
				t.Fatalf("token requests = %d", g.idp.RefreshRequests)
			}
			// A restarted scanner never sends the old token again.
			g.restart()
			if g.idp.RefreshRequests != 1 {
				t.Fatal("a restart resent the refresh token")
			}
			g.noLeak(old)
		})
	}
}

func TestGrokRefreshAfterARejectionCanBeRequestedAgain(t *testing.T) {
	g := newGrokRefresh(t)
	g.idp.RefreshStatus, g.idp.RefreshError = http.StatusBadRequest, "invalid_grant"
	if p := g.settle(); p.State != grokFailedSt {
		t.Fatalf("progress = %v", p.State)
	}
	// A definitive rejection consumed nothing: a new explicit request may try again.
	g.idp.RefreshStatus = 0
	g.op = g.request(nil)
	g.done = nil
	if p := g.settle(); p.State != grokSucceeds || g.idp.RefreshRequests != 2 {
		t.Fatalf("second refresh = %v after %d requests", p.State, g.idp.RefreshRequests)
	}
}

func TestGrokRefreshClaimIsDurableBeforeTheRequest(t *testing.T) {
	g := newGrokRefresh(t)
	entered, release := make(chan struct{}), make(chan struct{})
	g.idp.BeforeToken = func() { close(entered); <-release }
	g.run()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the token request was never sent")
	}
	a := g.account()
	o := a.Subscription.ServerOperation
	if !o.ExchangeClaimed || !o.NativeStarted || a.Subscription.Pending == nil || a.Subscription.Pending.Phase != domain.SubscriptionClaimed || a.Subscription.Pending.MachineID != "" || a.Subscription.Lease != nil {
		t.Fatal("the claim was not durable under the exclusive server lease before the request")
	}
	// The account is exclusively held: a second operation and a Worker lease are refused.
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokRefreshAction }); err == nil {
		t.Fatal("a second refresh was accepted while one is in flight")
	}
	if g.idp.TokenRequests != 1 {
		t.Fatal("exactly one request is expected while in flight")
	}
	close(release)
	g.await()
	if p := g.f.progressFor(g.op); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p.State)
	}
}

func TestGrokRefreshShutdownWhileTheRequestIsInFlightIsRecovery(t *testing.T) {
	g := newGrokRefresh(t)
	old := g.stored()
	entered, release := make(chan struct{}), make(chan struct{})
	g.idp.BeforeToken = func() { close(entered); <-release }
	g.run()
	<-entered
	g.cancel()
	g.await()
	close(release)
	if p := g.f.progressFor(g.op); p.State != grokRecovery {
		t.Fatalf("progress after shutdown = %v", p.State)
	}
	a := g.account()
	if !a.Subscription.RecoveryRequired || !g.stored().Equal(old) || g.idp.TokenRequests != 1 {
		t.Fatal("a shutdown after the claim must be recovery with one request and the old bundle")
	}
	g.restart()
	if g.idp.RefreshRequests != 1 {
		t.Fatal("a restart resent the refresh token")
	}
	g.noLeak(old)
}

func TestGrokRefreshCrashMatrix(t *testing.T) {
	cases := []struct {
		name     string
		stage    grokRefreshStage
		requests int
		recovery bool
	}{
		{"after the claim", grokRefreshAfterClaim, 0, true},
		{"after the response", grokRefreshAfterResponse, 1, true},
		{"after the persist", grokRefreshAfterPersist, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrokRefresh(t)
			old := g.stored()
			g.idp.RefreshRotate = true
			g.f.service.grokRefreshCrash = func(stage grokRefreshStage) bool { return stage == tc.stage }
			g.run()
			g.await()
			if g.idp.RefreshRequests != tc.requests {
				t.Fatalf("token requests = %d, want %d", g.idp.RefreshRequests, tc.requests)
			}
			// The dead process settled nothing; the claim is on disk.
			if o := g.account().Subscription.ServerOperation; !o.ExchangeClaimed {
				t.Fatal("the claim was not durable at the crash")
			}
			g.f.service.grokRefreshCrash = nil
			g.restart()
			after := g.account()
			if after.Subscription.RecoveryRequired != tc.recovery {
				t.Fatalf("recovery fence = %v, want %v", after.Subscription.RecoveryRequired, tc.recovery)
			}
			if g.idp.RefreshRequests != tc.requests {
				t.Fatal("recovery resent the refresh token")
			}
			if tc.recovery {
				if p := g.f.progressFor(g.op); p.State != grokRecovery || !g.stored().Equal(old) {
					t.Fatal("an uncertain refresh must stay in recovery with the old bundle")
				}
				return
			}
			// A crash after the publication is not uncertain: the new generation is
			// current and the account is usable.
			if p := g.f.progressFor(g.op); p.State != grokSucceeds || after.Subscription.Pending != nil || after.Health != domain.AccountReady {
				t.Fatalf("progress = %v", p.State)
			}
			next := g.stored()
			if next.RefreshToken() != g.idp.RefreshToken || next.Equal(old) {
				t.Fatal("the published generation does not hold the rotated credential")
			}
			// The superseded generation survived the crash; the next completed
			// operation removes it.
			if refs := g.f.references(); len(refs) != 2 {
				t.Fatalf("vault references after the crash = %v", refs)
			}
			g.op = g.request(nil)
			g.done = nil
			if p := g.settle(); p.State != grokSucceeds || g.idp.RefreshRequests != 2 {
				t.Fatalf("follow-up refresh = %v", p.State)
			}
			if refs := g.f.references(); len(refs) != 1 {
				t.Fatalf("vault references after the next refresh = %v", refs)
			}
		})
	}
}

func TestGrokRefreshRunsThroughTheServerScanner(t *testing.T) {
	g := newGrokRefresh(t)
	ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 20*time.Second)
	defer stop()
	finished := make(chan struct{})
	go func() { defer close(finished); g.f.service.runServerSubscriptions(ctx) }()
	deadline := time.Now().Add(15 * time.Second)
	for g.f.progressFor(g.op).State != grokSucceeds {
		if time.Now().After(deadline) {
			t.Fatal("the scanner never ran the refresh")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	<-finished
	if g.idp.RefreshRequests != 1 {
		t.Fatalf("token requests = %d", g.idp.RefreshRequests)
	}
}

func TestGrokRefreshRequestValidation(t *testing.T) {
	g := newGrokRefresh(t)
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) {
		r.Action = grokRefreshAction
		r.MachineId = string(g.f.input.MachineID)
	}); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
		t.Fatalf("a refresh that names a Runner Device was not refused as unsupported: %v", err)
	}
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) {
		r.Action = grokRefreshAction
		r.DeviceCode = true
	}); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.InvalidArgument {
		t.Fatalf("a refresh with device_code was not refused: %v", err)
	}
	a := g.account()
	if a.Subscription.Pending == nil || a.Subscription.Pending.ID != domain.ID(g.op) || a.Subscription.Pending.MachineID != "" || a.Subscription.Pending.Action != domain.SubscriptionRefresh {
		t.Fatal("the accepted refresh is not the server-owned operation")
	}
	// A refresh is never cancelable: it finishes under exclusive ownership.
	r, _ := g.f.record()
	if _, err := g.f.client.CancelSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err == nil {
		t.Fatal("a refresh was canceled")
	}
	if g.account().Subscription.Pending == nil {
		t.Fatal("the refused cancellation changed the operation")
	}
}

func TestGrokRefreshIsNotStartedWhileALeaseOrFenceHoldsTheAccount(t *testing.T) {
	g := newGrokRefresh(t)
	candidate := g.account()
	if !grokRefreshCandidate(candidate) {
		t.Fatal("a queued refresh of a connected account must be claimable")
	}
	for name, edit := range map[string]func(*domain.Account){
		"worker lease":  func(a *domain.Account) { a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID()} },
		"recovery":      func(a *domain.Account) { a.Subscription.RecoveryRequired = true },
		"disconnected":  func(a *domain.Account) { a.Connection = nil },
		"device bound":  func(a *domain.Account) { a.Subscription.Pending.MachineID = domain.NewID() },
		"claimed":       func(a *domain.Account) { a.Subscription.ServerOperation.NativeStarted = true },
		"login":         func(a *domain.Account) { a.Subscription.ServerOperation.Action = domain.SubscriptionLogin },
		"not preparing": func(a *domain.Account) { a.Subscription.ServerOperation.State = domain.SubscriptionWaiting },
	} {
		a := g.account()
		edit(&a)
		if grokRefreshCandidate(a) {
			t.Fatalf("%s: a refresh that must wait was claimable", name)
		}
	}
}

// A lease that has no pending operation (a Worker execution lease) owns the
// credential, so the refresh is refused at admission instead of being queued
// behind it.
func TestGrokRefreshRequestIsRefusedWhileALeaseHoldsTheAccount(t *testing.T) {
	g := newGrokRefresh(t)
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p.State)
	}
	g.f.mutateAccount(t, func(a *domain.Account) {
		a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), Action: domain.SubscriptionExecute}
	})
	_, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokRefreshAction })
	if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatalf("a refresh was accepted while a lease held the account: %v", err)
	}
	if _, after := g.f.record(); after.Subscription.Pending != nil || after.Subscription.ServerOperation.State == domain.SubscriptionPreparing {
		t.Fatal("the refused refresh left a pending operation behind")
	}
}

// A generation the account references but the vault no longer holds is a local
// credential loss: the refresh fences the account instead of settling as an
// ordinary failure that leaves the unusable generation ready.
func TestGrokRefreshWithAMissingBundleFencesTheAccount(t *testing.T) {
	g := newGrokRefresh(t)
	before := g.account()
	if err := g.f.secrets.Delete(context.Background(), credentials.Ref{Owner: g.f.input.AccountID, ID: before.Subscription.Generation, Purpose: credentials.AccountLogin}); err != nil {
		t.Fatal(err)
	}
	p := g.settle()
	if p.State != grokRecovery || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
		t.Fatalf("progress = %v", p)
	}
	after := g.account()
	if !after.Subscription.RecoveryRequired || after.Health != domain.AccountFailed || after.Subscription.Generation != before.Subscription.Generation || g.idp.RefreshRequests != 0 {
		t.Fatalf("recovery = %v, health = %v, requests = %d", after.Subscription.RecoveryRequired, after.Health, g.idp.RefreshRequests)
	}
	g.restart()
	if g.idp.RefreshRequests != 0 {
		t.Fatal("a restart sent a refresh for a fenced account")
	}
}

func TestGrokRefreshExpiredBeforeTheClaimSendsNothing(t *testing.T) {
	g := newGrokRefresh(t)
	_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.expire", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, g.f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerOperation.StartedAt = time.Now().Add(-time.Hour)
		a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(-time.Minute)
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := g.settle(); p.State != grokExpired {
		t.Fatalf("progress = %v", p.State)
	}
	if a := g.account(); a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || g.idp.TokenRequests != 0 {
		t.Fatal("an expired refresh must settle cleanly without a request")
	}
}

func TestGrokRefreshUnreachableProviderBeforeTheClaimFailsWithoutFencing(t *testing.T) {
	g := newGrokRefresh(t)
	g.f.service.grokLoginTransport = failingTransport{}
	old := g.stored()
	if p := g.settle(); p.State != grokFailedSt {
		t.Fatalf("progress = %v", p.State)
	}
	if a := g.account(); a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || !g.stored().Equal(old) {
		t.Fatal("nothing was sent, so the account must stay usable")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}
