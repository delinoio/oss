// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const grokLogoutAction = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT

// grokLogoutRun drives one server-owned Grok logout over the real Connect
// handlers against the fake authorization server.
type grokLogoutRun struct {
	t          *testing.T
	f          *grokFixture
	idp        *grokidptest.IdP
	logs       *grokLogCapture
	generation domain.ID
	op         string
	revokes    atomic.Int32
}

// newGrokLogout connects a Grok account whose bundle matches the fake server. No
// logout is requested yet.
func newGrokLogout(t *testing.T) *grokLogoutRun {
	t.Helper()
	f := newGrokFixture(t)
	idp := grokidptest.New()
	g := &grokLogoutRun{t: t, f: f, idp: idp}
	f.service.grokLoginTransport = countingRevokes{base: idp.Transport(), count: &g.revokes}
	g.generation = connectGrokAccount(t, f, f.input.AccountID, idp)
	g.logs = f.captureLogs()
	return g
}

// countingRevokes counts the revocation requests that reach the fake server.
type countingRevokes struct {
	base  http.RoundTripper
	count *atomic.Int32
}

func (c countingRevokes) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/oauth2/revoke" {
		c.count.Add(1)
	}
	return c.base.RoundTrip(r)
}

// connectGrokAccount stores the fake server's credential as the account's
// current generation and marks the account connected.
func connectGrokAccount(t *testing.T, f *grokFixture, account domain.ID, idp *grokidptest.IdP) domain.ID {
	t.Helper()
	bundle := boundGrokBundle(t, idp)
	generation := domain.NewID()
	sealed, err := subscription.SealGrokBundle(f.service.Identity.ServerID, account, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.secrets.Put(context.Background(), credentials.Ref{Owner: account, ID: generation, Purpose: credentials.AccountLogin}, sealed); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.connect", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		a.Subscription.Generation = generation
		a.Subscription.IdentityCommitment = f.service.grokIdentityCommitment(bundle)
		a.Connection = &domain.AccountConnection{ID: generation, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
		a.Health = domain.AccountReady
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func (g *grokLogoutRun) request() string {
	g.t.Helper()
	response, err := g.f.grokLoginRequest(g.t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokLogoutAction })
	if err != nil {
		g.t.Fatal(err)
	}
	g.op = response.OperationId
	return g.op
}

func (g *grokLogoutRun) ownerContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

// run executes the runner once, synchronously.
func (g *grokLogoutRun) run() {
	g.t.Helper()
	g.f.service.runGrokLogout(g.ownerContext(), g.f.input.AccountID)
}

// settle requests the logout, runs it to its end and returns its progress.
func (g *grokLogoutRun) settle() *pb.GetSubscriptionProgressResponse {
	g.t.Helper()
	g.request()
	g.run()
	return g.f.progressFor(g.op)
}

func (g *grokLogoutRun) account() domain.Account {
	g.t.Helper()
	_, a := g.f.record()
	if a.Subscription != nil {
		if err := a.Subscription.Validate(a); err != nil {
			g.t.Fatalf("persisted account violates the subscription invariants: %v", err)
		}
	}
	return a
}

// scan runs the server scanner for a short while.
func (g *grokLogoutRun) scan(d time.Duration) {
	g.t.Helper()
	ctx, stop := context.WithTimeout(g.ownerContext(), d)
	defer stop()
	g.f.service.runServerSubscriptions(ctx)
}

// restart simulates a server restart followed by a short scanner run.
func (g *grokLogoutRun) restart() {
	g.t.Helper()
	g.f.service.subscriptionServerEpoch()
	g.f.service.subscriptionEpoch = domain.NewID()
	if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
		g.t.Fatal(err)
	}
	g.scan(700 * time.Millisecond)
}

// executionLease places a fixture execution lease on the account. Grok execution
// does not exist yet, so the lease stands for the original execution.
func (g *grokLogoutRun) executionLease() domain.ID {
	g.t.Helper()
	lease := domain.NewID()
	g.f.mutateAccount(g.t, func(a *domain.Account) {
		a.Subscription.Lease = &domain.SubscriptionLease{ID: lease, OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: g.f.input.MachineID, InstanceID: g.f.instance, DeviceID: g.f.device, Epoch: g.f.service.subscriptionServerEpoch(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
	})
	return lease
}

func (g *grokLogoutRun) releaseLease() {
	g.t.Helper()
	g.f.mutateAccount(g.t, func(a *domain.Account) { a.Subscription.Lease = nil })
}

func (g *grokLogoutRun) logText() string {
	g.logs.mu.Lock()
	defer g.logs.mu.Unlock()
	return g.logs.buf.String()
}

// noLeak asserts that no credential, path or identity reached a log line, a
// progress message or the persisted account document.
func (g *grokLogoutRun) noLeak(old subscription.GrokBundle) {
	g.t.Helper()
	logs := g.logText()
	_, a := g.f.record()
	document, _ := json.Marshal(a)
	view, _ := json.Marshal(g.f.progressFor(g.op))
	secrets := []string{"fake-access-token", "fake-refresh-token", "refresh_token", "client_id", old.RefreshToken(), g.idp.Email, "/oauth2/revoke", "auth.json"}
	for _, secret := range secrets {
		for name, text := range map[string]string{"logs": logs, "document": string(document), "progress": string(view)} {
			if strings.Contains(text, secret) {
				g.t.Fatalf("%s contain a protected value", name)
			}
		}
	}
	if !g.idp.Redacted(logs) || !g.idp.Redacted(string(document)) {
		g.t.Fatal("a credential reached a log or document")
	}
}

func (g *grokLogoutRun) stored() subscription.GrokBundle {
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

func TestGrokLogoutRevokesTheTokenAndRemovesTheProtectedBundle(t *testing.T) {
	g := newGrokLogout(t)
	old := g.stored()
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p)
	}
	a := g.account()
	st := a.Subscription
	if a.Connection != nil || a.Health != domain.AccountDisconnected || st.Generation != "" || st.IdentityCommitment != "" || st.Pending != nil || st.RecoveryRequired || st.Lease != nil {
		t.Fatal("logout did not disconnect the account")
	}
	if o := st.ServerOperation; o == nil || o.State != domain.SubscriptionSucceeded || !o.ExchangeClaimed || o.NativeStarted || o.Action != domain.SubscriptionLogout {
		t.Fatal("the operation does not record a settled, claimed logout")
	}
	if refs := g.f.references(); len(refs) != 0 {
		t.Fatalf("the protected bundle survived the logout: %v", refs)
	}
	if g.idp.RevokeRequests != 1 || g.idp.TokenRequests != 0 {
		t.Fatalf("revocation requests = %d, token requests = %d", g.idp.RevokeRequests, g.idp.TokenRequests)
	}
	form := g.idp.RevokeForms[0]
	if len(form) != 3 || form.Get("token") != old.RefreshToken() || form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || g.idp.RevokeAuthHeaders[0] != "" {
		t.Fatal("the request is not the public-client revocation form")
	}
	if !g.idp.RefreshRevoked() {
		t.Fatal("the refresh token was not revoked")
	}
	logs := g.logText()
	if !strings.Contains(logs, `"phase":"LOGOUT"`) || !strings.Contains(logs, `"phase":"CLEANUP"`) || !strings.Contains(logs, g.op) {
		t.Fatal("logout records lack the LOGOUT and CLEANUP phases or the operation correlation id")
	}
	g.noLeak(old)
}

func TestGrokLogoutAcceptanceMarksTheAccountAndRefusesNewWork(t *testing.T) {
	g := newGrokLogout(t)
	g.request()
	a := g.account()
	if a.Health != domain.AccountRevoked || a.Subscription.Pending == nil || a.Subscription.Pending.Action != domain.SubscriptionLogout || !grokAccountLoggingOut(a) {
		t.Fatal("acceptance did not mark the account logging out")
	}
	if p := g.f.progressFor(g.op); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING {
		t.Fatalf("a queued logout reports %v", p.State)
	}
	// New Worker work is refused with a typed unavailable problem, never queued.
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN} {
		other := &pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}
		_, err := g.f.take(other, action)
		if err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unavailable {
			t.Fatalf("a Worker %v Take during logout = %v", action, err)
		}
	}
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokRefreshAction }); err == nil {
		t.Fatal("a refresh was accepted behind a queued logout")
	}
	if !strings.Contains(g.logText(), "grok_subscription_take_refused_logging_out") {
		t.Fatal("the refusal was not logged")
	}
	if a := g.account(); a.Subscription.Lease != nil || len(g.f.references()) != 1 {
		t.Fatal("a refused Take changed the account")
	}
}

func TestGrokLogoutWaitsForTheOriginalExecutionExit(t *testing.T) {
	g := newGrokLogout(t)
	old := g.stored()
	g.executionLease()
	g.request()
	g.scan(700 * time.Millisecond)
	if g.idp.RevokeRequests != 0 || len(g.f.references()) != 1 {
		t.Fatal("the logout acted while the original execution still held the account")
	}
	if p := g.f.progressFor(g.op); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING {
		t.Fatalf("a waiting logout reports %v", p.State)
	}
	// The execution exit releases its lease with confirmed cleanup.
	g.releaseLease()
	g.scan(2 * time.Second)
	if p := g.f.progressFor(g.op); p.State != grokSucceeds {
		t.Fatalf("progress after the exit = %v", p.State)
	}
	if g.idp.RevokeRequests != 1 || len(g.f.references()) != 0 {
		t.Fatal("the logout did not revoke and remove the bundle after the exit")
	}
	g.noLeak(old)
}

// An execution whose private cleanup is unconfirmed keeps the account leased, so
// the logout stays queued until the original owner confirms its cleanup.
func TestGrokLogoutStaysQueuedWhileAnExecutionCleanupIsUnconfirmed(t *testing.T) {
	g := newGrokLogout(t)
	g.executionLease()
	g.f.mutateAccount(t, func(a *domain.Account) {
		a.Subscription.Lease.CleanupUnconfirmed = true
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
	})
	_, a := g.f.record()
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokLogoutAction }); err == nil {
		t.Fatal("a logout was accepted while a cleanup recovery fence was set")
	}
	if a.Subscription.Lease == nil || g.idp.RevokeRequests != 0 {
		t.Fatal("unexpected state")
	}
}

func TestGrokLogoutRevocationOutcomes(t *testing.T) {
	cases := []struct {
		name       string
		edit       func(*grokidptest.IdP)
		wantState  pb.SubscriptionLoginState
		wantGone   bool
		wantFenced bool
	}{
		{"success", func(*grokidptest.IdP) {}, grokSucceeds, true, false},
		{"already invalid token", func(i *grokidptest.IdP) { i.RevokeStatus, i.RevokeError = http.StatusBadRequest, "invalid_token" }, grokSucceeds, true, false},
		{"already invalid grant", func(i *grokidptest.IdP) { i.RevokeStatus, i.RevokeError = http.StatusBadRequest, "invalid_grant" }, grokSucceeds, true, false},
		{"client error", func(i *grokidptest.IdP) { i.RevokeStatus, i.RevokeError = http.StatusBadRequest, "invalid_request" }, grokRecovery, false, true},
		{"unauthorized", func(i *grokidptest.IdP) { i.RevokeStatus, i.RevokeError = http.StatusUnauthorized, "invalid_client" }, grokRecovery, false, true},
		{"server error", func(i *grokidptest.IdP) { i.RevokeStatus = http.StatusInternalServerError }, grokRecovery, false, true},
		{"service unavailable", func(i *grokidptest.IdP) { i.RevokeStatus = http.StatusServiceUnavailable }, grokRecovery, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrokLogout(t)
			old := g.stored()
			tc.edit(g.idp)
			p := g.settle()
			if p.State != tc.wantState {
				t.Fatalf("progress = %v, want %v", p.State, tc.wantState)
			}
			a := g.account()
			if g.idp.RevokeRequests != 1 {
				t.Fatalf("revocation requests = %d", g.idp.RevokeRequests)
			}
			if tc.wantGone {
				if a.Connection != nil || a.Subscription.Generation != "" || len(g.f.references()) != 0 || a.Health != domain.AccountDisconnected {
					t.Fatal("a confirmed revocation did not remove the credential")
				}
			} else {
				// No definite success means no bundle deletion without recovery state.
				if !a.Subscription.RecoveryRequired || a.Health != domain.AccountFailed || a.Connection == nil || !g.stored().Equal(old) || len(g.f.references()) != 1 {
					t.Fatal("an unconfirmed revocation deleted the bundle or left no recovery state")
				}
				if p.GrokDiagnostic == nil || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGOUT {
					t.Fatal("the settled operation lacks a LOGOUT diagnostic")
				}
				// A restart never sends the revocation again by itself.
				g.restart()
				if g.idp.RevokeRequests != 1 {
					t.Fatal("a restart resent the revocation")
				}
			}
			g.noLeak(old)
		})
	}
}

// failingRevokeTransport answers every revocation request with a transport
// error, as a timed out request does.
type failingRevokeTransport struct {
	base  http.RoundTripper
	count atomic.Int32
}

func (f *failingRevokeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/oauth2/revoke" {
		f.count.Add(1)
		return nil, context.DeadlineExceeded
	}
	return f.base.RoundTrip(r)
}

func TestGrokLogoutTimeoutLeavesRecoveryAndKeepsTheBundle(t *testing.T) {
	g := newGrokLogout(t)
	old := g.stored()
	transport := &failingRevokeTransport{base: g.idp.Transport()}
	g.f.service.grokLoginTransport = transport
	if p := g.settle(); p.State != grokRecovery {
		t.Fatalf("progress = %v", p.State)
	}
	a := g.account()
	if transport.count.Load() != 1 || !a.Subscription.RecoveryRequired || !g.stored().Equal(old) || a.Connection == nil {
		t.Fatal("a timed out revocation must fence the account and keep the bundle")
	}
	g.noLeak(old)
}

func TestGrokLogoutBeforeTheRequestKeepsTheCredentialAndTheAccountUsable(t *testing.T) {
	t.Run("no revocation endpoint", func(t *testing.T) {
		g := newGrokLogout(t)
		old := g.stored()
		g.idp.RevokeOmitEndpoint = true
		if p := g.settle(); p.State != grokFailedSt {
			t.Fatalf("progress = %v", p.State)
		}
		a := g.account()
		if g.idp.RevokeRequests != 0 || a.Subscription.RecoveryRequired || a.Health != domain.AccountReady || a.Connection == nil || !g.stored().Equal(old) || a.Subscription.ServerOperation.ExchangeClaimed {
			t.Fatal("a logout that sent nothing must leave the account usable")
		}
		// The account can try again once discovery works.
		g.idp.RevokeOmitEndpoint = false
		if p := g.settle(); p.State != grokSucceeds {
			t.Fatalf("second logout = %v", p.State)
		}
	})
	t.Run("unusable stored bundle", func(t *testing.T) {
		g := newGrokLogout(t)
		g.f.secrets.mu.Lock()
		for ref := range g.f.secrets.values {
			g.f.secrets.values[ref] = []byte("not a sealed bundle")
		}
		g.f.secrets.mu.Unlock()
		if p := g.settle(); p.State != grokRecovery {
			t.Fatalf("progress = %v", p.State)
		}
		if g.idp.RevokeRequests != 0 || !g.account().Subscription.RecoveryRequired {
			t.Fatal("an unusable bundle must fence the account without a request")
		}
	})
}

func TestGrokLogoutFaultInjectionNeverLosesTheCredentialWithoutRecovery(t *testing.T) {
	cases := []struct {
		name         string
		stage        grokLogoutStage
		requests     int
		wantBundle   bool
		wantRecovery bool
	}{
		{"after the claim", grokLogoutAfterClaim, 0, true, true},
		{"after the revocation", grokLogoutAfterRevoke, 1, true, true},
		{"after the publication", grokLogoutAfterPublish, 1, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrokLogout(t)
			g.f.service.grokLogoutCrash = func(stage grokLogoutStage) bool { return stage == tc.stage }
			g.request()
			g.run()
			g.f.service.grokLogoutCrash = nil
			if g.idp.RevokeRequests != tc.requests {
				t.Fatalf("revocation requests = %d, want %d", g.idp.RevokeRequests, tc.requests)
			}
			if o := g.account().Subscription.ServerOperation; !o.ExchangeClaimed && tc.stage != grokLogoutAfterPublish {
				t.Fatal("the revocation claim was not durable at the crash")
			}
			g.restart()
			a := g.account()
			if g.idp.RevokeRequests != tc.requests {
				t.Fatal("a restart resent the revocation")
			}
			if tc.stage == grokLogoutAfterPublish {
				// Published but not cleaned: the credential stays vault-owned and the
				// account is disconnected with no recovery fence from the restart.
				if a.Connection != nil || len(g.f.references()) != 1 {
					t.Fatal("the publication crash must leave the unreferenced bundle for cleanup")
				}
				return
			}
			if !a.Subscription.RecoveryRequired || a.Connection == nil || len(g.f.references()) != 1 {
				t.Fatal("an interrupted logout must keep the bundle under a recovery fence")
			}
		})
	}
}

func TestGrokLogoutVaultCleanupFailureLeavesTheRetainedCleanupShape(t *testing.T) {
	g := newGrokLogout(t)
	g.f.secrets.mu.Lock()
	g.f.secrets.deleteError = errors.New("vault unavailable")
	g.f.secrets.mu.Unlock()
	if p := g.settle(); p.State != grokRecovery {
		t.Fatalf("progress = %v", p.State)
	}
	a := g.account()
	if a.Connection != nil || a.Subscription.Generation != "" || !a.Subscription.RecoveryRequired || a.Health != domain.AccountFailed || len(g.f.references()) != 1 {
		t.Fatal("an unconfirmed vault removal must leave the disconnected account fenced with the reference retained")
	}
	if !serverLogoutRecoveryCandidate(a) {
		t.Fatal("the retained shape is not the logout-cleanup recovery owner deletion consumes")
	}
	if !strings.Contains(g.logText(), "grok_logout_cleanup_pending") {
		t.Fatal("the pending cleanup was not logged")
	}
}

func TestGrokLogoutRetryByTheOriginalActorOnly(t *testing.T) {
	g := newGrokLogout(t)
	g.idp.RevokeStatus = http.StatusInternalServerError
	if p := g.settle(); p.State != grokRecovery {
		t.Fatalf("progress = %v", p.State)
	}
	// A different actor cannot take over the unfinished logout.
	other := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	request := connect.NewRequest(&pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(g.f.input.AccountID), ExpectedRevision: func() uint64 { r, _ := g.f.record(); return r.Revision }()}, Action: grokLogoutAction})
	if _, err := g.f.service.requestGrokSubscription(other, request); err == nil {
		t.Fatal("another actor retried the logout")
	}
	if g.idp.RevokeRequests != 1 {
		t.Fatal("a refused retry sent a request")
	}
	// The original actor retries after the provider recovered.
	g.idp.RevokeStatus = 0
	if p := g.settle(); p.State != grokSucceeds {
		t.Fatalf("retry = %v", p.State)
	}
	if g.idp.RevokeRequests != 2 || len(g.f.references()) != 0 || g.account().Connection != nil {
		t.Fatal("the retry did not complete the logout")
	}
}

// attributeToClient makes a client device the initiator of the pending logout.
func (g *grokLogoutRun) attributeToClient(device domain.ID) {
	g.t.Helper()
	g.f.mutateAccount(g.t, func(a *domain.Account) {
		actor := domain.Principal{Type: domain.ClientDevice, DeviceID: device}
		a.Subscription.Pending.Actor, a.Subscription.ServerOperation.Actor = actor, actor
	})
}

// revokeClient runs the queued-initiator cleanup that revoking a client device
// performs in the same transaction.
func (g *grokLogoutRun) revokeClient(device domain.ID) {
	g.t.Helper()
	if _, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.revoke", nil, func(tx *store.Tx) (any, error) {
		return nil, cancelQueuedSubscriptionInitiator(tx, device, g.f.service.subscriptionServerEpoch())
	}); err != nil {
		g.t.Fatal(err)
	}
}

// A logout owes the provider a token revocation that no initiator is needed to
// perform. Revoking the client that queued it behind a Worker lease must not
// cancel it, or the account would stay connected and health-revoked forever.
func TestGrokLogoutQueuedBehindALeaseSurvivesRevocationOfItsClient(t *testing.T) {
	g := newGrokLogout(t)
	g.executionLease()
	g.request()
	device := domain.NewID()
	g.attributeToClient(device)
	g.revokeClient(device)
	a := g.account()
	if a.Subscription.Pending == nil || !grokAccountLoggingOut(a) || a.Subscription.ServerOperation.State != domain.SubscriptionPreparing {
		t.Fatal("revoking the initiating client canceled the queued logout")
	}
	g.releaseLease()
	g.scan(2 * time.Second)
	if a := g.account(); a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatalf("operation after the exit = %v", a.Subscription.ServerOperation.State)
	}
	if a := g.account(); g.idp.RevokeRequests != 1 || len(g.f.references()) != 0 || a.Connection != nil || a.Subscription.Pending != nil {
		t.Fatal("the queued logout did not revoke the token and remove the bundle after the client was revoked")
	}
}

// Revoking the initiator still cancels the other queued server operations.
func TestGrokClientRevocationStillCancelsQueuedRefresh(t *testing.T) {
	g := newGrokLogout(t)
	response, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH })
	if err != nil {
		t.Fatal(err)
	}
	device := domain.NewID()
	g.f.mutateAccount(t, func(a *domain.Account) {
		actor := domain.Principal{Type: domain.ClientDevice, DeviceID: device}
		a.Subscription.Pending.Actor, a.Subscription.ServerOperation.Actor = actor, actor
	})
	g.revokeClient(device)
	if a := g.account(); a.Subscription.Pending != nil || a.Subscription.ServerOperation.ID != domain.ID(response.OperationId) || a.Subscription.ServerOperation.State != domain.SubscriptionCanceled {
		t.Fatal("a queued refresh survived the revocation of its client")
	}
}

// A logout that stayed queued after its client was revoked and then ended
// uncertain can be retried by the owner, because its original actor can no
// longer act. Another client still cannot take it over.
func TestGrokLogoutOfARevokedClientIsRetriedByTheOwner(t *testing.T) {
	g := newGrokLogout(t)
	g.idp.RevokeStatus = http.StatusInternalServerError
	g.request()
	device := domain.NewID()
	g.attributeToClient(device)
	g.revokeClient(device)
	g.run()
	if a := g.account(); a.Subscription.ServerOperation.State != domain.SubscriptionRecovery || !a.Subscription.RecoveryRequired {
		t.Fatalf("operation = %v", a.Subscription.ServerOperation.State)
	}
	retry := func(ctx context.Context) error {
		r, _ := g.f.record()
		_, err := g.f.service.requestGrokSubscription(ctx, connect.NewRequest(&pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(g.f.input.AccountID), ExpectedRevision: r.Revision}, Action: grokLogoutAction}))
		return err
	}
	if err := retry(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})); err == nil {
		t.Fatal("another client took over the logout of a revoked client")
	}
	g.idp.RevokeStatus = 0
	if err := retry(g.ownerContext()); err != nil {
		t.Fatalf("the owner could not retry the logout of a revoked client: %v", err)
	}
	g.run()
	if a := g.account(); g.idp.RevokeRequests != 2 || len(g.f.references()) != 0 || a.Connection != nil || a.Subscription.RecoveryRequired {
		t.Fatal("the owner's retry did not complete the logout")
	}
}

func TestGrokLogoutSerializesWithLoginAndRefreshOnOneAccount(t *testing.T) {
	g := newGrokLogout(t)
	gate := make(chan struct{})
	reached := make(chan struct{})
	var once sync.Once
	g.idp.BeforeRevoke = func() {
		once.Do(func() { close(reached) })
		<-gate
	}
	g.request()
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.run()
	}()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the logout did not reach the provider")
	}
	// While the logout holds the account, nothing else may start.
	if _, err := g.f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokRefreshAction }); err == nil {
		t.Error("a refresh was accepted during the logout")
	}
	if _, err := g.f.grokLoginRequest(t, nil); err == nil {
		t.Error("a login was accepted during the logout")
	}
	if _, err := g.f.take(&pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
		t.Error("a Worker lease was granted during the logout")
	}
	g.f.service.runGrokLogout(g.ownerContext(), g.f.input.AccountID)
	if g.revokes.Load() != 1 {
		t.Errorf("a second runner sent a revocation: %d", g.revokes.Load())
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the logout did not finish")
	}
	if p := g.f.progressFor(g.op); p.State != grokSucceeds {
		t.Fatalf("progress = %v", p.State)
	}
}

func TestGrokLogoutOfSeparateAccountsRunsInParallel(t *testing.T) {
	g := newGrokLogout(t)
	second := domain.NewID()
	_, base := g.f.record()
	_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.second", nil, func(tx *store.Tx) (any, error) {
		clone := base
		clone.Alias = "second grok"
		clone.Connection, clone.Subscription, clone.Health = nil, nil, domain.AccountDisconnected
		return tx.Put(domain.AccountKind, second, 0, "", "", clone)
	})
	if err != nil {
		t.Fatal(err)
	}
	idp := g.idp
	// The second account holds the same fake credential family under its own
	// identity; both revocations are served by the same fake server, whose
	// accepted token is revoked by the first request.
	connectGrokAccount(t, g.f, second, idp)
	var reached atomic.Int32
	both := make(chan struct{})
	var once sync.Once
	idp.BeforeRevoke = func() {
		if reached.Add(1) == 2 {
			once.Do(func() { close(both) })
		}
		select {
		case <-both:
		case <-time.After(20 * time.Second):
		}
	}
	for _, id := range []domain.ID{g.f.input.AccountID, second} {
		r, _ := g.f.accountRecordOf(t, id)
		if _, err := g.f.client.RequestSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: r.Revision}, Action: grokLogoutAction})); err != nil {
			t.Fatal(err)
		}
	}
	ctx, stop := context.WithTimeout(g.ownerContext(), 30*time.Second)
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		g.f.service.runServerSubscriptions(ctx)
	}()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		settled := 0
		for _, id := range []domain.ID{g.f.input.AccountID, second} {
			if _, a := g.f.accountRecordOf(t, id); a.Subscription.ServerOperation != nil && a.Subscription.ServerOperation.State == domain.SubscriptionSucceeded {
				settled++
			}
		}
		if settled == 2 {
			break
		}
	}
	stop()
	<-scanned
	select {
	case <-both:
	default:
		t.Fatal("the two accounts did not reach the provider at the same time")
	}
	for _, id := range []domain.ID{g.f.input.AccountID, second} {
		_, a := g.f.accountRecordOf(t, id)
		if a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded && a.Subscription.ServerOperation.State != domain.SubscriptionRecovery {
			t.Fatalf("account %s did not settle", id)
		}
	}
}

func (f *grokFixture) accountRecordOf(t *testing.T, id domain.ID) (store.Record, domain.Account) {
	t.Helper()
	var r store.Record
	var a domain.Account
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		r, a, err = accountFromTx(tx, id, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, a
}
