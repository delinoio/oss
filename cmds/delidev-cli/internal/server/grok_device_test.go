// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	deviceUserCode    = "ABCD-1234"
	deviceVerifyURL   = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234"
	stateFailed       = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_FAILED
	stateExpired      = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_EXPIRED
	stateCanceled     = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_CANCELED
	stateRecovery     = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED
	stateSucceeded    = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED
	stateWaiting      = pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
	deviceFixtureBase = 1_000_000
)

// fakeGrokClock is a virtual poll clock: Wait advances virtual time instantly
// and records the requested delay, so interval and deadline behavior is
// asserted without real waiting. A hook may block a numbered wait.
type fakeGrokClock struct {
	mu    sync.Mutex
	now   time.Time
	start time.Time
	waits []time.Duration
	hook  func(ctx context.Context, n int) error
}

func newFakeGrokClock() *fakeGrokClock {
	start := time.Unix(deviceFixtureBase, 0)
	return &fakeGrokClock{now: start, start: start}
}

func (c *fakeGrokClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeGrokClock) Wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	n := len(c.waits)
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, n); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func (c *fakeGrokClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func (c *fakeGrokClock) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(c.start)
}

// blockAt blocks wait number n until release is closed or the login ends, and
// signals entered first.
func (c *fakeGrokClock) blockAt(n int, entered chan<- struct{}, release <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hook = func(ctx context.Context, got int) error {
		if got != n {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// pollRecorder wraps the fake provider's transport and stamps every token
// request with the virtual clock; onToken runs before the request is sent.
type pollRecorder struct {
	base    http.RoundTripper
	clock   *fakeGrokClock
	mu      sync.Mutex
	times   []time.Time
	onToken func()
}

func (p *pollRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/oauth2/token" {
		p.mu.Lock()
		p.times = append(p.times, p.clock.Now())
		hook := p.onToken
		p.mu.Unlock()
		if hook != nil {
			hook()
		}
	}
	return p.base.RoundTrip(r)
}

func (p *pollRecorder) Times() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.times...)
}

type grokDeviceFixture struct {
	*grokLoginFixture
	clock *fakeGrokClock
	rec   *pollRecorder
}

func newGrokDeviceFixture(t *testing.T) *grokDeviceFixture {
	t.Helper()
	g := &grokDeviceFixture{grokLoginFixture: newGrokLoginFixture(t), clock: newFakeGrokClock()}
	g.rec = &pollRecorder{base: g.idp.Transport, clock: g.clock}
	g.service.grokOAuthTransport = g.rec
	g.service.grokClock = g.clock
	return g
}

func (g *grokDeviceFixture) requestDevice(t *testing.T, requestID string) (*pb.RequestSubscriptionResponse, error) {
	t.Helper()
	r, _ := g.record()
	result, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: requestID, Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true}))
	if err != nil {
		return nil, err
	}
	return result.Msg, nil
}

// beginDevice requests a device login, launches it and waits until the
// verification URL is published (or the login settled).
func (g *grokDeviceFixture) beginDevice(t *testing.T) *grokRun {
	t.Helper()
	op, err := g.requestDevice(t, string(domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	run := g.launch(t, op)
	// The virtual clock lets a login that is not held settle within milliseconds,
	// and a settled login no longer returns its URL, so a settled login also ends
	// the wait. Tests that need the published URL hold the first wait with
	// holdFirstWait.
	waitUntil(t, "the device verification URL", func() bool {
		p := g.progressFor(run.op.OperationId)
		run.url = p.Url
		return p.Url != "" || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING && p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING
	})
	return run
}

// holdFirstWait keeps a device login waiting before its first poll, so its
// published URL stays observable until the test ends or cancels it.
func (g *grokDeviceFixture) holdFirstWait() {
	g.clock.blockAt(1, make(chan struct{}), make(chan struct{}))
}

// startDevice requests and launches a device login without waiting for the URL.
func (g *grokDeviceFixture) startDevice(t *testing.T) *grokRun {
	t.Helper()
	op, err := g.requestDevice(t, string(domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	return g.launch(t, op)
}

func (g *grokDeviceFixture) settle(t *testing.T, run *grokRun, want pb.SubscriptionLoginState) (*pb.GetSubscriptionProgressResponse, domain.Account) {
	t.Helper()
	g.awaitDone(t, run)
	p := g.progress(run)
	_, a := g.record()
	if p.State != want {
		t.Fatalf("state %v, want %v", p.State, want)
	}
	if p.Url != "" || p.UserCode != "" {
		t.Fatal("a settled login still exposes the verification URL or user code")
	}
	return p, a
}

func (g *grokDeviceFixture) requireNoBundle(t *testing.T, a domain.Account) {
	t.Helper()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.IdentityCommitment != "" || g.refs(t) != 0 {
		t.Fatal("a login that did not succeed left a connection, generation or protected credential")
	}
}

// requireNoDeviceLeak scans logs and durable metadata for the device-only values.
func (g *grokDeviceFixture) requireNoDeviceLeak(t *testing.T, run *grokRun) {
	t.Helper()
	secrets := []string{deviceUserCode, deviceVerifyURL, "accounts.x.ai", g.idp.IssuedDeviceCode(), "fake-device-", "fake-access-token", "fake-refresh-token", "eyJ"}
	logs := g.logs.String()
	if !strings.Contains(logs, `"phase":"LOGIN"`) || !strings.Contains(logs, run.op.OperationId) {
		t.Fatal("logs lack the LOGIN phase or the operation correlation id")
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("a device value reached the logs: %.8s", secret)
		}
	}
	for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(g.service.Store.Root(), name))
		if err != nil {
			continue
		}
		for _, secret := range secrets {
			if secret != "" && secret != "eyJ" && bytes.Contains(data, []byte(secret)) {
				t.Fatalf("a device value reached durable metadata: %.8s", secret)
			}
		}
	}
}

func gaps(times []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(times); i++ {
		out = append(out, times[i].Sub(times[i-1]))
	}
	return out
}

func TestGrokDeviceLoginCompletesAfterPendingPolls(t *testing.T) {
	g := newGrokDeviceFixture(t)
	g.idp.DeviceScript = []string{"authorization_pending", "authorization_pending", "authorization_pending"}
	entered, release := make(chan struct{}), make(chan struct{})
	g.clock.blockAt(1, entered, release)
	var claimedAtFirstPoll bool
	var once sync.Once
	g.rec.onToken = func() {
		once.Do(func() {
			_, a := g.record()
			o := a.Subscription.ServerOperation
			claimedAtFirstPoll = o.CodeConsumed && o.CodeSubmissionID != "" && o.LoginMethod == domain.SubscriptionDeviceCode && o.State == domain.SubscriptionWaiting
		})
	}
	run := g.beginDevice(t)
	_, a := g.record()
	if a.Subscription.Pending.MachineID != "" || !a.Subscription.Pending.DeviceCode || a.Subscription.ServerOperation.LoginMethod != domain.SubscriptionDeviceCode || a.Subscription.ServerOperation.CodeConsumed {
		t.Fatal("device login did not use the server operation model before the first poll")
	}
	waiting := g.progress(run)
	if waiting.State != stateWaiting || waiting.Url != deviceVerifyURL || waiting.UserCode != deviceUserCode || waiting.GrokDiagnostic != nil {
		t.Fatalf("waiting progress is wrong: %+v", waiting)
	}
	if g.idp.DeviceRequests() != 1 || g.idp.DevicePolls() != 0 {
		t.Fatal("the device code was requested more than once or polled before the interval")
	}
	form := g.idp.LastDeviceForm()
	if form.Get("client_id") != subscription.GrokClientID || form.Get("scope") != subscription.GrokScope() {
		t.Fatal("device request does not reuse the browser client and scopes")
	}
	close(release)
	g.awaitDone(t, run)
	p := g.progress(run)
	_, a = g.record()
	st := a.Subscription
	if p.State != stateSucceeded || p.Generation != string(st.Generation) || p.SuggestedName != "fixture@example.invalid" || p.Url != "" || p.UserCode != "" || p.GrokDiagnostic != nil {
		t.Fatalf("success progress is wrong: %+v", p)
	}
	if a.Connection == nil || a.Health != domain.AccountReady || st.Pending != nil || st.Lease != nil || st.ServerOperation.NativeStarted || st.Generation != st.ServerOperation.FinishID || !st.ServerOperation.CodeConsumed || st.ServerOperation.LoginMethod != domain.SubscriptionDeviceCode {
		t.Fatal("account document does not carry the operation and generation")
	}
	if !claimedAtFirstPoll {
		t.Fatal("the exchange was not claimed durably before the first poll was sent")
	}
	// Three pending answers and the success: one bundle, every poll one interval
	// after the previous one, the first one interval after publication.
	times := g.rec.Times()
	if len(times) != 4 || g.idp.DevicePolls() != 4 || g.idp.UserInfoRequests() != 1 || g.refs(t) != 1 {
		t.Fatalf("polls %d, bundles %d", len(times), g.refs(t))
	}
	if times[0].Sub(g.clock.start) != 5*time.Second {
		t.Fatal("the first poll was not one interval after the authorization")
	}
	for _, gap := range gaps(times) {
		if gap != 5*time.Second {
			t.Fatalf("poll gap %v is not the interval", gap)
		}
	}
	stored, err := g.secrets.Get(context.Background(), credentials.Ref{Owner: g.input.AccountID, ID: st.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.OpenGrokBundle(stored, g.service.Identity.ServerID, g.input.AccountID, st.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Identity().UserID != "fake-user-1" || st.IdentityCommitment != g.service.grokIdentityCommitment(bundle) {
		t.Fatal("bundle identity or commitment is wrong")
	}
	g.requireNoDeviceLeak(t, run)
}

func TestGrokDeviceLoginSlowDownAddsFiveSecondsToEveryLaterPoll(t *testing.T) {
	g := newGrokDeviceFixture(t)
	g.idp.DeviceScript = []string{"slow_down", "authorization_pending"}
	run := g.beginDevice(t)
	g.awaitDone(t, run)
	if p := g.progress(run); p.State != stateSucceeded {
		t.Fatalf("state %v", p.State)
	}
	if got := g.clock.Waits(); len(got) != 3 || got[0] != 5*time.Second || got[1] != 10*time.Second || got[2] != 10*time.Second {
		t.Fatalf("waits %v: slow_down must add 5s to this and every later wait", got)
	}
	times := g.rec.Times()
	if len(times) != 3 || times[0].Sub(g.clock.start) != 5*time.Second {
		t.Fatalf("polls %d", len(times))
	}
	for _, gap := range gaps(times) {
		if gap < 10*time.Second {
			t.Fatalf("a poll followed slow_down after only %v", gap)
		}
	}
}

func TestGrokDeviceLoginTerminalStatesAreDistinctAndKeepNoBundle(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*grokidp.IdP)
		state    pb.SubscriptionLoginState
		code     string
		polls    int
		claimed  bool
		diagnose bool
	}{
		{"denied", func(i *grokidp.IdP) { i.DeviceScript = []string{"authorization_pending", "access_denied"} }, stateFailed, string(domain.PermissionDenied), 2, true, true},
		{"provider expiry", func(i *grokidp.IdP) { i.DeviceScript = []string{"expired_token"} }, stateExpired, "", 1, true, false},
		{"provider rejection", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 400, `{"error":"invalid_grant"}` }, stateFailed, string(domain.PermissionDenied), 1, true, true},
		{"device endpoint unavailable", func(i *grokidp.IdP) { i.DeviceStatus, i.DeviceBody = 503, "down" }, stateFailed, string(domain.Unavailable), 0, false, true},
		{"unsafe verification host", func(i *grokidp.IdP) { i.DeviceVerification = "https://evil.example/d" }, stateFailed, string(domain.Unavailable), 0, false, true},
		{"id token rejected", func(i *grokidp.IdP) { i.SignWithWrongKey = true }, stateFailed, string(domain.Unauthenticated), 1, true, true},
		{"userinfo fails", func(i *grokidp.IdP) { i.UserInfoStatus = 500 }, stateFailed, string(domain.Unavailable), 1, true, true},
	}
	seen := map[pb.SubscriptionLoginState]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrokDeviceFixture(t)
			tc.setup(g.idp)
			run := g.startDevice(t)
			p, a := g.settle(t, run, tc.state)
			seen[p.State] = true
			if g.idp.DevicePolls() != tc.polls && tc.name != "id token rejected" && tc.name != "userinfo fails" {
				t.Fatalf("polls %d, want %d", g.idp.DevicePolls(), tc.polls)
			}
			if (p.GrokDiagnostic != nil) != tc.diagnose || tc.diagnose && (p.GrokDiagnostic.Code != tc.code || p.GrokDiagnostic.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_LOGIN) {
				t.Fatalf("diagnostic %+v", p.GrokDiagnostic)
			}
			if a.Subscription.ServerOperation.CodeConsumed != tc.claimed || a.Subscription.Pending != nil || a.Health != domain.AccountDisconnected {
				t.Fatal("claim or settlement state is wrong")
			}
			g.requireNoBundle(t, a)
			g.requireNoDeviceLeak(t, run)
		})
	}
	if len(seen) != 2 {
		t.Fatalf("denied, rejected and expired must map to distinct failed and expired states: %v", seen)
	}
}

func TestGrokDeviceLoginLocalDeadlineIsTheMinimumOfExpiresInAndTheOperationWindow(t *testing.T) {
	t.Run("expires_in shorter than the window", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceExpiresIn = 12
		g.idp.DeviceScript = pendingScript(20)
		run := g.startDevice(t)
		_, a := g.settle(t, run, stateExpired)
		// Polls at 5s and 10s; the remaining 2s is shorter than the interval, so
		// the loop waits it out and expires without a third poll.
		if g.idp.DevicePolls() != 2 || g.clock.Elapsed() != 12*time.Second {
			t.Fatalf("polls %d elapsed %v", g.idp.DevicePolls(), g.clock.Elapsed())
		}
		for _, d := range g.clock.Waits() {
			if d > 5*time.Second {
				t.Fatalf("waited %v, more than the interval", d)
			}
		}
		g.requireNoBundle(t, a)
	})
	t.Run("operation window shorter than expires_in", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceExpiresIn, g.idp.DeviceInterval = 3600, 60
		g.idp.DeviceScript = pendingScript(100)
		run := g.startDevice(t)
		_, a := g.settle(t, run, stateExpired)
		if g.clock.Elapsed() > 15*time.Minute || g.clock.Elapsed() < 14*time.Minute || g.idp.DevicePolls() > 15 {
			t.Fatalf("elapsed %v polls %d: the 15-minute window did not cap the device code", g.clock.Elapsed(), g.idp.DevicePolls())
		}
		g.requireNoBundle(t, a)
	})
}

func pendingScript(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "authorization_pending"
	}
	return out
}

func TestGrokDeviceLoginCancelAtEachPhaseHasADistinctOutcome(t *testing.T) {
	t.Run("during the device authorization request", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceGate, g.idp.DeviceEntered = make(chan struct{}), make(chan struct{}, 1)
		run := g.startDevice(t)
		awaitServerFixture(t, g.idp.DeviceEntered)
		g.cancelLogin(t)
		_, a := g.settle(t, run, stateCanceled)
		if g.idp.DevicePolls() != 0 || a.Subscription.ServerOperation.CodeConsumed {
			t.Fatal("cancel before any poll must not claim or poll")
		}
		g.requireNoBundle(t, a)
	})
	t.Run("while waiting for the first poll", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(1, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		g.cancelLogin(t)
		_, a := g.settle(t, run, stateCanceled)
		if g.idp.DevicePolls() != 0 || a.Subscription.ServerOperation.CodeConsumed {
			t.Fatal("cancel before the claim must leave the exchange unclaimed")
		}
		g.requireNoBundle(t, a)
	})
	t.Run("between polls after the claim", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceScript = pendingScript(5)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(2, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		g.cancelLogin(t)
		_, a := g.settle(t, run, stateCanceled)
		if g.idp.DevicePolls() != 1 || !a.Subscription.ServerOperation.CodeConsumed {
			t.Fatal("cancel between polls must keep the claim and send no further poll")
		}
		g.requireNoBundle(t, a)
	})
	t.Run("during a poll request", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.TokenGate, g.idp.TokenEntered = make(chan struct{}), make(chan struct{}, 1)
		run := g.startDevice(t)
		awaitServerFixture(t, g.idp.TokenEntered)
		g.cancelLogin(t)
		p, a := g.settle(t, run, stateRecovery)
		if g.idp.DevicePolls() != 1 || !a.Subscription.RecoveryRequired || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
			t.Fatal("a canceled in-flight poll must stay uncertain")
		}
		g.requireNoBundle(t, a)
	})
}

func TestGrokDeviceLoginUncertainPollRequiresRecoveryAndIsNeverResent(t *testing.T) {
	for name, setup := range map[string]func(*grokidp.IdP){
		"server error":      func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, "unavailable" },
		"rate limited":      func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 429, "slow" },
		"truncated success": func(i *grokidp.IdP) { i.TruncateTokenBody = true },
		"malformed success": func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` },
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokDeviceFixture(t)
			setup(g.idp)
			run := g.startDevice(t)
			p, a := g.settle(t, run, stateRecovery)
			if g.idp.DevicePolls() != 1 || !a.Subscription.RecoveryRequired || !a.Subscription.ServerOperation.CodeConsumed || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
				t.Fatalf("polls %d", g.idp.DevicePolls())
			}
			g.requireNoBundle(t, a)
			// Restarting the scanner must not poll or request a device code again.
			g.service.runServerSubscription(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), g.input.AccountID)
			if g.idp.DevicePolls() != 1 || g.idp.DeviceRequests() != 1 {
				t.Fatal("recovery resent the device code")
			}
			if g.service.recoverGrokLogin(context.Background(), g.input.AccountID, domain.ID(run.op.OperationId)) == nil {
				t.Fatal("recovery released a claimed exchange")
			}
			g.requireNoDeviceLeak(t, run)
		})
	}
}

func TestGrokDeviceLoginRestartRecoversWithoutResendingTheDeviceCode(t *testing.T) {
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	interrupted := func(g *grokDeviceFixture, consumed bool) *pb.RequestSubscriptionResponse {
		op, err := g.requestDevice(t, string(domain.NewID()))
		if err != nil {
			t.Fatal(err)
		}
		g.mutateServerOperation(func(a *domain.Account) {
			o := a.Subscription.ServerOperation
			o.State, o.NativeStarted, o.LoginMethod = domain.SubscriptionWaiting, true, domain.SubscriptionDeviceCode
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
		g := newGrokDeviceFixture(t)
		op := interrupted(g, true)
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || grokLoginNeedsRecovery(a) || g.progressFor(op.OperationId).State != stateRecovery {
			t.Fatal("a claimed device poll was not retained as uncertain")
		}
		if g.service.recoverGrokLogin(ctx, g.input.AccountID, domain.ID(op.OperationId)) == nil {
			t.Fatal("recovery released a claimed exchange")
		}
		g.service.runServerSubscription(ctx, g.input.AccountID)
		if g.idp.DeviceRequests() != 0 || g.idp.DevicePolls() != 0 || g.idp.TokenRequests() != 0 {
			t.Fatal("restart contacted the provider")
		}
	})
	t.Run("unclaimed", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		op := interrupted(g, false)
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || !grokLoginNeedsRecovery(a) {
			t.Fatal("an unclaimed interrupted device login is not recoverable")
		}
		if err := g.service.recoverGrokLogin(ctx, g.input.AccountID, domain.ID(op.OperationId)); err != nil {
			t.Fatal(err)
		}
		_, a = g.record()
		if a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || g.progressFor(op.OperationId).State != stateFailed || g.idp.DeviceRequests() != 0 || g.idp.TokenRequests() != 0 {
			t.Fatal("unclaimed device login was not freed without contacting the provider")
		}
		g.requireNoBundle(t, a)
	})
	t.Run("shutdown during a poll", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.TokenGate, g.idp.TokenEntered = make(chan struct{}), make(chan struct{}, 1)
		run := g.startDevice(t)
		awaitServerFixture(t, g.idp.TokenEntered)
		run.cancel()
		g.awaitDone(t, run)
		g.service.subscriptionEpoch = domain.NewID()
		if err := g.service.initializeServerSubscriptions(ctx); err != nil {
			t.Fatal(err)
		}
		_, a := g.record()
		if !a.Subscription.RecoveryRequired || g.progress(run).State != stateRecovery || !a.Subscription.ServerOperation.CodeConsumed {
			t.Fatal("shutdown during an in-flight poll did not leave recovery")
		}
		g.service.runServerSubscription(ctx, g.input.AccountID)
		if g.idp.DevicePolls() != 1 || g.idp.DeviceRequests() != 1 {
			t.Fatal("restart resent the device code")
		}
		g.requireNoBundle(t, a)
	})
}

func TestGrokDeviceRequestReplayReturnsTheOriginalOperationAndNeverSwitchesMethods(t *testing.T) {
	g := newGrokDeviceFixture(t)
	requestID := string(domain.NewID())
	first, err := g.requestDevice(t, requestID)
	if err != nil {
		t.Fatal(err)
	}
	// The same mutation id and request returns the original operation.
	r, _ := g.record()
	again, err := g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: requestID, Id: string(g.input.AccountID), ExpectedRevision: r.Revision - 1}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Msg.Replayed || again.Msg.OperationId != first.OperationId {
		t.Fatalf("replay: %+v", again.Msg)
	}
	// The same mutation id with the other method is a conflict, never a switch.
	_, err = g.client.RequestSubscription(context.Background(), subscriptionRequest(g.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: requestID, Id: string(g.input.AccountID), ExpectedRevision: r.Revision - 1}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err == nil {
		t.Fatal("the same mutation id with the browser method was accepted")
	}
	_, a := g.record()
	if !a.Subscription.Pending.DeviceCode || a.Subscription.ServerOperation.ID != domain.ID(first.OperationId) {
		t.Fatal("the original device operation changed")
	}
	// A second device login while one is pending is refused.
	if _, err := g.requestDevice(t, string(domain.NewID())); err == nil {
		t.Fatal("a second login started while one was pending")
	}
	g.holdFirstWait()
	run := g.launch(t, first)
	waitUntil(t, "the device URL", func() bool { return g.progress(run).Url != "" })
	g.cancelLogin(t)
	g.awaitDone(t, run)
}

func TestGrokDeviceLoginNeverUsesTheBrowserSurface(t *testing.T) {
	g := newGrokDeviceFixture(t)
	g.holdFirstWait()
	run := g.beginDevice(t)
	p := g.progress(run)
	if !strings.HasPrefix(p.Url, "https://accounts.x.ai/") || strings.Contains(p.Url, "oauth2/authorize") {
		t.Fatalf("device login published a browser authorization URL")
	}
	account, operation := string(g.input.AccountID), run.op.OperationId
	if g.forward(account, operation, "code=x&state=y") == nil {
		t.Fatal("a forwarded callback was accepted for a device login")
	}
	g.cancelLogin(t)
	g.awaitDone(t, run)
	// The browser method never touches the device endpoint.
	browser := newGrokDeviceFixture(t)
	b := browser.begin(t)
	grant := browser.idp.Approve(t, b.url)
	if status, err := callback(b.port, http.MethodGet, b.good(grant.Code), ""); err == nil && status != http.StatusOK {
		t.Fatalf("callback: %d %v", status, err)
	}
	browser.awaitDone(t, b)
	if browser.idp.DeviceRequests() != 0 || browser.idp.DevicePolls() != 0 || len(browser.clock.Waits()) != 0 {
		t.Fatal("the browser login used the device grant")
	}
}

func TestGrokDeviceAndBrowserLoginsProduceIdenticalBundles(t *testing.T) {
	normalized := func(g *grokDeviceFixture) (subscription.GrokIdentity, map[string]any) {
		_, a := g.record()
		st := a.Subscription
		stored, err := g.secrets.Get(context.Background(), credentials.Ref{Owner: g.input.AccountID, ID: st.Generation, Purpose: credentials.AccountLogin})
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := subscription.OpenGrokBundle(stored, g.service.Identity.ServerID, g.input.AccountID, st.Generation)
		if err != nil {
			t.Fatal(err)
		}
		file, err := bundle.AuthFile()
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]map[string]any
		if err := json.Unmarshal(file, &doc); err != nil {
			t.Fatal(err)
		}
		entry := doc[subscription.GrokScopeKey]
		// The exchange time is the only value that legitimately differs.
		delete(entry, "create_time")
		delete(entry, "expires_at")
		return bundle.Identity(), entry
	}
	device := newGrokDeviceFixture(t)
	device.idp.PrincipalType, device.idp.PrincipalID, device.idp.TeamID = "team-member", "principal-77", "team-9"
	run := device.beginDevice(t)
	device.awaitDone(t, run)
	browser := newGrokDeviceFixture(t)
	browser.idp.PrincipalType, browser.idp.PrincipalID, browser.idp.TeamID = "team-member", "principal-77", "team-9"
	b, _ := browser.loginAs(t)
	_ = b
	deviceIdentity, deviceEntry := normalized(device)
	browserIdentity, browserEntry := normalized(browser)
	left, _ := json.Marshal(deviceEntry)
	right, _ := json.Marshal(browserEntry)
	if string(left) != string(right) || deviceIdentity.UserID != browserIdentity.UserID || deviceIdentity.PrincipalType != browserIdentity.PrincipalType || deviceIdentity.PrincipalID != browserIdentity.PrincipalID {
		t.Fatal("device and browser login produced different bundles for the same token response")
	}
}

func TestValidServerLoginProgressIsServiceAware(t *testing.T) {
	cases := []struct {
		name    string
		service domain.SubscriptionService
		url     string
		code    string
		want    bool
	}{
		{"grok device", domain.SubscriptionGrok, deviceVerifyURL, "ABCD-1234", true},
		{"grok plain url", domain.SubscriptionGrok, "https://accounts.x.ai/oauth2/device", "WXYZ", true},
		{"grok openai host", domain.SubscriptionGrok, "https://auth.openai.com/codex/device", "ABCD-1234", false},
		{"grok auth host", domain.SubscriptionGrok, "https://auth.x.ai/oauth2/device", "ABCD-1234", false},
		{"grok http", domain.SubscriptionGrok, "http://accounts.x.ai/oauth2/device", "ABCD-1234", false},
		{"grok credentials", domain.SubscriptionGrok, "https://u@accounts.x.ai/oauth2/device", "ABCD-1234", false},
		{"grok fragment", domain.SubscriptionGrok, "https://accounts.x.ai/oauth2/device#x", "ABCD-1234", false},
		{"grok lowercase code", domain.SubscriptionGrok, deviceVerifyURL, "abcd-1234", false},
		{"grok no code", domain.SubscriptionGrok, deviceVerifyURL, "", false},
		{"chatgpt device", domain.SubscriptionChatGPT, "https://auth.openai.com/codex/device", "ABCD-1234", true},
		{"chatgpt rejects xai", domain.SubscriptionChatGPT, deviceVerifyURL, "ABCD-1234", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validServerLoginProgress(tc.service, tc.url, tc.code); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Server shutdown is not a user cancellation. The device code lives only in
// memory, so after the exchange claim a shutdown between polls leaves the same
// durable state as a crash: recovery-required, with the claim retained. Before
// the claim nothing was sent, so shutdown stays canceled.
func TestGrokDeviceLoginShutdownBetweenPollsKeepsAClaimInRecovery(t *testing.T) {
	t.Run("after the claim", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		g.idp.DeviceScript = pendingScript(5)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(2, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		run.cancel()
		p, a := g.settle(t, run, stateRecovery)
		st := a.Subscription
		if g.idp.DevicePolls() != 1 || !st.RecoveryRequired || st.Pending == nil || !st.ServerOperation.CodeConsumed || st.ServerOperation.State != domain.SubscriptionRecovery || p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
			t.Fatal("a shutdown between polls after the claim did not keep the login in recovery")
		}
		g.requireNoBundle(t, a)
		// The claimed device code is never resent or released automatically.
		if grokLoginNeedsRecovery(a) || g.service.recoverGrokLogin(ownerContext(), g.input.AccountID, st.ServerOperation.ID) == nil || g.idp.DevicePolls() != 1 {
			t.Fatal("recovery released or resent a claimed device login")
		}
	})
	t.Run("before the claim", func(t *testing.T) {
		g := newGrokDeviceFixture(t)
		entered, release := make(chan struct{}), make(chan struct{})
		g.clock.blockAt(1, entered, release)
		run := g.startDevice(t)
		awaitServerFixture(t, entered)
		run.cancel()
		_, a := g.settle(t, run, stateCanceled)
		if g.idp.DevicePolls() != 0 || a.Subscription.ServerOperation.CodeConsumed || a.Subscription.RecoveryRequired {
			t.Fatal("a shutdown before the claim must settle as canceled without a fence")
		}
		g.requireNoBundle(t, a)
	})
}

// The tokens of a claimed grant are already issued when they are verified. An
// interruption of that verification (cancel, shutdown or the window ending) is
// uncertain and stays fenced; a verification that fails on its own is an ordinary
// failure that stores nothing.
func TestGrokVerificationInterruptedAfterTheTokensAreIssuedStaysInRecovery(t *testing.T) {
	g := newGrokDeviceFixture(t)
	client := subscription.GrokOAuthClient{Transport: g.idp.Transport}
	endpoints, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Clear()
	tokens, result, err := client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
	if err != nil || result != subscription.GrokDeviceTokens {
		t.Fatalf("poll = %v, %v", result, err)
	}
	original := domain.ServerSubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, device := range []bool{true, false} {
		outcome := g.service.grokBundleOutcome(canceled, client, endpoints, tokens, "", device, original)
		if outcome.state != domain.SubscriptionRecovery || outcome.code != domain.RecoveryRequired || !outcome.claimed {
			t.Fatalf("an interrupted verification (device=%v) settled as %v", device, outcome.state)
		}
	}
	// A verification that fails on its own, with a live context, stays an ordinary
	// failure.
	g.idp.UserInfoStatus = http.StatusInternalServerError
	if outcome := g.service.grokBundleOutcome(context.Background(), client, endpoints, tokens, "", true, original); outcome.state != domain.SubscriptionFailed || !outcome.claimed {
		t.Fatalf("a userinfo failure settled as %v", outcome.state)
	}
}

// A cancellation that lands after the claimed exchange succeeded is too late: the
// tokens are issued, so the login publishes instead of settling as canceled with
// its credentials discarded and the redeemed grant forgotten.
func TestGrokCancelRacingTheSettlementOfASuccessfulClaimedLoginIsTooLate(t *testing.T) {
	g := newGrokDeviceFixture(t)
	g.service.grokFaultHook = func(p grokFaultPoint) error {
		if p == grokFaultBeforeLoginFinish {
			g.cancelLogin(t)
		}
		return nil
	}
	run := g.startDevice(t)
	g.awaitDone(t, run)
	g.service.grokFaultHook = nil
	_, a := g.record()
	if a.Connection == nil || a.Subscription.Generation == "" || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("a cancellation after the successful exchange discarded the issued credentials")
	}
	if g.idp.DevicePolls() != 1 {
		t.Fatalf("the device code was polled %d times", g.idp.DevicePolls())
	}
}

func TestGrokLateCancelOutcomeTable(t *testing.T) {
	for name, c := range map[string]struct {
		claimed, success, canceled, gone bool
		result, want                     domain.SubscriptionLoginState
	}{
		"unclaimed cancel":              {false, false, true, false, domain.SubscriptionFailed, domain.SubscriptionCanceled},
		"claimed failure cancel":        {true, false, true, false, domain.SubscriptionFailed, domain.SubscriptionCanceled},
		"claimed success cancel":        {true, true, true, false, domain.SubscriptionSucceeded, domain.SubscriptionSucceeded},
		"claimed success vanished":      {true, true, false, true, domain.SubscriptionSucceeded, domain.SubscriptionRecovery},
		"claimed success cancel vanish": {true, true, true, true, domain.SubscriptionSucceeded, domain.SubscriptionRecovery},
		"uncertain cancel":              {true, false, true, false, domain.SubscriptionRecovery, domain.SubscriptionRecovery},
		"no cancel":                     {true, true, false, false, domain.SubscriptionSucceeded, domain.SubscriptionSucceeded},
	} {
		t.Run(name, func(t *testing.T) {
			if _, got, _ := grokLateCancelOutcome(c.claimed, c.success, c.result, domain.Canceled, c.canceled, c.gone); got != c.want {
				t.Fatalf("settled as %v, want %v", got, c.want)
			}
		})
	}
}
