// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	grokDeviceUserCode   = "ABCD-1234"
	grokDeviceVerifyPage = "https://accounts.x.ai/oauth2/device"
)

// grokDeviceRun drives one server-owned Grok device-code login against the fake
// authorization server with a virtual clock.
type grokDeviceRun struct {
	*grokLoginRun
	clock *grokidptest.Clock
}

func newGrokDeviceRun(t *testing.T) *grokDeviceRun {
	t.Helper()
	f := newGrokFixture(t)
	idp := grokidptest.New()
	clock := grokidptest.NewClock(time.Now())
	idp.Clock, idp.DeviceInterval = clock, 5
	f.service.grokLoginTransport = idp.Transport()
	f.service.grokDeviceClock = clock
	logs := f.captureLogs()
	response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
	if err != nil {
		t.Fatal(err)
	}
	return &grokDeviceRun{grokLoginRun: &grokLoginRun{t: t, f: f, idp: idp, logs: logs, op: response.OperationId}, clock: clock}
}

// holdWait blocks the poll loop at its n-th wait until release is closed or the
// login context ends.
func (g *grokDeviceRun) holdWait(n int) (entered <-chan struct{}, release func()) {
	in, out := make(chan struct{}), make(chan struct{})
	g.clock.Gate = func(ctx context.Context, wait int, _ time.Duration) error {
		if wait != n {
			return nil
		}
		close(in)
		select {
		case <-out:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return in, func() { close(out) }
}

func (g *grokDeviceRun) waitEntered(entered <-chan struct{}) {
	g.t.Helper()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		g.t.Fatal("the login never reached the expected point")
	}
}

func (g *grokDeviceRun) cancelRPC() {
	g.t.Helper()
	r, _ := g.f.record()
	if _, err := g.f.client.CancelSubscription(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}})); err != nil {
		g.t.Fatal(err)
	}
}

func (g *grokDeviceRun) noDeviceLeak() {
	g.t.Helper()
	g.noLeak(grokDeviceUserCode, "fake-device-code", "oauth2/device", "accounts.x.ai")
}

// requireNothingStored asserts that no credential, connection or claim remains.
func (g *grokDeviceRun) requireNothingStored(claimed bool) domain.Account {
	g.t.Helper()
	a := g.account()
	if a.Connection != nil || a.Subscription.Generation != "" || !g.vaultEmpty() || a.Subscription.ServerOperation.ExchangeClaimed != claimed {
		g.t.Fatalf("stored state is wrong: connected = %v, claimed = %v, vault empty = %v", a.Connection != nil, a.Subscription.ServerOperation.ExchangeClaimed, g.vaultEmpty())
	}
	return a
}

func TestGrokDeviceLoginStoresABundleAfterThreePendingPolls(t *testing.T) {
	g := newGrokDeviceRun(t)
	_, queued := g.f.record()
	if st := queued.Subscription; st.ServerOperation == nil || st.ServerOperation.State != domain.SubscriptionPreparing || st.Pending == nil || !st.Pending.DeviceCode || st.Pending.MachineID != "" {
		t.Fatal("the device login was not accepted as a server-owned operation")
	}
	g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending}
	entered, release := g.holdWait(1)
	g.run()
	g.waitEntered(entered)
	waiting := g.waitState(grokWaiting)
	if waiting.Url != grokDeviceVerifyPage || waiting.UserCode != grokDeviceUserCode || waiting.GrokDiagnostic != nil || waiting.Generation != "" {
		t.Fatalf("waiting progress = %v", waiting)
	}
	if !subscription.ValidGrokDeviceVerification(waiting.Url, waiting.UserCode) {
		t.Fatal("the presentation is not the shape the desktop accepts")
	}
	form := g.idp.DeviceForms[0]
	if g.idp.DeviceRequests != 1 || form.Get("client_id") != subscription.GrokClientID || form.Get("scope") != strings.Join(subscription.GrokScopes, " ") {
		t.Fatalf("device request = %v", form)
	}
	if a := g.account(); a.Subscription.ServerOperation.ExchangeClaimed || len(g.idp.DevicePollForms) != 0 {
		t.Fatal("a claim or a poll existed before the first interval")
	}
	release()
	done := g.waitState(grokSucceeds)
	g.await()
	if done.Url != "" || done.UserCode != "" || done.GrokDiagnostic != nil {
		t.Fatalf("terminal progress still carries the presentation: %v", done)
	}
	if got := g.clock.Waits(); len(got) != 4 {
		t.Fatalf("waits = %v", got)
	} else {
		for _, wait := range got {
			if wait != 5*time.Second {
				t.Fatalf("waits = %v", got)
			}
		}
	}
	times := g.idp.DevicePollTimes
	if len(times) != 4 || g.idp.DeviceTokenSuccessCount != 1 {
		t.Fatalf("polls = %d, successes = %d", len(times), g.idp.DeviceTokenSuccessCount)
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) != 5*time.Second {
			t.Fatalf("poll %d was not one interval after the previous one", i+1)
		}
	}
	for _, form := range g.idp.DevicePollForms {
		if form.Get("grant_type") != subscription.GrokDeviceGrantType || form.Get("client_id") != subscription.GrokClientID || form.Get("device_code") == "" {
			t.Fatalf("poll form = %v", form)
		}
	}
	if g.idp.TokenAuthHeaders[0] != "" {
		t.Fatal("the device poll authenticated the public client")
	}
	a := g.account()
	st := a.Subscription
	if a.Connection == nil || a.Health != domain.AccountReady || st.Pending != nil || st.RecoveryRequired || st.ServerOperation.NativeStarted || !st.ServerOperation.ExchangeClaimed || st.Generation == "" || st.Generation != st.ServerOperation.FinishID || done.Generation != string(st.Generation) {
		t.Fatal("the device login did not publish the generation")
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
	g.noDeviceLeak()
}

func TestGrokDeviceLoginSlowDownLengthensTheNextWait(t *testing.T) {
	g := newGrokDeviceRun(t)
	g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DeviceSlowDown, grokidptest.DevicePending}
	g.run()
	g.waitState(grokSucceeds)
	g.await()
	want := []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second}
	got := g.clock.Waits()
	if len(got) != len(want) {
		t.Fatalf("waits = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
	times := g.idp.DevicePollTimes
	if times[1].Sub(times[0]) != 10*time.Second || times[2].Sub(times[1]) != 10*time.Second {
		t.Fatal("a poll came sooner than the lengthened interval")
	}
	g.noDeviceLeak()
}

func TestGrokDeviceLoginTerminalStatesAreDistinct(t *testing.T) {
	type terminal struct {
		name    string
		prepare func(*grokDeviceRun)
		state   pb.SubscriptionLoginState
		code    domain.Code
		polls   int
	}
	for _, test := range []terminal{
		{name: "denied", prepare: func(g *grokDeviceRun) {
			g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DeviceDenied}
		}, state: grokFailedSt, code: domain.PermissionDenied, polls: 2},
		{name: "expired token", prepare: func(g *grokDeviceRun) {
			g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DeviceExpired}
		}, state: grokExpired, polls: 2},
		{name: "device lifetime", prepare: func(g *grokDeviceRun) {
			g.idp.DeviceExpiresIn = 12
			g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending}
		}, state: grokExpired, polls: 2},
		{name: "operation expiry caps the lifetime", prepare: func(g *grokDeviceRun) {
			g.idp.DeviceInterval, g.idp.DeviceExpiresIn = 300, 24*3600
			g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending}
		}, state: grokExpired, polls: 3},
		{name: "unknown rejection", prepare: func(g *grokDeviceRun) {
			g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DeviceUnknown}
		}, state: grokFailedSt, code: domain.Unauthenticated, polls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newGrokDeviceRun(t)
			test.prepare(g)
			g.run()
			p := g.waitState(test.state)
			g.await()
			if test.state == grokFailedSt {
				requireGrokProgressOnly(t, p, test.state, test.code, g.op)
			} else if p.GrokDiagnostic != nil || p.Canceled || p.Url != "" || p.UserCode != "" {
				t.Fatalf("progress = %v", p)
			}
			a := g.requireNothingStored(false)
			if a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || len(g.idp.DevicePollTimes) != test.polls || g.idp.DeviceTokenSuccessCount != 0 {
				t.Fatalf("pending = %v, recovery = %v, polls = %d", a.Subscription.Pending != nil, a.Subscription.RecoveryRequired, len(g.idp.DevicePollTimes))
			}
			g.noDeviceLeak()
		})
	}
}

func TestGrokDeviceLoginOperationExpiryDoesNotPollPastTheDeadline(t *testing.T) {
	g := newGrokDeviceRun(t)
	g.idp.DeviceInterval, g.idp.DeviceExpiresIn = 300, 24*3600
	g.idp.DeviceScript = make([]grokidptest.DeviceStep, 8)
	for i := range g.idp.DeviceScript {
		g.idp.DeviceScript[i] = grokidptest.DevicePending
	}
	g.run()
	g.waitState(grokExpired)
	g.await()
	_, a := g.f.record()
	deadline := a.Subscription.ServerOperation.ExpiresAt
	if g.clock.Now().After(deadline.Add(time.Second)) {
		t.Fatalf("the clock advanced past the operation expiry: %v", g.clock.Waits())
	}
}

func TestGrokDeviceLoginCancellationIsDistinctAtEveryPhase(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.cancelRPC()
		if p := g.progress(); p.State != grokCanceled || !p.Canceled {
			t.Fatalf("progress = %v", p)
		}
		a := g.account()
		if a.Subscription.Pending != nil || grokLoginCandidate(a) || g.idp.DeviceRequests != 0 {
			t.Fatal("a canceled queued device login is still claimable or reached the provider")
		}
	})
	t.Run("during the device authorization request", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		entered, hold := make(chan struct{}), make(chan struct{})
		g.idp.BeforeDeviceAuthorization = func() { close(entered); <-hold }
		g.run()
		g.waitEntered(entered)
		g.cancelRPC()
		p := g.waitState(grokCanceled)
		g.await()
		close(hold)
		if !p.Canceled || p.Url != "" || p.UserCode != "" || p.GrokDiagnostic != nil {
			t.Fatalf("progress = %v", p)
		}
		g.requireNothingStored(false)
		if len(g.idp.DevicePollForms) != 0 {
			t.Fatal("a token request was sent")
		}
		g.noDeviceLeak()
	})
	t.Run("between polls", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending}
		entered, _ := g.holdWait(2)
		g.run()
		g.waitEntered(entered)
		if a := g.account(); a.Subscription.ServerOperation.ExchangeClaimed {
			t.Fatal("a claim stood between two polls")
		}
		g.cancelRPC()
		p := g.waitState(grokCanceled)
		g.await()
		if !p.Canceled || p.Url != "" || p.UserCode != "" || p.GrokDiagnostic != nil {
			t.Fatalf("progress = %v", p)
		}
		a := g.requireNothingStored(false)
		if a.Subscription.Pending != nil || a.Subscription.RecoveryRequired || len(g.idp.DevicePollForms) != 1 {
			t.Fatalf("polls = %d", len(g.idp.DevicePollForms))
		}
		g.noDeviceLeak()
	})
	t.Run("while a token request is in flight", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending}
		entered, release := make(chan struct{}), make(chan struct{})
		g.idp.BeforeDevicePoll = func(poll int) {
			if poll == 2 {
				close(entered)
				<-release
			}
		}
		g.run()
		g.waitEntered(entered)
		if a := g.account(); !a.Subscription.ServerOperation.ExchangeClaimed {
			t.Fatal("the claim was not durable before the token request")
		}
		g.cancelRPC()
		p := g.waitState(grokRecovery)
		g.await()
		close(release)
		if p.GrokDiagnostic == nil || p.GrokDiagnostic.Code != string(domain.RecoveryRequired) {
			t.Fatalf("progress = %v", p)
		}
		a := g.requireNothingStored(true)
		if !a.Subscription.RecoveryRequired || len(g.idp.DevicePollForms) != 2 {
			t.Fatal("a cancel with an unanswered request must be recovery with no further request")
		}
		g.noDeviceLeak()
	})
}

func TestGrokDeviceLoginRestartNeverResendsAnExchangedCode(t *testing.T) {
	t.Run("shutdown while a token request is in flight", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending}
		entered, release := make(chan struct{}), make(chan struct{})
		g.idp.BeforeDevicePoll = func(poll int) {
			if poll == 2 {
				close(entered)
				<-release
			}
		}
		g.run()
		g.waitEntered(entered)
		g.cancel()
		g.await()
		close(release)
		if p := g.progress(); p.State != grokRecovery || p.Url != "" || p.UserCode != "" {
			t.Fatalf("progress after shutdown = %v", p)
		}
		a := g.requireNothingStored(true)
		if !a.Subscription.RecoveryRequired {
			t.Fatal("shutdown after the claim must be recovery")
		}
		var binds atomic.Int32
		g.f.service.grokListen = func() (net.Listener, error) { binds.Add(1); return net.Listen("tcp4", "127.0.0.1:0") }
		g.f.service.subscriptionEpoch = domain.NewID()
		if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 700*time.Millisecond)
		defer stop()
		g.f.service.runServerSubscriptions(ctx)
		if binds.Load() != 0 || g.idp.DeviceRequests != 1 || len(g.idp.DevicePollForms) != 2 {
			t.Fatal("restart relaunched the login or resent the device code")
		}
		g.noDeviceLeak()
	})
	t.Run("restart while polling", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending}
		entered, _ := g.holdWait(2)
		g.run()
		g.waitEntered(entered)
		g.f.service.subscriptionEpoch = domain.NewID()
		if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
			t.Fatal(err)
		}
		p := g.progress()
		if p.State != grokRecovery || p.Url != "" || p.UserCode != "" {
			t.Fatalf("restart progress = %v", p)
		}
		g.cancel()
		g.await()
		if a := g.account(); !a.Subscription.RecoveryRequired || a.Connection != nil || len(g.idp.DevicePollForms) != 1 {
			t.Fatal("restart did not leave the operation in recovery with a single poll")
		}
		g.noDeviceLeak()
	})
	t.Run("queued across a restart", func(t *testing.T) {
		g := newGrokDeviceRun(t)
		g.f.service.subscriptionEpoch = domain.NewID()
		if err := g.f.service.initializeServerSubscriptions(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 700*time.Millisecond)
		defer stop()
		g.f.service.runServerSubscriptions(ctx)
		if p := g.progress(); p.State != grokRecovery || g.idp.DeviceRequests != 0 {
			t.Fatalf("progress = %v, device requests = %d", p, g.idp.DeviceRequests)
		}
	})
}

func TestGrokDeviceLoginFailuresNeverFallBackToTheBrowserFlow(t *testing.T) {
	for name, test := range map[string]struct {
		prepare func(*grokidptest.IdP)
		code    domain.Code
	}{
		"no device endpoint":   {func(i *grokidptest.IdP) { i.DeviceOmitEndpoint = true }, domain.Unavailable},
		"device request fails": {func(i *grokidptest.IdP) { i.DeviceStatus = 502 }, domain.Unavailable},
		"foreign verification": {func(i *grokidptest.IdP) { i.DeviceVerificationURI = "https://evil.example/oauth2/device" }, domain.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokDeviceRun(t)
			test.prepare(g.idp)
			var binds atomic.Int32
			g.f.service.grokListen = func() (net.Listener, error) { binds.Add(1); return net.Listen("tcp4", "127.0.0.1:0") }
			g.run()
			p := g.waitState(grokFailedSt)
			g.await()
			requireGrokProgressOnly(t, p, grokFailedSt, test.code, g.op)
			if binds.Load() != 0 || len(g.idp.DevicePollForms) != 0 || p.Url != "" || p.UserCode != "" {
				t.Fatal("the device login fell back to the browser flow or polled")
			}
			g.requireNothingStored(false)
		})
	}
}

func TestGrokDeviceLoginRejectsInvalidIDTokens(t *testing.T) {
	for name, prepare := range map[string]func(*grokidptest.IdP){
		"issuer":    func(i *grokidptest.IdP) { i.IDTokenIssuer = "https://evil.example" },
		"audience":  func(i *grokidptest.IdP) { i.IDTokenAudience = "another-client" },
		"signature": func(i *grokidptest.IdP) { i.SignWithOtherKey = true },
		"expired":   func(i *grokidptest.IdP) { i.IDTokenLifetime = -time.Minute },
		"principal": func(i *grokidptest.IdP) { i.PrincipalID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			g := newGrokDeviceRun(t)
			prepare(g.idp)
			g.run()
			p := g.waitState(grokFailedSt)
			g.await()
			requireGrokProgressOnly(t, p, grokFailedSt, domain.Unauthenticated, g.op)
			a := g.requireNothingStored(true)
			if a.Subscription.Pending != nil || g.idp.DeviceTokenSuccessCount != 1 || len(g.idp.DevicePollForms) != 1 {
				t.Fatal("a rejected ID token must not be exchanged again")
			}
			g.noDeviceLeak()
		})
	}
}

func TestGrokDeviceLoginReplayReturnsTheOriginalOperation(t *testing.T) {
	f := newGrokFixture(t)
	r, _ := f.record()
	request := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true}
	send := func() *pb.RequestSubscriptionResponse {
		response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg
	}
	first, second := send(), send()
	if first.Replayed || !second.Replayed || first.OperationId != second.OperationId || first.OperationId != request.Mutation.RequestId {
		t.Fatal("the replay did not return the original operation")
	}
	_, a := f.record()
	if a.Subscription.Pending == nil || !a.Subscription.Pending.DeviceCode || a.Subscription.Pending.ID != domain.ID(first.OperationId) {
		t.Fatal("the replay changed the pending operation")
	}
}

func TestGrokDeviceLoginRefusesCallbackForwarding(t *testing.T) {
	g := newGrokDeviceRun(t)
	entered, _ := g.holdWait(1)
	g.run()
	g.waitEntered(entered)
	g.waitState(grokWaiting)
	if _, err := g.f.client.ForwardSubscriptionCallback(context.Background(), subscriptionRequest(g.f.service.Identity.Token, &pb.ForwardSubscriptionCallbackRequest{AccountId: string(g.f.input.AccountID), OperationId: g.op, CallbackQuery: []byte("code=x&state=y")})); err == nil {
		t.Fatal("a callback was forwarded into a device login")
	}
	if len(g.idp.DevicePollForms) != 0 {
		t.Fatal("the refused callback reached the exchange")
	}
}

func TestGrokDeviceAndBrowserLoginsStoreTheSameIdentity(t *testing.T) {
	stored := func(g *grokLoginRun) subscription.GrokIdentity {
		a := g.account()
		raw, err := g.f.secrets.Get(context.Background(), credentials.Ref{Owner: g.f.input.AccountID, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin})
		if err != nil {
			g.t.Fatal(err)
		}
		bundle, err := subscription.OpenGrokBundle(raw, g.f.service.Identity.ServerID, g.f.input.AccountID, a.Subscription.Generation)
		if err != nil {
			g.t.Fatal(err)
		}
		return bundle.Identity()
	}
	browser := newGrokLogin(t)
	browser.waiting()
	browser.browse(nil)
	browser.waitState(grokSucceeds)
	browser.await()
	device := newGrokDeviceRun(t)
	device.run()
	device.waitState(grokSucceeds)
	device.await()
	a, b := stored(browser), stored(device.grokLoginRun)
	if a.UserID != b.UserID || a.PrincipalType != b.PrincipalType || a.PrincipalID != b.PrincipalID || a.TeamID != b.TeamID {
		t.Fatal("the two methods stored different identities for one provider identity")
	}
}
