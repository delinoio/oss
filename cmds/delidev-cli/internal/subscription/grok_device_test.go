// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

type grokDeviceFlow struct {
	idp       *grokidptest.IdP
	clock     *grokidptest.Clock
	profile   subscription.GrokOAuthProfile
	endpoints subscription.GrokEndpoints
	claims    int
	releases  int
	polls     []subscription.GrokDevicePoll
	claimErr  error
	releaseEr error
}

func newGrokDeviceFlow(t *testing.T, interval int) *grokDeviceFlow {
	t.Helper()
	f := &grokDeviceFlow{idp: grokidptest.New(), profile: subscription.DefaultGrokOAuthProfile()}
	f.clock = grokidptest.NewClock(time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC))
	f.idp.Clock = f.clock
	f.idp.DeviceInterval = interval
	var err error
	if f.endpoints, err = subscription.DiscoverGrokEndpoints(context.Background(), f.idp.Client(), f.profile); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *grokDeviceFlow) authorize(t *testing.T) *subscription.GrokDeviceAuthorization {
	t.Helper()
	a, err := subscription.RequestGrokDeviceAuthorization(context.Background(), f.idp.Client(), f.profile, f.endpoints)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Clear)
	return a
}

func (f *grokDeviceFlow) poll(a *subscription.GrokDeviceAuthorization, deadline time.Time) (subscription.GrokTokens, error) {
	return subscription.PollGrokDeviceToken(context.Background(), a, subscription.GrokDevicePolling{
		Client: f.idp.Client(), Profile: f.profile, Endpoints: f.endpoints, Clock: f.clock, Deadline: deadline,
		Claim:   func(context.Context) error { f.claims++; return f.claimErr },
		Release: func(context.Context) error { f.releases++; return f.releaseEr },
		Observe: func(p subscription.GrokDevicePoll) { f.polls = append(f.polls, p) },
	})
}

func (f *grokDeviceFlow) far() time.Time { return f.clock.Now().Add(15 * time.Minute) }

func TestGrokDeviceDiscoveryAdvertisesTheEndpointOnlyWithTheGrant(t *testing.T) {
	f := newGrokDeviceFlow(t, 0)
	if f.endpoints.DeviceAuthorization != "https://auth.x.ai/oauth2/device/code" {
		t.Fatalf("device endpoint = %q", f.endpoints.DeviceAuthorization)
	}
	for name, override := range map[string]string{
		"absent":   `"grant_types_supported":["authorization_code"]`,
		"foreign":  `"device_authorization_endpoint":"https://evil.example/device"`,
		"no-grant": `"grant_types_supported":["refresh_token"]`,
	} {
		t.Run(name, func(t *testing.T) {
			document := strings.TrimSuffix(discoveryJSON(override), "}")
			if name == "foreign" {
				document += `,"grant_types_supported":["` + subscription.GrokDeviceGrantType + `"]`
			} else if name != "absent" {
				document += `,"device_authorization_endpoint":"https://auth.x.ai/oauth2/device/code"`
			}
			document += "}"
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return recorded(func(w http.ResponseWriter) { fmt.Fprint(w, document) }), nil
			})}
			endpoints, err := subscription.DiscoverGrokEndpoints(context.Background(), client, f.profile)
			if err != nil || endpoints.Token == "" {
				t.Fatalf("the browser login must not depend on the device endpoint: %v", err)
			}
			if endpoints.DeviceAuthorization != "" {
				t.Fatalf("device endpoint %q accepted for %s", endpoints.DeviceAuthorization, name)
			}
		})
	}
}

func TestGrokDeviceAuthorizationUsesTheBrowserProfileAndStaysRedacted(t *testing.T) {
	f := newGrokDeviceFlow(t, 7)
	a := f.authorize(t)
	if f.idp.DeviceRequests != 1 {
		t.Fatalf("device requests = %d", f.idp.DeviceRequests)
	}
	form := f.idp.DeviceForms[0]
	if form.Get("client_id") != subscription.GrokClientID || form.Get("scope") != strings.Join(subscription.GrokScopes, " ") || len(form) != 2 {
		t.Fatalf("device request form = %v", form)
	}
	if a.UserCode != "ABCD-1234" || a.VerificationURL != "https://accounts.x.ai/oauth2/device" || a.Interval != 7*time.Second || a.ExpiresIn != 15*time.Minute {
		t.Fatalf("authorization = %#v", a.UserCode)
	}
	for _, text := range []string{fmt.Sprint(a), fmt.Sprintf("%v %+v %#v %s", a, *a, *a, a)} {
		if strings.Contains(text, "ABCD") || strings.Contains(text, "fake-device-code") || strings.Contains(text, "accounts.x.ai") {
			t.Fatal("a formatter leaked a device value")
		}
	}
	a.Clear()
	if a.UserCode != "" || a.VerificationURL != "" {
		t.Fatal("Clear kept presentation values")
	}
}

func TestGrokDeviceAuthorizationDefaultsAndCompleteURL(t *testing.T) {
	f := newGrokDeviceFlow(t, 0)
	f.idp.DeviceVerificationFull = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234"
	a := f.authorize(t)
	if a.Interval != subscription.GrokDeviceDefaultInterval || a.VerificationURL != f.idp.DeviceVerificationFull {
		t.Fatal("the RFC default interval or the complete address was not used")
	}
	// A complete address for another code is ignored; the plain page is used.
	f.idp.DeviceVerificationFull = "https://accounts.x.ai/oauth2/device?user_code=OTHER-1"
	if b := f.authorize(t); b.VerificationURL != "https://accounts.x.ai/oauth2/device" {
		t.Fatalf("address = %q", b.VerificationURL)
	}
}

func TestGrokDeviceAuthorizationRejectsUnusableAnswers(t *testing.T) {
	for name, edit := range map[string]func(*grokidptest.IdP){
		"server error":      func(i *grokidptest.IdP) { i.DeviceStatus = http.StatusBadGateway },
		"client error":      func(i *grokidptest.IdP) { i.DeviceStatus = http.StatusBadRequest },
		"foreign page":      func(i *grokidptest.IdP) { i.DeviceVerificationURI = "https://evil.example/oauth2/device" },
		"port":              func(i *grokidptest.IdP) { i.DeviceVerificationURI = "https://accounts.x.ai:8443/oauth2/device" },
		"query":             func(i *grokidptest.IdP) { i.DeviceVerificationURI = "https://accounts.x.ai/oauth2/device?next=1" },
		"lowercase code":    func(i *grokidptest.IdP) { i.DeviceUserCode = "abcd-1234" },
		"short code":        func(i *grokidptest.IdP) { i.DeviceUserCode = "AB" },
		"huge interval":     func(i *grokidptest.IdP) { i.DeviceInterval = 3600 },
		"huge lifetime":     func(i *grokidptest.IdP) { i.DeviceExpiresIn = 7 * 24 * 3600 },
		"space in the code": func(i *grokidptest.IdP) { i.DeviceCodeValue = "has space" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokDeviceFlow(t, 5)
			edit(f.idp)
			if _, err := subscription.RequestGrokDeviceAuthorization(context.Background(), f.idp.Client(), f.profile, f.endpoints); err == nil {
				t.Fatal("an unusable device authorization was accepted")
			} else if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonDeviceAuthorization || uncertain {
				t.Fatalf("reason = %q uncertain = %v", reason, uncertain)
			}
		})
	}
	f := newGrokDeviceFlow(t, 5)
	if _, err := subscription.RequestGrokDeviceAuthorization(context.Background(), f.idp.Client(), f.profile, subscription.GrokEndpoints{}); err == nil {
		t.Fatal("a request without a discovered endpoint was sent")
	}
	if f.idp.DeviceRequests != 0 {
		t.Fatal("a request reached the fake without an endpoint")
	}
}

func TestGrokDevicePendingThreeTimesThenSuccessPollsAtTheInterval(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending}
	a := f.authorize(t)
	start := f.clock.Now()
	tokens, err := f.poll(a, f.far())
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken == "" || f.idp.DeviceTokenSuccessCount != 1 {
		t.Fatal("expected one token set")
	}
	if got := f.clock.Waits(); len(got) != 4 || got[0] != 5*time.Second || got[1] != 5*time.Second || got[2] != 5*time.Second || got[3] != 5*time.Second {
		t.Fatalf("waits = %v", got)
	}
	times := f.idp.DevicePollTimes
	if len(times) != 4 || !times[0].Equal(start.Add(5*time.Second)) {
		t.Fatalf("poll times = %v", times)
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) != 5*time.Second {
			t.Fatalf("poll %d came %v after the previous one", i+1, times[i].Sub(times[i-1]))
		}
	}
	form := f.idp.DevicePollForms[0]
	if form.Get("grant_type") != subscription.GrokDeviceGrantType || form.Get("client_id") != subscription.GrokClientID || form.Has("client_secret") || f.idp.TokenAuthHeaders[0] != "" {
		t.Fatalf("poll form = %v", form)
	}
	if f.claims != 4 || f.releases != 3 {
		t.Fatalf("claims = %d releases = %d, want 4 and 3 (the success keeps its claim)", f.claims, f.releases)
	}
	if len(f.polls) != 4 || f.polls[3].Outcome != subscription.GrokDevicePollSuccess || f.polls[0].Outcome != subscription.GrokDevicePollPending {
		t.Fatalf("observed polls = %v", f.polls)
	}
}

func TestGrokDeviceSlowDownDelaysTheNextPollByFiveSeconds(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DeviceSlowDown, grokidptest.DevicePending, grokidptest.DeviceSlowDown}
	a := f.authorize(t)
	if _, err := f.poll(a, f.far()); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second, 15 * time.Second}
	got := f.clock.Waits()
	if len(got) != len(want) {
		t.Fatalf("waits = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
	times := f.idp.DevicePollTimes
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < want[i] {
			t.Fatalf("poll %d came faster than the interval", i+1)
		}
	}
	if f.polls[1].Interval != 5*time.Second || f.polls[2].Interval != 10*time.Second || f.polls[4].Interval != 15*time.Second {
		t.Fatalf("observed intervals = %v", f.polls)
	}
}

// slow_down always adds five seconds, also when the interval already sits at the
// cap that applies to the interval the provider demands in its authorization
// answer.
func TestGrokDeviceSlowDownAddsFiveSecondsAboveTheIntervalCap(t *testing.T) {
	f := newGrokDeviceFlow(t, int(subscription.GrokDeviceMaxInterval/time.Second))
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DeviceSlowDown, grokidptest.DeviceSlowDown}
	a := f.authorize(t)
	if _, err := f.poll(a, f.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{300 * time.Second, 305 * time.Second, 310 * time.Second}
	got := f.clock.Waits()
	if len(got) != len(want) {
		t.Fatalf("waits = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", got, want)
		}
	}
	times := f.idp.DevicePollTimes
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < want[i] {
			t.Fatalf("poll %d came faster than the slowed interval", i+1)
		}
	}
}

// A claim commit that outlives the local deadline sends nothing, clears the
// claim again and expires.
func TestGrokDeviceDeadlinePassedWhileClaimingSendsNothing(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending}
	a := f.authorize(t)
	deadline := f.clock.Now().Add(11 * time.Second)
	_, err := subscription.PollGrokDeviceToken(context.Background(), a, subscription.GrokDevicePolling{
		Client: f.idp.Client(), Profile: f.profile, Endpoints: f.endpoints, Clock: f.clock, Deadline: deadline,
		Claim: func(ctx context.Context) error {
			f.claims++
			if f.claims == 2 {
				// The second claim blocks on the account lock until after the deadline.
				return f.clock.Wait(ctx, 2*time.Second)
			}
			return nil
		},
		Release: func(context.Context) error { f.releases++; return f.releaseEr },
	})
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonDeviceExpired || uncertain {
		t.Fatalf("err = %v", err)
	}
	if len(f.idp.DevicePollForms) != 1 || f.claims != 2 || f.releases != 2 {
		t.Fatalf("token requests = %d claims = %d releases = %d: nothing may be sent after the deadline and the claim must be cleared", len(f.idp.DevicePollForms), f.claims, f.releases)
	}
}

func TestGrokDeviceDeadlinePassedWhileClaimingWithAFailedReleaseIsUncertain(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	a := f.authorize(t)
	f.releaseEr = errors.New("store unavailable")
	_, err := subscription.PollGrokDeviceToken(context.Background(), a, subscription.GrokDevicePolling{
		Client: f.idp.Client(), Profile: f.profile, Endpoints: f.endpoints, Clock: f.clock, Deadline: f.clock.Now().Add(7 * time.Second),
		Claim:   func(ctx context.Context) error { return f.clock.Wait(ctx, 3*time.Second) },
		Release: func(context.Context) error { return f.releaseEr },
	})
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenUncertain || !uncertain || len(f.idp.DevicePollForms) != 0 {
		t.Fatalf("err = %v requests = %d", err, len(f.idp.DevicePollForms))
	}
}

// A success that arrives at or after the local deadline consumed the device
// code but is never accepted: the claim stands and the outcome is uncertain.
func TestGrokDeviceLateSuccessIsNotAccepted(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.BeforeDevicePoll = func(int) { _ = f.clock.Wait(context.Background(), 3*time.Second) }
	a := f.authorize(t)
	tokens, err := f.poll(a, f.clock.Now().Add(7*time.Second))
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenUncertain || !uncertain {
		t.Fatalf("err = %v", err)
	}
	if tokens.AccessToken != "" || f.idp.DeviceTokenSuccessCount != 1 || f.claims != 1 || f.releases != 0 {
		t.Fatalf("tokens accepted = %v, successes = %d, claims = %d, releases = %d", tokens.AccessToken != "", f.idp.DeviceTokenSuccessCount, f.claims, f.releases)
	}
	if len(f.polls) != 1 || f.polls[0].Outcome != subscription.GrokDevicePollUncertain {
		t.Fatalf("observed polls = %v", f.polls)
	}
}

func TestGrokDeviceTerminalAnswersAreDistinctAndReleaseTheClaim(t *testing.T) {
	for name, test := range map[string]struct {
		step   grokidptest.DeviceStep
		reason subscription.GrokOAuthReason
	}{
		"denied":   {grokidptest.DeviceDenied, subscription.GrokReasonDenied},
		"expired":  {grokidptest.DeviceExpired, subscription.GrokReasonDeviceExpired},
		"rejected": {grokidptest.DeviceUnknown, subscription.GrokReasonTokenRejected},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokDeviceFlow(t, 5)
			f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, test.step}
			a := f.authorize(t)
			_, err := f.poll(a, f.far())
			reason, uncertain := subscription.GrokOAuthReasonOf(err)
			if reason != test.reason || uncertain {
				t.Fatalf("reason = %q uncertain = %v", reason, uncertain)
			}
			if f.claims != 2 || f.releases != 2 || len(f.idp.DevicePollTimes) != 2 || f.idp.DeviceTokenSuccessCount != 0 {
				t.Fatalf("claims = %d releases = %d polls = %d", f.claims, f.releases, len(f.idp.DevicePollTimes))
			}
		})
	}
}

func TestGrokDeviceLocalDeadlineExpiresWithoutAnotherRequest(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending, grokidptest.DevicePending}
	a := f.authorize(t)
	deadline := f.clock.Now().Add(12 * time.Second)
	_, err := f.poll(a, deadline)
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonDeviceExpired || uncertain {
		t.Fatalf("err = %v", err)
	}
	if len(f.idp.DevicePollTimes) != 2 || f.clock.Now().After(deadline) {
		t.Fatalf("polls = %d, now = %v", len(f.idp.DevicePollTimes), f.clock.Now())
	}
	if got := f.clock.Waits(); len(got) != 3 || got[2] != 2*time.Second {
		t.Fatalf("waits = %v", got)
	}
	if f.releases != f.claims {
		t.Fatal("an expiry left a claim standing")
	}
}

func TestGrokDeviceDeadlineIsCappedByTheOperationExpiry(t *testing.T) {
	now := time.Now()
	if got := subscription.GrokDeviceDeadline(now, time.Hour, now.Add(15*time.Minute)); !got.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("deadline = %v", got)
	}
	if got := subscription.GrokDeviceDeadline(now, 5*time.Minute, now.Add(15*time.Minute)); !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("deadline = %v", got)
	}
}

func TestGrokDeviceUncertainRequestKeepsTheClaim(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending, grokidptest.DeviceFailure}
	a := f.authorize(t)
	_, err := f.poll(a, f.far())
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenUncertain || !uncertain {
		t.Fatalf("err = %v", err)
	}
	if f.claims != 2 || f.releases != 1 {
		t.Fatalf("claims = %d releases = %d: the uncertain request must keep its claim", f.claims, f.releases)
	}
}

// A device_code answer other than 200 or 4xx, and a 200 whose token response is
// unusable, may hide a consumed device code: both keep the durable claim and
// stay uncertain instead of proving the code was not consumed.
func TestGrokDeviceUnusableAnswersKeepTheClaimAsUncertain(t *testing.T) {
	cases := map[string]func(*grokidptest.IdP){
		"truncated success": func(i *grokidptest.IdP) {
			i.DeviceTokenBody = `{"access_token":"a","token_type":"Bearer","expires_in":`
		},
		"incomplete success": func(i *grokidptest.IdP) {
			i.DeviceTokenBody = `{"access_token":"a","token_type":"mac","expires_in":5}`
		},
		"accepted status": func(i *grokidptest.IdP) { i.DeviceTokenStatus = http.StatusAccepted },
		"redirect status": func(i *grokidptest.IdP) { i.DeviceTokenStatus = http.StatusFound },
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGrokDeviceFlow(t, 5)
			prepare(f.idp)
			a := f.authorize(t)
			_, err := f.poll(a, f.far())
			if _, uncertain := subscription.GrokOAuthReasonOf(err); !uncertain {
				t.Fatalf("an unusable answer must be uncertain: %v", err)
			}
			if f.claims != 1 || f.releases != 0 {
				t.Fatalf("claims = %d releases = %d: the claim must stand", f.claims, f.releases)
			}
		})
	}
}

func TestGrokDeviceFailedReleaseIsUncertain(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.idp.DeviceScript = []grokidptest.DeviceStep{grokidptest.DevicePending}
	f.releaseEr = errors.New("store unavailable")
	a := f.authorize(t)
	_, err := f.poll(a, f.far())
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenUncertain || !uncertain || len(f.idp.DevicePollTimes) != 1 {
		t.Fatalf("err = %v polls = %d", err, len(f.idp.DevicePollTimes))
	}
}

func TestGrokDeviceRefusedClaimSendsNothing(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	f.claimErr = errors.New("claim refused")
	a := f.authorize(t)
	if _, err := f.poll(a, f.far()); !errors.Is(err, f.claimErr) {
		t.Fatalf("err = %v", err)
	}
	if len(f.idp.DevicePollForms) != 0 {
		t.Fatal("a token request was sent without a durable claim")
	}
}

func TestGrokDeviceCancellationWhileWaitingSendsNothing(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	ctx, cancel := context.WithCancel(context.Background())
	f.clock.Gate = func(ctx context.Context, _ int, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	a := f.authorize(t)
	_, err := subscription.PollGrokDeviceToken(ctx, a, subscription.GrokDevicePolling{Client: f.idp.Client(), Profile: f.profile, Endpoints: f.endpoints, Clock: f.clock, Deadline: f.far(),
		Claim: func(context.Context) error { f.claims++; return nil }, Release: func(context.Context) error { return nil }})
	if !errors.Is(err, context.Canceled) || f.claims != 0 || len(f.idp.DevicePollForms) != 0 {
		t.Fatalf("err = %v claims = %d", err, f.claims)
	}
}

func TestGrokDeviceAndBrowserLoginsBuildIdenticalBundles(t *testing.T) {
	f := newGrokDeviceFlow(t, 5)
	keys, err := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	if err != nil {
		t.Fatal(err)
	}
	a := f.authorize(t)
	deviceTokens, err := f.poll(a, f.far())
	if err != nil {
		t.Fatal(err)
	}
	browser := newGrokFlow(t)
	browser.idp = f.idp
	browser.endpoints = f.endpoints
	auth, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		t.Fatal(err)
	}
	callback, err := f.idp.Authorize(f.profile.AuthorizationURL(f.endpoints, grokTestRedirect, auth))
	if err != nil {
		t.Fatal(err)
	}
	browserTokens, err := subscription.ExchangeGrokCode(context.Background(), f.idp.Client(), f.profile, f.endpoints, grokidptest.Code(callback), auth.Verifier(), grokTestRedirect)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	viaDevice, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, deviceTokens, subscription.GrokLoginDeviceCode, "", now)
	if err != nil {
		t.Fatal(err)
	}
	viaBrowser, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, browserTokens, subscription.GrokLoginBrowser, auth.Nonce, now)
	if err != nil {
		t.Fatal(err)
	}
	if !viaDevice.Equal(viaBrowser) {
		t.Fatal("the two methods produced different bundles for one identity")
	}
	if _, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, browserTokens, subscription.GrokLoginBrowser, "", now); err == nil {
		t.Fatal("the browser method accepted an empty nonce")
	}
	if _, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, deviceTokens, subscription.GrokLoginMethod("other"), "", now); err == nil {
		t.Fatal("an unknown method was accepted")
	}
}

func TestGrokDeviceIDTokenIsValidatedLikeTheBrowserToken(t *testing.T) {
	for name, edit := range map[string]func(*grokidptest.IdP){
		"issuer":    func(i *grokidptest.IdP) { i.IDTokenIssuer = "https://evil.example" },
		"audience":  func(i *grokidptest.IdP) { i.IDTokenAudience = "another-client" },
		"signature": func(i *grokidptest.IdP) { i.SignWithOtherKey = true },
		"expired":   func(i *grokidptest.IdP) { i.IDTokenLifetime = -time.Minute },
		"subject":   func(i *grokidptest.IdP) { i.UserinfoSubject = "someone-else" },
		"principal": func(i *grokidptest.IdP) { i.PrincipalID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokDeviceFlow(t, 5)
			edit(f.idp)
			keys, _ := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
			a := f.authorize(t)
			tokens, err := f.poll(a, f.far())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := subscription.CompleteGrokLogin(context.Background(), f.idp.Client(), f.profile, f.endpoints, keys, tokens, subscription.GrokLoginDeviceCode, "", time.Now()); err == nil {
				t.Fatal("an invalid device ID token produced a bundle")
			}
		})
	}
}
