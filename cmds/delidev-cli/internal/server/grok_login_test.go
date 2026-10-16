// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
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

const (
	grokWaiting  = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
	grokSucceeds = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED
	grokFailedSt = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED
	grokCanceled = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
	grokExpired  = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED
	grokRecovery = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED
)

// grokLoginRun drives one server-owned Grok browser login over the real
// Connect handlers against the fake authorization server.
type grokLoginRun struct {
	t      *testing.T
	f      *grokFixture
	idp    *grokidptest.IdP
	logs   *grokLogCapture
	op     string
	done   chan struct{}
	cancel context.CancelFunc
	port   int
	url    string
}

func (f *grokFixture) grokLoginRequest(t *testing.T, edit func(*pb.RequestSubscriptionRequest)) (*pb.RequestSubscriptionResponse, error) {
	t.Helper()
	r, _ := f.record()
	request := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}
	if edit != nil {
		edit(request)
	}
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

// newGrokLogin requests the login and wires the fake server; the runner is not
// started until run is called.
func newGrokLogin(t *testing.T) *grokLoginRun {
	t.Helper()
	f := newGrokFixture(t)
	idp := grokidptest.New()
	f.service.grokLoginTransport = idp.Transport()
	logs := f.captureLogs()
	response, err := f.grokLoginRequest(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &grokLoginRun{t: t, f: f, idp: idp, logs: logs, op: response.OperationId}
}

func (g *grokLoginRun) run() {
	g.t.Helper()
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	g.cancel = cancel
	g.done = make(chan struct{})
	go func() {
		defer close(g.done)
		g.f.service.runGrokLogin(ctx, g.f.input.AccountID)
	}()
	g.t.Cleanup(func() { cancel(); <-g.done })
}

func (g *grokLoginRun) progress() *pb.GetSubscriptionProgressResponse {
	g.t.Helper()
	return g.f.progressFor(g.op)
}

// waitState polls progress until it reports one of the states.
func (g *grokLoginRun) waitState(want ...pb.SubscriptionLoginState) *pb.GetSubscriptionProgressResponse {
	g.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		p := g.progress()
		for _, state := range want {
			if p.State == state && (state != grokWaiting || p.Url != "") {
				return p
			}
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("progress stayed %v, want %v", p.State, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (g *grokLoginRun) await() {
	g.t.Helper()
	select {
	case <-g.done:
	case <-time.After(30 * time.Second):
		g.t.Fatal("login runner did not finish")
	}
}

// waiting starts the runner and returns the published authorization URL.
func (g *grokLoginRun) waiting() string {
	g.t.Helper()
	g.run()
	p := g.waitState(grokWaiting)
	g.url = p.Url
	u, err := url.Parse(p.Url)
	if err != nil {
		g.t.Fatal(err)
	}
	redirect, err := url.Parse(u.Query().Get("redirect_uri"))
	if err != nil {
		g.t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(redirect.Host)
	g.port = atoiPort(g.t, port)
	return p.Url
}

func atoiPort(t *testing.T, value string) int {
	t.Helper()
	var port int
	if err := json.Unmarshal([]byte(value), &port); err != nil {
		t.Fatal(err)
	}
	return port
}

// browse plays the browser: the fake server approves the URL and the redirect
// reaches the loopback listener. It returns the HTTP status of the callback.
func (g *grokLoginRun) browse(edit func(callback *url.URL)) int {
	g.t.Helper()
	callback, err := g.idp.Authorize(g.url)
	if err != nil {
		g.t.Fatal(err)
	}
	u, _ := url.Parse(callback)
	if edit != nil {
		edit(u)
	}
	return g.hit(u.String(), "")
}

func (g *grokLoginRun) hit(target, host string) int {
	g.t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	if host != "" {
		request.Host = host
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		g.t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), "fake-code") {
		g.t.Fatal("the callback page echoed the code")
	}
	return response.StatusCode
}

func (g *grokLoginRun) listenerClosed() bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", itoa(g.port)), 200*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func itoa(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// noLeak asserts that no secret reached a log line, a progress message or the
// persisted account document.
func (g *grokLoginRun) noLeak(secrets ...string) {
	g.t.Helper()
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	_, a := g.f.record()
	document, _ := json.Marshal(a)
	p := g.progress()
	view, _ := json.Marshal(p)
	secrets = append(secrets, g.idp.AccessToken, g.idp.RefreshToken, "fake-code-", "code_verifier", "auth.x.ai/oauth2/authorize")
	if g.url != "" {
		u, _ := url.Parse(g.url)
		secrets = append(secrets, u.Query().Get("state"), u.Query().Get("nonce"), u.Query().Get("code_challenge"))
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for name, text := range map[string]string{"logs": logs, "document": string(document)} {
			if strings.Contains(text, secret) {
				g.t.Fatalf("%s contain a secret value", name)
			}
		}
		if p.State != grokWaiting && strings.Contains(string(view), secret) {
			g.t.Fatal("terminal progress contains a secret value")
		}
	}
	if !strings.Contains(logs, `"phase":"LOGIN"`) || !strings.Contains(logs, g.op) {
		g.t.Fatal("login records lack the LOGIN phase or the operation correlation id")
	}
}

func (g *grokLoginRun) account() domain.Account {
	g.t.Helper()
	_, a := g.f.record()
	if err := a.Subscription.Validate(a); err != nil {
		g.t.Fatalf("persisted account violates the subscription invariants: %v", err)
	}
	return a
}

func (g *grokLoginRun) vaultEmpty() bool {
	g.f.secrets.mu.Lock()
	defer g.f.secrets.mu.Unlock()
	return len(g.f.secrets.values) == 0
}

func TestGrokBrowserLoginStoresABundleThroughTheFakeAuthorizationServer(t *testing.T) {
	g := newGrokLogin(t)
	_, queued := g.f.record()
	st := queued.Subscription
	if st.ServerOperation == nil || st.ServerOperation.State != domain.SubscriptionPreparing || st.Pending == nil || st.Pending.MachineID != "" || st.Pending.Action != domain.SubscriptionLogin || st.Pending.DeviceCode {
		t.Fatal("the Grok login was not accepted as a server-owned browser operation")
	}
	if p := g.progress(); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING || p.Url != "" {
		t.Fatalf("progress before the runner = %v", p)
	}
	authorization := g.waiting()
	prefix := "https://auth.x.ai/oauth2/authorize?response_type=code&client_id=b1a00492-073a-47ea-816f-4c329264a828&redirect_uri=http%3A%2F%2F127.0.0.1%3A"
	if !strings.HasPrefix(authorization, prefix) || !strings.Contains(authorization, "%2Fcallback&scope=openid%20profile%20email%20offline_access%20grok-cli%3Aaccess%20api%3Aaccess%20conversations%3Aread%20conversations%3Awrite%20workspaces%3Aread%20workspaces%3Awrite&code_challenge=") || !strings.HasSuffix(authorization, "&referrer=grok-build") {
		t.Fatal("authorization URL differs from the observed shape")
	}
	if port, _, ok := grokAuthorizationTarget(authorization); !ok || port != g.port || port < 1024 {
		t.Fatal("authorization URL is not the shape the desktop receiver accepts")
	}
	if p := g.progress(); p.UserCode != "" || p.GrokDiagnostic != nil || p.Generation != "" {
		t.Fatalf("waiting progress carries extra presentation: %v", p)
	}
	if status := g.browse(nil); status != http.StatusOK {
		t.Fatalf("callback status = %d", status)
	}
	done := g.waitState(grokSucceeds)
	g.await()
	a := g.account()
	st = a.Subscription
	if a.Connection == nil || a.Health != domain.AccountReady || st.Pending != nil || st.RecoveryRequired || st.ServerOperation.NativeStarted || !st.ServerOperation.ExchangeClaimed || st.Generation == "" || st.Generation != st.ServerOperation.FinishID || done.Generation != string(st.Generation) {
		t.Fatal("login did not publish the generation")
	}
	if done.Url != "" || done.GrokDiagnostic != nil {
		t.Fatalf("terminal progress = %v", done)
	}
	raw, err := g.f.secrets.Get(context.Background(), credentials.Ref{Owner: g.f.input.AccountID, ID: st.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenGrokBundle(raw, g.f.service.Identity.ServerID, g.f.input.AccountID, st.Generation)
	if err != nil {
		t.Fatal(err)
	}
	identity := bundle.Identity()
	if identity.UserID != "fake-user-1" || identity.PrincipalType != "user" || identity.PrincipalID != "fake-principal-1" || st.IdentityCommitment != g.f.service.grokIdentityCommitment(bundle) {
		t.Fatal("stored bundle identity is wrong")
	}
	authFile, err := bundle.AuthFile()
	if err != nil || !strings.Contains(string(authFile), `"oidc_issuer":"https://auth.x.ai"`) || !strings.Contains(string(authFile), `"user_id":"fake-user-1"`) {
		t.Fatalf("auth.json: %v", err)
	}
	parsed, err := subscription.ParseGrokAuthFile(authFile)
	if err != nil || !parsed.Equal(bundle) {
		t.Fatal("stored bundle does not round-trip through ParseGrokAuthFile")
	}
	resealed, err := subscription.SealGrokBundle(g.f.service.Identity.ServerID, g.f.input.AccountID, st.Generation, parsed)
	if err != nil || string(resealed) != string(raw) {
		t.Fatal("stored bundle does not round-trip through SealGrokBundle")
	}
	if g.idp.TokenRequests != 1 || g.idp.TokenAuthHeaders[0] != "" || !g.listenerClosed() {
		t.Fatal("expected exactly one public-client token request and a closed listener")
	}
	g.noLeak()
}

func TestGrokBrowserLoginIgnoresRequestsOutsideTheExactCallback(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	base := "http://127.0.0.1:" + itoa(g.port)
	if status := g.hit(base+"/other?code=x&state=y", ""); status != http.StatusNotFound {
		t.Fatalf("wrong path = %d", status)
	}
	if status := g.hit(base+"/callback?code=x&state=y", "localhost:"+itoa(g.port)); status != http.StatusForbidden {
		t.Fatalf("non-loopback host = %d", status)
	}
	if status := g.hit(base+"/callback?code=x&state=y", "127.0.0.1:1"); status != http.StatusForbidden {
		t.Fatalf("wrong port = %d", status)
	}
	if p := g.progress(); p.State != grokWaiting || g.idp.TokenRequests != 0 {
		t.Fatal("an ignored request ended the flow or reached the exchange")
	}
	if status := g.browse(nil); status != http.StatusOK {
		t.Fatalf("valid callback = %d", status)
	}
	g.waitState(grokSucceeds)
	g.await()
	if g.idp.TokenRequests != 1 {
		t.Fatal("ignored requests changed the number of token requests")
	}
}

func TestGrokCallbackServerRefusesNonLoopbackPeersAndSecondCallbacks(t *testing.T) {
	f := newGrokFixture(t)
	c, err := f.service.startGrokCallbackServer("state-value")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	request := func(remote, target string) int {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.RemoteAddr, r.Host = remote, c.host
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w.Code
	}
	if status := request("192.0.2.10:5000", "/callback?code=c&state=state-value"); status != http.StatusForbidden || len(c.results) != 0 {
		t.Fatal("a non-loopback peer reached the callback")
	}
	if status := request("[::1]:5000", "/callback?code=c&state=state-value"); status != http.StatusForbidden {
		t.Fatal("an IPv6 peer reached the IPv4 callback")
	}
	if status := request("127.0.0.1:5000", "/callback?code=c&state=state-value"); status != http.StatusOK {
		t.Fatalf("first callback = %d", status)
	}
	if status := request("127.0.0.1:5001", "/callback?code=d&state=state-value"); status != http.StatusConflict || len(c.results) != 1 {
		t.Fatal("a second callback was accepted")
	}
	if got := <-c.results; got.code != "c" {
		t.Fatal("the first callback was replaced")
	}
}

func TestGrokBrowserLoginFailuresNeverSendASecondTokenRequest(t *testing.T) {
	type failure struct {
		name     string
		prepare  func(*grokLoginRun)
		edit     func(*url.URL)
		state    pb.SubscriptionLoginState
		tokens   int
		claimed  bool
		code     domain.Code
		recovery bool
	}
	setQuery := func(key, value string) func(*url.URL) {
		return func(u *url.URL) {
			q := u.Query()
			if value == "" {
				q.Del(key)
			} else {
				q.Set(key, value)
			}
			u.RawQuery = q.Encode()
		}
	}
	for _, test := range []failure{
		{name: "wrong state", edit: setQuery("state", "another-state"), state: grokFailedSt, code: domain.InvalidArgument},
		{name: "missing state", edit: setQuery("state", ""), state: grokFailedSt, code: domain.InvalidArgument},
		{name: "missing code", edit: setQuery("code", ""), state: grokFailedSt, code: domain.InvalidArgument},
		{name: "access denied", edit: func(u *url.URL) { u.RawQuery = "error=access_denied&state=" + url.QueryEscape(u.Query().Get("state")) }, state: grokFailedSt, code: domain.PermissionDenied},
		{name: "provider error", edit: func(u *url.URL) { u.RawQuery = "error=server_error&state=" + url.QueryEscape(u.Query().Get("state")) }, state: grokFailedSt, code: domain.InvalidArgument},
		{name: "wrong nonce", prepare: func(g *grokLoginRun) { g.idp.NonceOverride = "another-nonce" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "bad signature", prepare: func(g *grokLoginRun) { g.idp.SignWithOtherKey = true }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "wrong issuer", prepare: func(g *grokLoginRun) { g.idp.IDTokenIssuer = "https://evil.example" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "wrong audience", prepare: func(g *grokLoginRun) { g.idp.IDTokenAudience = "another-client" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "expired token", prepare: func(g *grokLoginRun) { g.idp.IDTokenLifetime = -time.Minute }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "userinfo subject mismatch", prepare: func(g *grokLoginRun) { g.idp.UserinfoSubject = "someone-else" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unavailable},
		{name: "missing principal", prepare: func(g *grokLoginRun) { g.idp.PrincipalID = "" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "token rejected", prepare: func(g *grokLoginRun) { g.idp.TokenStatus = http.StatusBadRequest }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "token success with a truncated body", prepare: func(g *grokLoginRun) { g.idp.TokenBody = `{"access_token":"a","token_type":"Bearer","expires_in":` }, state: grokRecovery, tokens: 1, claimed: true, recovery: true},
		{name: "token success without the required fields", prepare: func(g *grokLoginRun) { g.idp.TokenBody = `{"access_token":"a","token_type":"mac","expires_in":5}` }, state: grokRecovery, tokens: 1, claimed: true, recovery: true},
		{name: "authorized party of another client", prepare: func(g *grokLoginRun) { g.idp.IDTokenAuthorizedParty = "another-client" }, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "extra audience", prepare: func(g *grokLoginRun) {
			g.idp.IDTokenAudiences = []string{subscription.GrokClientID, "another-client"}
			g.idp.IDTokenAuthorizedParty = subscription.GrokClientID
		}, state: grokFailedSt, tokens: 1, claimed: true, code: domain.Unauthenticated},
		{name: "token throttled", prepare: func(g *grokLoginRun) { g.idp.TokenStatus = http.StatusTooManyRequests }, state: grokRecovery, tokens: 1, claimed: true, recovery: true},
		{name: "token request timeout", prepare: func(g *grokLoginRun) { g.idp.TokenStatus = http.StatusRequestTimeout }, state: grokRecovery, tokens: 1, claimed: true, recovery: true},
		{name: "token server error", prepare: func(g *grokLoginRun) { g.idp.TokenStatus = http.StatusBadGateway }, state: grokRecovery, tokens: 1, claimed: true, recovery: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newGrokLogin(t)
			if test.prepare != nil {
				test.prepare(g)
			}
			g.waiting()
			g.browse(test.edit)
			p := g.waitState(test.state)
			g.await()
			if test.recovery {
				if p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
					t.Fatalf("recovery progress = %v", p)
				}
			} else {
				requireGrokProgressOnly(t, p, test.state, test.code, g.op)
			}
			a := g.account()
			if g.idp.TokenRequests != test.tokens || a.Subscription.ServerOperation.ExchangeClaimed != test.claimed || a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() {
				t.Fatalf("token requests = %d, claimed = %v, connected = %v", g.idp.TokenRequests, a.Subscription.ServerOperation.ExchangeClaimed, a.Connection != nil)
			}
			if a.Subscription.RecoveryRequired != test.recovery || (!test.recovery && a.Subscription.Pending != nil) {
				t.Fatal("pending operation or recovery fence is wrong")
			}
			if !g.listenerClosed() {
				t.Fatal("the listener outlived the failed flow")
			}
			g.noLeak()
		})
	}
}

func requireGrokProgressOnly(t *testing.T, p *pb.GetSubscriptionProgressResponse, state pb.SubscriptionLoginState, code domain.Code, operation string) {
	t.Helper()
	requireGrokProgress(t, p, state, domain.GrokLogin, code, operation)
}

func TestGrokBrowserLoginRejectsADuplicateIdentity(t *testing.T) {
	g := newGrokLogin(t)
	raw, err := json.Marshal(map[string]any{subscription.GrokScopeKey: map[string]any{
		"key": "other-key", "auth_mode": "oidc", "create_time": "2026-10-14T00:00:00Z", "expires_at": "2026-10-14T06:00:00Z",
		"user_id": "fake-user-1", "principal_type": "user", "principal_id": "fake-principal-1", "refresh_token": "other-refresh",
		"oidc_issuer": subscription.GrokIssuer, "oidc_client_id": subscription.GrokClientID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := subscription.ParseGrokAuthFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	other := domain.NewID()
	_, account := g.f.record()
	_, err = g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.owner", nil, func(tx *store.Tx) (any, error) {
		account.SubscriptionService = domain.SubscriptionGrok
		account.Subscription = &domain.SubscriptionState{IdentityCommitment: g.f.service.grokIdentityCommitment(owner)}
		return tx.Put(domain.AccountKind, other, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	g.waiting()
	g.browse(nil)
	p := g.waitState(grokFailedSt)
	g.await()
	requireGrokProgressOnly(t, p, grokFailedSt, domain.Conflict, g.op)
	if a := g.account(); a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() {
		t.Fatal("a duplicate identity was stored")
	}
}

// An identity scan that fails for a reason other than a confirmed duplicate
// (here an unrelated account record that cannot be decoded) says nothing about
// the identity. The exchange already completed, so the login must fence the
// account for recovery instead of settling a definitive FAILED or a duplicate.
func TestGrokBrowserLoginFencesAnIdentityScanFailureAsRecovery(t *testing.T) {
	g := newGrokLogin(t)
	unrelated := domain.NewID()
	_, account := g.f.record()
	_, err := g.f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.unrelated", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, unrelated, 0, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(g.f.service.Store.Root(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE entities SET body=? WHERE id=?", []byte("{"), string(unrelated)); err != nil {
		t.Fatal(err)
	}
	g.waiting()
	g.browse(nil)
	p := g.waitState(grokRecovery)
	g.await()
	if p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
		t.Fatalf("recovery progress = %v", p)
	}
	a := g.account()
	if g.idp.TokenRequests != 1 || !a.Subscription.ServerOperation.ExchangeClaimed || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() {
		t.Fatal("an identity scan failure did not fence the account for recovery")
	}
	g.logs.mu.Lock()
	logs := g.logs.buf.String()
	g.logs.mu.Unlock()
	if strings.Contains(logs, "grok_login_identity_duplicate") {
		t.Fatal("an identity scan failure was logged as a duplicate identity")
	}
}

func TestGrokBrowserLoginCancelBeforeTheCallbackLeavesNothing(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	cancel := func() (*pb.CancelSubscriptionResponse, error) {
		r, _ := g.f.record()
		response, err := g.f.client.CancelSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	if _, err := cancel(); err != nil {
		t.Fatal(err)
	}
	p := g.waitState(grokCanceled)
	g.await()
	if !p.Canceled || p.GrokDiagnostic != nil || p.Url != "" {
		t.Fatalf("cancel progress = %v", p)
	}
	a := g.account()
	if a.Subscription.Pending != nil || a.Subscription.ServerOperation.ExchangeClaimed || a.Subscription.RecoveryRequired || !g.vaultEmpty() || g.idp.TokenRequests != 0 || !g.listenerClosed() {
		t.Fatal("cancel left a pending operation, claim, credential or listener")
	}
	// The closed listener cannot be used by a late callback.
	if _, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", itoa(g.port)), 200*time.Millisecond); err == nil {
		t.Fatal("a canceled login still accepts connections")
	}
	if _, err := cancel(); err == nil {
		t.Fatal("a second cancel found a pending operation")
	}
	g.noLeak()
}

func TestGrokBrowserLoginCancelOfAQueuedLoginSettlesWithoutARunner(t *testing.T) {
	g := newGrokLogin(t)
	r, _ := g.f.record()
	if _, err := g.f.client.CancelSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		t.Fatal(err)
	}
	if p := g.progress(); p.State != grokCanceled || !p.Canceled {
		t.Fatalf("progress = %v", p)
	}
	a := g.account()
	if a.Subscription.Pending != nil || grokLoginCandidate(a) {
		t.Fatal("a canceled queued login is still claimable")
	}
}

func TestGrokBrowserLoginCancelAfterTheClaimIsRecoveryAndNeverResends(t *testing.T) {
	g := newGrokLogin(t)
	entered, release := make(chan struct{}), make(chan struct{})
	g.idp.BeforeToken = func() { close(entered); <-release }
	g.waiting()
	g.browse(nil)
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the token request was never sent")
	}
	if a := g.account(); !a.Subscription.ServerOperation.ExchangeClaimed {
		t.Fatal("the exchange claim was not durable before the token request")
	}
	r, _ := g.f.record()
	if _, err := g.f.client.CancelSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		t.Fatal(err)
	}
	g.waitState(grokRecovery)
	close(release)
	g.await()
	a := g.account()
	if !a.Subscription.RecoveryRequired || a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() || g.idp.TokenRequests != 1 || !g.listenerClosed() {
		t.Fatal("a cancel after the claim was not recovery with a single token request")
	}
	g.noLeak()
}

func TestGrokBrowserLoginShutdownAfterTheClaimIsRecovery(t *testing.T) {
	g := newGrokLogin(t)
	entered, release := make(chan struct{}), make(chan struct{})
	g.idp.BeforeToken = func() { close(entered); <-release }
	g.waiting()
	g.browse(nil)
	<-entered
	g.cancel()
	g.await()
	close(release)
	p := g.progress()
	if p.State != grokRecovery {
		t.Fatalf("progress after shutdown = %v", p)
	}
	if a := g.account(); !a.Subscription.RecoveryRequired || g.idp.TokenRequests != 1 || !g.vaultEmpty() || !g.listenerClosed() {
		t.Fatal("shutdown after the claim must be recovery without a second request")
	}
	// A restarted scanner never relaunches the operation or binds a listener.
	var binds atomic.Int32
	g.f.service.grokListen = func() (net.Listener, error) { binds.Add(1); return net.Listen("tcp4", "127.0.0.1:0") }
	g.f.service.subscriptionEpoch = domain.NewID()
	if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 700*time.Millisecond)
	defer stop()
	g.f.service.runServerSubscriptions(ctx)
	if binds.Load() != 0 || g.idp.TokenRequests != 1 {
		t.Fatal("restart relaunched the login or resent the code")
	}
}

// A flow that decided SUCCEEDED can still lose to the deadline or to a server
// shutdown that lands between its outcome and the settlement commit. The
// settlement must keep the recovery fence and publish nothing.
func TestGrokBrowserLoginInterruptionBeforeSettlementIsRecoveryAndNeverPublishes(t *testing.T) {
	for name, interrupt := range map[string]func(g *grokLoginRun){
		"deadline": func(g *grokLoginRun) {
			g.f.mutateAccount(t, func(a *domain.Account) {
				a.Subscription.ServerOperation.StartedAt = time.Now().Add(-time.Hour)
				a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(-time.Second)
			})
		},
		"shutdown": func(g *grokLoginRun) { g.cancel() },
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokLogin(t)
			g.f.service.grokLoginBeforeSettle = func() { interrupt(g) }
			g.waiting()
			g.browse(nil)
			g.await()
			p := g.progress()
			if p.State != grokRecovery {
				t.Fatalf("progress after the interruption = %v", p)
			}
			a := g.account()
			if !a.Subscription.RecoveryRequired || a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() || g.idp.TokenRequests != 1 {
				t.Fatal("an interrupted settlement published credentials or dropped the recovery fence")
			}
			g.noLeak()
		})
	}
}

func TestGrokBrowserLoginRestartLeavesNoListener(t *testing.T) {
	g := newGrokLogin(t)
	g.f.service.subscriptionServerEpoch()
	g.f.service.subscriptionEpoch = domain.NewID()
	var binds atomic.Int32
	g.f.service.grokListen = func() (net.Listener, error) { binds.Add(1); return net.Listen("tcp4", "127.0.0.1:0") }
	if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := g.progress(); p.State != grokRecovery {
		t.Fatalf("restart progress = %v", p)
	}
	ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 700*time.Millisecond)
	defer stop()
	g.f.service.runServerSubscriptions(ctx)
	if binds.Load() != 0 {
		t.Fatal("restart bound a listener")
	}
	g.account()
}

func TestGrokBrowserLoginExpiry(t *testing.T) {
	t.Run("while waiting", func(t *testing.T) {
		g := newGrokLogin(t)
		g.f.mutateAccount(t, func(a *domain.Account) {
			a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(1500 * time.Millisecond)
		})
		g.run()
		g.waitState(grokExpired)
		g.await()
		a := g.account()
		if a.Subscription.Pending != nil || a.Subscription.ServerOperation.ExchangeClaimed || !g.vaultEmpty() || g.idp.TokenRequests != 0 {
			t.Fatal("expiry left work behind")
		}
		p := g.progress()
		if p.Url != "" || p.Canceled {
			t.Fatalf("expired progress = %v", p)
		}
	})
	t.Run("before the claim", func(t *testing.T) {
		g := newGrokLogin(t)
		g.f.mutateAccount(t, func(a *domain.Account) { a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(-time.Second) })
		g.f.mutateAccount(t, func(a *domain.Account) { a.Subscription.ServerOperation.StartedAt = time.Now().Add(-time.Hour) })
		g.run()
		g.await()
		if p := g.progress(); p.State != grokExpired {
			t.Fatalf("progress = %v", p)
		}
	})
}

func TestGrokBrowserLoginRequestValidation(t *testing.T) {
	f := newGrokFixture(t)
	for name, edit := range map[string]func(*pb.RequestSubscriptionRequest){
		"machine": func(r *pb.RequestSubscriptionRequest) { r.MachineId = string(f.input.MachineID) },
		"logout machine": func(r *pb.RequestSubscriptionRequest) {
			r.Action, r.MachineId = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, string(f.input.MachineID)
		},
		"logout device code": func(r *pb.RequestSubscriptionRequest) {
			r.Action, r.DeviceCode = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT, true
		},
	} {
		if _, err := f.grokLoginRequest(t, edit); err == nil || domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
			t.Fatalf("%s was not refused as unsupported: %v", name, err)
		}
	}
	// A logout needs a connected account: this one has no credential yet.
	if _, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT }); err == nil {
		t.Fatal("a logout of an unconnected account was accepted")
	}
	// A refresh needs a connected account: this one has no credential yet.
	if _, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH }); err == nil {
		t.Fatal("a refresh of an unconnected account was accepted")
	}
	if _, a := f.record(); a.Subscription != nil && (a.Subscription.Pending != nil || a.Subscription.ServerOperation != nil) {
		t.Fatal("a refused request left an operation")
	}
	if _, err := f.grokLoginRequest(t, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.grokLoginRequest(t, nil); err == nil {
		t.Fatal("a second login was accepted while the first is active")
	}
}

func TestGrokBrowserLoginForwardsARemoteCallbackToTheOriginalListener(t *testing.T) {
	g := newGrokLogin(t)
	g.waiting()
	callback, err := g.idp.Authorize(g.url)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(callback)
	forward := func(query string) error {
		_, err := g.f.client.ForwardSubscriptionCallback(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.ForwardSubscriptionCallbackRequest{AccountId: string(g.f.input.AccountID), OperationId: g.op, CallbackQuery: []byte(query)}))
		return err
	}
	bad := url.Values{"code": {grokidptest.Code(callback)}, "state": {"another-state"}}
	if err := forward(bad.Encode()); err == nil {
		t.Fatal("a callback with the wrong state was forwarded")
	}
	if err := forward(u.RawQuery + "&extra=1"); err == nil {
		t.Fatal("a callback with an extra key was forwarded")
	}
	if err := forward(u.RawQuery); err != nil {
		t.Fatal(err)
	}
	if err := forward(u.RawQuery); err == nil {
		t.Fatal("a callback was forwarded twice")
	}
	g.waitState(grokSucceeds)
	g.await()
	if g.idp.TokenRequests != 1 {
		t.Fatal("the forwarded callback produced the wrong number of token requests")
	}
	g.noLeak()
}

func TestGrokSubscriptionLoginCapabilityDoesNotEnableExecution(t *testing.T) {
	f := newGrokFixture(t)
	response, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	login := false
	for _, capability := range response.Msg.Capabilities {
		if capability == pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_LOGIN_V1 {
			login = true
		}
		if capability == pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_EXECUTION_V1 {
			t.Fatal("login enabled Grok execution")
		}
	}
	if !login {
		t.Fatal("server-owned Grok CLI login is not advertised")
	}
}
