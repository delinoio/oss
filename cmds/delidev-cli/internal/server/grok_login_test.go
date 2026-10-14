// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// lockedBuffer collects structured logs from the login goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type grokLoginFixture struct {
	*grokFixture
	idp  *grokidp.IdP
	logs *lockedBuffer
}

func newGrokLoginFixture(t *testing.T) *grokLoginFixture {
	t.Helper()
	g := &grokLoginFixture{grokFixture: newGrokFixture(t), idp: grokidp.New(t), logs: &lockedBuffer{}}
	g.service.grokOAuthTransport = g.idp.Transport
	g.service.logger = slog.New(slog.NewJSONHandler(g.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return g
}

// grokRun is one launched login with its published authorization.
type grokRun struct {
	op      *pb.RequestSubscriptionResponse
	url     string
	port    int
	state   string
	nonce   string
	done    <-chan struct{}
	cancel  context.CancelFunc
	account domain.ID
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// launch starts the login goroutine for an already requested operation.
func (g *grokLoginFixture) launch(t *testing.T, op *pb.RequestSubscriptionResponse) *grokRun {
	t.Helper()
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	account := g.input.AccountID
	go func() {
		defer close(done)
		g.service.runServerSubscription(ctx, account)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return &grokRun{op: op, done: done, cancel: cancel, account: account}
}

// begin requests a login, launches it and waits for the published URL.
func (g *grokLoginFixture) begin(t *testing.T) *grokRun {
	t.Helper()
	run := g.launch(t, g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN))
	waitUntil(t, "the authorization URL", func() bool {
		run.url = g.progressFor(run.op.OperationId).Url
		return run.url != ""
	})
	state, port, ok := subscription.GrokAuthorizationDetails(run.url)
	if !ok {
		t.Fatal("published URL is not a valid Grok authorization URL")
	}
	u, _ := url.Parse(run.url)
	run.state, run.port, run.nonce = state, port, u.Query().Get("nonce")
	return run
}

func (g *grokLoginFixture) awaitDone(t *testing.T, run *grokRun) {
	t.Helper()
	awaitServerFixture(t, run.done)
}

func (g *grokLoginFixture) progress(run *grokRun) *pb.GetSubscriptionProgressResponse {
	return g.progressFor(run.op.OperationId)
}

func (g *grokLoginFixture) cancelLogin(t *testing.T) {
	t.Helper()
	r, _ := g.record()
	if _, err := g.client.CancelSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		t.Fatal(err)
	}
}

// callback performs one loopback request with an optional Host override.
func callback(port int, method, pathAndQuery, host string) (int, error) {
	request, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, pathAndQuery), nil)
	if err != nil {
		return 0, err
	}
	if host != "" {
		request.Host = host
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func (r *grokRun) good(code string) string {
	return "/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(r.state)
}

// requireClosed proves that nothing listens on the login's port any more.
func requireClosed(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = connection.Close()
		if time.Now().After(deadline) {
			t.Fatalf("listener on port %d is still open", port)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireOpen(t *testing.T, port int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("listener on port %d is not open: %v", port, err)
	}
	_ = connection.Close()
}

// requireNoLeak scans the structured logs and the durable metadata for the
// secret and presentation values of one login.
func (g *grokLoginFixture) requireNoLeak(t *testing.T, run *grokRun, extra ...string) {
	t.Helper()
	secrets := append([]string{run.url, run.state, run.nonce, "fake-access-token", "fake-refresh-token", "code_challenge", "oauth2/authorize", "eyJ"}, extra...)
	if form := g.idp.LastTokenForm(); form != nil {
		secrets = append(secrets, form.Get("code"), form.Get("code_verifier"))
	}
	logs := g.logs.String()
	if !strings.Contains(logs, `"phase":"LOGIN"`) || !strings.Contains(logs, string(run.op.OperationId)) {
		t.Fatal("logs lack the LOGIN phase or the operation correlation id")
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("a secret or URL value reached the logs: %.8s", secret)
		}
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(g.service.Store.Root(), name))
		if err != nil {
			continue
		}
		for _, secret := range secrets {
			if secret != "" && secret != "eyJ" && bytes.Contains(data, []byte(secret)) {
				t.Fatalf("a secret reached durable metadata: %.8s", secret)
			}
		}
	}
}

func (g *grokLoginFixture) refs(t *testing.T) int {
	t.Helper()
	refs, err := g.secrets.UnremovedReferences(context.Background(), g.input.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	return len(refs)
}

func TestGrokLoginBrowserFlowCompletesWithAFakeProvider(t *testing.T) {
	g := newGrokLoginFixture(t)
	_, before := g.record()
	if before.SubscriptionService != domain.SubscriptionGrok {
		t.Fatal("fixture is not a Grok account")
	}
	run := g.begin(t)
	r, a := g.record()
	o := a.Subscription.ServerOperation
	if a.Subscription.Pending.MachineID != "" || o.ExpiresAt.Sub(o.StartedAt) != 15*time.Minute || o.Action != domain.SubscriptionLogin || !o.NativeStarted {
		t.Fatal("Grok login did not use the server operation model")
	}
	if p := g.progress(run); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING || p.UserCode != "" || p.GrokDiagnostic != nil || p.SuggestedName != "" || p.Generation != "" {
		t.Fatal("waiting progress is wrong")
	}
	if run.port < subscription.GrokMinCallbackPort {
		t.Fatal("login bound a privileged port")
	}
	if bytes.Contains(r.Data, []byte(run.state)) || bytes.Contains(r.Data, []byte(run.url)) {
		t.Fatal("the authorization URL or state reached the account document")
	}
	requireOpen(t, run.port)
	grant := g.idp.Approve(t, run.url)
	if status, err := callback(run.port, http.MethodGet, run.good(grant.Code), ""); err != nil || status != http.StatusOK {
		t.Fatalf("callback: %d %v", status, err)
	}
	g.awaitDone(t, run)
	requireClosed(t, run.port)
	_, a = g.record()
	p := g.progress(run)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || p.Generation != string(a.Subscription.Generation) || p.SuggestedName != "fixture@example.invalid" || p.GrokDiagnostic != nil || p.Url != "" {
		t.Fatalf("success progress is wrong: %+v", p)
	}
	st := a.Subscription
	if a.Connection == nil || a.Health != domain.AccountReady || st.Pending != nil || st.Lease != nil || st.ServerOperation.NativeStarted || st.ServerOperation.ID != domain.ID(run.op.OperationId) || st.Generation != st.ServerOperation.FinishID || !st.ServerOperation.CodeConsumed || st.ServerOperation.CodeSubmissionID == "" {
		t.Fatal("account document does not carry the operation and generation")
	}
	stored, err := g.secrets.Get(context.Background(), credentials.Ref{Owner: g.input.AccountID, ID: st.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenGrokBundle(stored, g.service.Identity.ServerID, g.input.AccountID, st.Generation)
	if err != nil {
		t.Fatalf("sealed envelope: %v", err)
	}
	if bundle.Identity().UserID != "fake-user-1" || bundle.Identity().PrincipalType != "user" || st.IdentityCommitment != g.service.grokIdentityCommitment(bundle) || bundle.SuggestedName() != "fixture@example.invalid" {
		t.Fatal("bundle identity or commitment is wrong")
	}
	file, _ := bundle.AuthFile()
	if !strings.Contains(string(file), `"oidc_issuer":"https://auth.x.ai"`) || !strings.Contains(string(file), subscription.GrokClientID) {
		t.Fatal("bundle lacks the fixed issuer and client")
	}
	if g.refs(t) != 1 || g.idp.TokenRequests() != 1 || g.idp.UserInfoRequests() != 1 {
		t.Fatal("unexpected protected generations or outbound requests")
	}
	form := g.idp.LastTokenForm()
	if form.Get("client_secret") != "" || form.Get("code_verifier") == "" || form.Get("redirect_uri") != subscription.GrokRedirectURI(run.port) {
		t.Fatal("exchange was not a PKCE public-client exchange against the original redirect")
	}
	g.requireNoLeak(t, run, grant.Code)
	// The account can neither start a second login nor borrow the first.
	again, _ := g.record()
	if _, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(g.input.AccountID), ExpectedRevision: again.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err == nil {
		t.Fatal("a connected Grok account accepted another login")
	}
}

func TestGrokServerLoginRejectsDeviceCodeAndOtherActions(t *testing.T) {
	g := newGrokLoginFixture(t)
	r, _ := g.record()
	for name, request := range map[string]*pb.RequestSubscriptionRequest{
		"device code": {Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true},
		"refresh":     {Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH},
		"logout":      {Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT},
	} {
		request.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}
		_, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, request))
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	_, a := g.record()
	if a.Subscription != nil && (a.Subscription.Pending != nil || a.Subscription.ServerOperation != nil) {
		t.Fatal("a rejected request left an operation")
	}
}

func TestGrokLoginRefusesInvalidCallbacksWithoutConsumingTheFlow(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	port := run.port
	cases := []struct {
		name, method, target, host string
	}{
		{"wrong state", http.MethodGet, "/callback?code=" + grant.Code + "&state=wrong-state-value", ""},
		{"wrong path", http.MethodGet, "/other?code=" + grant.Code + "&state=" + run.state, ""},
		{"trailing path", http.MethodGet, "/callback/?code=" + grant.Code + "&state=" + run.state, ""},
		{"non-loopback host", http.MethodGet, run.good(grant.Code), "attacker.example:" + fmt.Sprint(port)},
		{"wrong port in host", http.MethodGet, run.good(grant.Code), "127.0.0.1:" + fmt.Sprint(port+1)},
		{"localhost host", http.MethodGet, run.good(grant.Code), "localhost:" + fmt.Sprint(port)},
		{"post", http.MethodPost, run.good(grant.Code), ""},
		{"extra key", http.MethodGet, run.good(grant.Code) + "&redirect_uri=https://attacker.example", ""},
		{"missing code", http.MethodGet, "/callback?state=" + run.state, ""},
		{"duplicate state", http.MethodGet, run.good(grant.Code) + "&state=" + run.state, ""},
	}
	for _, c := range cases {
		status, err := callback(port, c.method, c.target, c.host)
		if err != nil || status == http.StatusOK {
			t.Fatalf("%s was accepted: %d %v", c.name, status, err)
		}
	}
	if g.idp.TokenRequests() != 0 || g.progress(run).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
		t.Fatal("an invalid callback reached the token endpoint or ended the flow")
	}
	// The genuine callback still completes exactly once; a second is refused.
	if status, err := callback(port, http.MethodGet, run.good(grant.Code), ""); err != nil || status != http.StatusOK {
		t.Fatalf("genuine callback: %d %v", status, err)
	}
	if status, err := callback(port, http.MethodGet, run.good(grant.Code), ""); err == nil && status == http.StatusOK {
		t.Fatal("a second callback was accepted")
	}
	g.awaitDone(t, run)
	if g.idp.TokenRequests() != 1 {
		t.Fatal("more than one token request was sent")
	}
	requireClosed(t, port)
	g.requireNoLeak(t, run, grant.Code)
}

func TestGrokLoginHandlerRefusesNonLoopbackPeers(t *testing.T) {
	flow := &grokLoginFlow{port: 43817, state: "state-value", callback: make(chan grokCallback, 1)}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43817/callback?code=c&state=state-value", nil)
	request.RemoteAddr = "192.0.2.10:50000"
	recorder := httptest.NewRecorder()
	flow.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || flow.accepted.Load() {
		t.Fatal("a request from another interface was accepted")
	}
	request.RemoteAddr = "127.0.0.1:50000"
	recorder = httptest.NewRecorder()
	flow.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !flow.accepted.Load() || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the loopback request was refused")
	}
}

func TestGrokLoginFailuresSettleWithoutASecondTokenRequest(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*grokidp.IdP)
		query    func(*grokRun, grokidp.Grant) string
		code     domain.Code
		requests int
		consumed bool
	}{
		{name: "wrong nonce", setup: func(i *grokidp.IdP) { i.OverrideNonce = "attacker-nonce" }, code: domain.Unauthenticated, requests: 1, consumed: true},
		{name: "bad signature", setup: func(i *grokidp.IdP) { i.SignWithWrongKey = true }, code: domain.Unauthenticated, requests: 1, consumed: true},
		{name: "expired token", setup: func(i *grokidp.IdP) { i.ExpiredIDToken = true }, code: domain.Unauthenticated, requests: 1, consumed: true},
		{name: "wrong issuer", setup: func(i *grokidp.IdP) { i.OverrideIssuer = "https://evil.example" }, code: domain.Unauthenticated, requests: 1, consumed: true},
		{name: "wrong audience", setup: func(i *grokidp.IdP) { i.OverrideAudience = "other-client" }, code: domain.Unauthenticated, requests: 1, consumed: true},
		{name: "provider rejects the code", setup: func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 400, `{"error":"invalid_grant"}` }, code: domain.PermissionDenied, requests: 1, consumed: true},
		{name: "userinfo fails", setup: func(i *grokidp.IdP) { i.UserInfoStatus = 500 }, code: domain.Unavailable, requests: 1, consumed: true},
		{name: "half principal", setup: func(i *grokidp.IdP) { i.PrincipalType = "team" }, code: domain.InvalidArgument, requests: 1, consumed: true},
		{name: "access denied", query: func(r *grokRun, _ grokidp.Grant) string { return "/callback?error=access_denied&state=" + r.state }, code: domain.PermissionDenied, requests: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			if c.setup != nil {
				c.setup(g.idp)
			}
			run := g.begin(t)
			grant := g.idp.Approve(t, run.url)
			target := run.good(grant.Code)
			if c.query != nil {
				target = c.query(run, grant)
			}
			_, _ = callback(run.port, http.MethodGet, target, "")
			g.awaitDone(t, run)
			requireClosed(t, run.port)
			_, a := g.record()
			st := a.Subscription
			p := g.progress(run)
			if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED || a.Connection != nil || st.Generation != "" || st.IdentityCommitment != "" || st.Pending != nil || st.RecoveryRequired || a.Health != domain.AccountDisconnected || g.refs(t) != 0 {
				t.Fatalf("failed login left credentials or a fence: %v", p.State)
			}
			if st.ServerOperation.CodeConsumed != c.consumed || g.idp.TokenRequests() != c.requests {
				t.Fatalf("consumed=%v requests=%d", st.ServerOperation.CodeConsumed, g.idp.TokenRequests())
			}
			d := p.GrokDiagnostic
			if d == nil || d.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN || d.Code != string(c.code) || d.CorrelationId != run.op.OperationId || d.Message == "" || d.Guidance == "" {
				t.Fatalf("grok_diagnostic is wrong: %+v", d)
			}
			g.requireNoLeak(t, run, grant.Code)
			// The failed account is free for a fresh login.
			if g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN) == nil {
				t.Fatal("failed login blocked a retry")
			}
		})
	}
}

func TestGrokLoginUncertainExchangeRequiresRecoveryAndIsNeverResent(t *testing.T) {
	for name, setup := range map[string]func(*grokidp.IdP){
		"server error":      func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, "unavailable" },
		"malformed success": func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` },
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokLoginFixture(t)
			setup(g.idp)
			run := g.begin(t)
			grant := g.idp.Approve(t, run.url)
			if status, err := callback(run.port, http.MethodGet, run.good(grant.Code), ""); err != nil || status != http.StatusOK {
				t.Fatalf("callback: %d %v", status, err)
			}
			g.awaitDone(t, run)
			requireClosed(t, run.port)
			_, a := g.record()
			p := g.progress(run)
			if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || !a.Subscription.RecoveryRequired || a.Subscription.Pending == nil || a.Connection != nil || a.Health != domain.AccountFailed || !a.Subscription.ServerOperation.CodeConsumed {
				t.Fatalf("uncertain exchange did not require recovery: %v", p.State)
			}
			if p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN {
				t.Fatal("recovery lacks its LOGIN diagnostic")
			}
			// Neither the scanner entry points nor another run resend the claim.
			ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			g.service.runServerSubscription(ctx, g.input.AccountID)
			if grokLoginNeedsRecovery(a) || g.service.recoverGrokLogin(ctx, g.input.AccountID, domain.ID(run.op.OperationId)) == nil {
				t.Fatal("a claimed exchange was released automatically")
			}
			if g.idp.TokenRequests() != 1 {
				t.Fatal("the token request was sent again")
			}
			g.requireNoLeak(t, run, grant.Code)
		})
	}
}

func TestGrokLoginCancelBeforeCallbackLeavesNothingBehind(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	requireOpen(t, run.port)
	g.cancelLogin(t)
	g.awaitDone(t, run)
	requireClosed(t, run.port)
	_, a := g.record()
	p := g.progress(run)
	if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED || p.Url != "" || p.GrokDiagnostic != nil || a.Subscription.Pending != nil || a.Connection != nil || a.Subscription.ServerOperation.CodeConsumed || g.idp.TokenRequests() != 0 || g.refs(t) != 0 {
		t.Fatal("cancellation left state behind")
	}
	if g.service.grokFlow(domain.ID(run.op.OperationId)) != nil {
		t.Fatal("the flow stayed registered")
	}
	// A late callback to the closed port cannot be delivered.
	if status, err := callback(run.port, http.MethodGet, run.good("late"), ""); err == nil && status == http.StatusOK {
		t.Fatal("late callback accepted")
	}
	g.requireNoLeak(t, run)
}

func TestGrokLoginCancelDuringExchangeRequiresRecovery(t *testing.T) {
	g := newGrokLoginFixture(t)
	g.idp.TokenGate = make(chan struct{})
	g.idp.TokenEntered = make(chan struct{}, 1)
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	if status, err := callback(run.port, http.MethodGet, run.good(grant.Code), ""); err != nil || status != http.StatusOK {
		t.Fatalf("callback: %d %v", status, err)
	}
	select {
	case <-g.idp.TokenEntered:
	case <-time.After(30 * time.Second):
		t.Fatal("token request never started")
	}
	requireClosed(t, run.port)
	// The claim is durable before the request completes.
	_, a := g.record()
	if !a.Subscription.ServerOperation.CodeConsumed {
		t.Fatal("the exchange claim was not persisted before the token request")
	}
	g.cancelLogin(t)
	g.awaitDone(t, run)
	_, a = g.record()
	if p := g.progress(run); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED || !a.Subscription.RecoveryRequired || a.Connection != nil || g.idp.TokenRequests() != 1 || g.refs(t) != 0 {
		t.Fatalf("cancel during exchange did not retain recovery: %v", p.State)
	}
	g.requireNoLeak(t, run, grant.Code)
}

func TestGrokLoginExpiryClosesTheListener(t *testing.T) {
	g := newGrokLoginFixture(t)
	op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	g.mutateServerOperation(func(a *domain.Account) {
		a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(3 * time.Second)
	})
	run := g.launch(t, op)
	waitUntil(t, "the authorization URL", func() bool {
		run.url = g.progress(run).Url
		return run.url != ""
	})
	_, run.port, _ = subscription.GrokAuthorizationDetails(run.url)
	requireOpen(t, run.port)
	g.awaitDone(t, run)
	requireClosed(t, run.port)
	_, a := g.record()
	if p := g.progress(run); p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED || p.Url != "" || p.GrokDiagnostic != nil || a.Subscription.Pending != nil || g.idp.TokenRequests() != 0 || a.Subscription.ServerOperation.CodeConsumed {
		t.Fatalf("expiry state is wrong: %v", p.State)
	}
}

func TestGrokLoginServerShutdownClosesTheListener(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	run.cancel()
	g.awaitDone(t, run)
	requireClosed(t, run.port)
	_, a := g.record()
	if g.progress(run).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED || a.Subscription.Pending != nil || g.idp.TokenRequests() != 0 {
		t.Fatal("shutdown before the callback did not settle as canceled")
	}
}

func TestGrokLoginRestartKeepsAClaimedExchangeUncertainAndFreesAnUnclaimedOne(t *testing.T) {
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	interrupted := func(g *grokLoginFixture, consumed bool) *pb.RequestSubscriptionResponse {
		op := g.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
		g.mutateServerOperation(func(a *domain.Account) {
			o := a.Subscription.ServerOperation
			o.State, o.NativeStarted, o.LoginMethod = domain.SubscriptionWaiting, true, domain.SubscriptionBrowserCallback
			a.Subscription.Pending.Phase = domain.SubscriptionClaimed
			if consumed {
				o.CodeSubmissionID, o.CodeConsumed = domain.NewID(), true
			}
		})
		g.service.subscriptionEpoch = domain.NewID()
		if err := g.service.initializeServerSubscriptions(ctx); err != nil {
			t.Fatal(err)
		}
		return op
	}
	t.Run("claimed", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		op := interrupted(g, true)
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || grokLoginNeedsRecovery(a) || g.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
			t.Fatal("a claimed exchange was not retained as uncertain")
		}
		if g.service.recoverGrokLogin(ctx, g.input.AccountID, domain.ID(op.OperationId)) == nil {
			t.Fatal("recovery released a claimed exchange")
		}
		g.service.runServerSubscription(ctx, g.input.AccountID)
		if g.idp.TokenRequests() != 0 || g.idp.UserInfoRequests() != 0 {
			t.Fatal("restart contacted the provider")
		}
	})
	t.Run("unclaimed", func(t *testing.T) {
		g := newGrokLoginFixture(t)
		op := interrupted(g, false)
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || !grokLoginNeedsRecovery(a) {
			t.Fatal("an unclaimed interrupted login is not recoverable")
		}
		if err := g.service.recoverGrokLogin(ctx, g.input.AccountID, domain.ID(op.OperationId)); err != nil {
			t.Fatal(err)
		}
		_, a = g.record()
		if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Health != domain.AccountDisconnected || g.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED || g.idp.TokenRequests() != 0 {
			t.Fatal("unclaimed login was not freed")
		}
		if g.progressFor(op.OperationId).GrokDiagnostic == nil {
			t.Fatal("recovered login lacks its diagnostic")
		}
		g.begin(t)
	})
}

// loginAs runs one complete login for the account and identity currently
// selected on the fixture.
func (g *grokLoginFixture) loginAs(t *testing.T) (*grokRun, domain.Account) {
	t.Helper()
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	if status, err := callback(run.port, http.MethodGet, run.good(grant.Code), ""); err != nil || status != http.StatusOK {
		t.Fatalf("callback: %d %v", status, err)
	}
	g.awaitDone(t, run)
	_, a := g.record()
	return run, a
}

func TestGrokLoginRejectsDuplicateIdentityAndAdmitsADifferentPrincipal(t *testing.T) {
	g := newGrokLoginFixture(t)
	firstID, secondID, thirdID := g.input.AccountID, domain.NewID(), domain.NewID()
	_, empty := g.record()
	for _, id := range []domain.ID{secondID, thirdID} {
		if _, err := g.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.extra-account", nil, func(tx *store.Tx) (any, error) {
			return tx.Put(domain.AccountKind, id, 0, "", "", empty)
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, first := g.loginAs(t)
	if first.Connection == nil {
		t.Fatal("first login failed")
	}
	// The same user and principal on another account is a duplicate.
	g.input.AccountID = secondID
	run, second := g.loginAs(t)
	p := g.progress(run)
	if second.Connection != nil || second.Subscription.Generation != "" || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.Conflict) || g.refs(t) != 0 {
		t.Fatalf("duplicate identity was accepted: %v", p.State)
	}
	// A different principal of the same user is a different identity.
	g.idp.PrincipalType, g.idp.PrincipalID = "team", "other-principal"
	g.input.AccountID = thirdID
	_, third := g.loginAs(t)
	if third.Connection == nil || third.Subscription.IdentityCommitment == first.Subscription.IdentityCommitment || g.refs(t) != 1 {
		t.Fatal("a different principal was refused")
	}
	g.input.AccountID = firstID
	if g.refs(t) != 1 {
		t.Fatal("the first account's generation changed")
	}
}

func (g *grokLoginFixture) forward(account, operation string, query string) error {
	_, err := g.client.ForwardSubscriptionCallback(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.ForwardSubscriptionCallbackRequest{AccountId: account, OperationId: operation, CallbackQuery: []byte(query)}))
	return err
}

func TestGrokForwardedCallbackDeliversOnceToTheLoopbackLogin(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	account, operation := string(g.input.AccountID), run.op.OperationId
	good := "code=" + url.QueryEscape(grant.Code) + "&state=" + url.QueryEscape(run.state)
	for name, attempt := range map[string]func() error{
		"wrong state":     func() error { return g.forward(account, operation, "code=x&state=wrong-state-value") },
		"extra key":       func() error { return g.forward(account, operation, good+"&redirect_uri=https://attacker.example") },
		"duplicate code":  func() error { return g.forward(account, operation, good+"&code=other") },
		"wrong operation": func() error { return g.forward(account, string(domain.NewID()), good) },
		"wrong account":   func() error { return g.forward(string(domain.NewID()), operation, good) },
		"empty":           func() error { return g.forward(account, operation, "") },
	} {
		if attempt() == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if g.idp.TokenRequests() != 0 {
		t.Fatal("a refused delivery reached the token endpoint")
	}
	if err := g.forward(account, operation, good); err != nil {
		t.Fatalf("forwarded callback: %v", err)
	}
	// Duplicate and direct deliveries are refused once the code was delivered.
	if g.forward(account, operation, good) == nil {
		t.Fatal("duplicate forwarded callback accepted")
	}
	if status, err := callback(run.port, http.MethodGet, run.good(grant.Code), ""); err == nil && status == http.StatusOK {
		t.Fatal("direct callback accepted after the forwarded one")
	}
	g.awaitDone(t, run)
	if g.forward(account, operation, good) == nil {
		t.Fatal("late forwarded callback accepted")
	}
	_, a := g.record()
	o := a.Subscription.ServerOperation
	if a.Connection == nil || g.idp.TokenRequests() != 1 || !o.CallbackForwarded || !o.CodeConsumed || o.CodeSubmissionID == "" || o.LoginMethod != domain.SubscriptionBrowserCallback {
		t.Fatal("forwarded login did not settle once")
	}
	requireClosed(t, run.port)
	g.requireNoLeak(t, run, grant.Code)
}

func TestGrokForwardedCallbackIsRefusedForADifferentActorAndStaleOperation(t *testing.T) {
	g := newGrokLoginFixture(t)
	run := g.begin(t)
	grant := g.idp.Approve(t, run.url)
	g.cancelLogin(t)
	g.awaitDone(t, run)
	query := "code=" + url.QueryEscape(grant.Code) + "&state=" + url.QueryEscape(run.state)
	if g.forward(string(g.input.AccountID), run.op.OperationId, query) == nil || g.idp.TokenRequests() != 0 {
		t.Fatal("a callback for a canceled login was delivered")
	}
}

func TestGrokServerLoginKeepsTheCodexPathClosedForGrok(t *testing.T) {
	f := newGrokLoginFixture(t)
	// The Codex gate still refuses Grok while the server-operation gate admits it.
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		if _, _, e := subscriptionAccount(tx, f.input.AccountID, 0); e == nil {
			t.Error("subscriptionAccount admitted a Grok account")
		}
		if _, _, e := serverSubscriptionAccount(tx, f.input.AccountID, 0); e != nil {
			t.Errorf("server gate refused a Grok account: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
