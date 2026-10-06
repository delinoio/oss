// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// This path is synthetic registration evidence, not a claimed Baseten approval path.
const deviceFixtureURI = "https://app.baseten.co/delidev-fixture-approval"

type deviceFixtureClient struct {
	authorize func(context.Context, oauthProfile) (oauthDeviceGrant, error)
	poll      func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error)
}

func (f deviceFixtureClient) Authorize(c context.Context, p oauthProfile) (oauthDeviceGrant, error) {
	return f.authorize(c, p)
}
func (f deviceFixtureClient) Poll(c context.Context, p oauthProfile, v []byte) (oauthTokenResult, oauthDevicePoll, error) {
	return f.poll(c, p, v)
}
func newDeviceFixture(t *testing.T) *oauthFixture {
	f := newOAuthFixture(t)
	f.s.oauthRegistrations = map[domain.ProviderPresetID]providers.OAuthRegistration{domain.PresetBaseten: {ClientID: "delidev-device-fixture", VerificationURI: deviceFixtureURI, Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}}
	inventory, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range inventory.Msg.Entries {
		if entry.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_BASETEN {
			f.provider = entry.Provider
			if entry.ConnectionMethod != pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_OAUTH_DEVICE {
				t.Fatal("wrong Device method")
			}
		}
	}
	return f
}
func (f *oauthFixture) deviceStart(t *testing.T, id domain.ID) *pb.StartAccountOAuthResponse {
	t.Helper()
	r, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, id)}))
	if e != nil {
		t.Fatal(e)
	}
	return r.Msg
}
func (f *oauthFixture) deviceStatus(t *testing.T, id string, want pb.AccountOAuthState) *pb.GetAccountOAuthStatusResponse {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, e := f.s.GetAccountOAuthStatus(f.ctx, connect.NewRequest(&pb.GetAccountOAuthStatusRequest{AttemptId: id}))
		if e != nil {
			t.Fatal(e)
		}
		if r.Msg.Attempt.State == want {
			return r.Msg
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("device did not settle")
	return nil
}
func deviceGrant() oauthDeviceGrant {
	return oauthDeviceGrant{device: []byte("device-private-sentinel"), user: []byte("ABCD-EFGH"), interval: time.Millisecond, expires: time.Now().Add(time.Minute)}
}
func (f *oauthFixture) deviceJoined(t *testing.T) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		unlock, e := f.s.lockAccounts(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		n := len(f.s.accountChecks)
		unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("unjoined device job")
}
func TestBasetenDevicePendingSlowDownAndProtectedCompletion(t *testing.T) {
	f := newDeviceFixture(t)
	var authorize, polls atomic.Int32
	var prior time.Time
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) {
		authorize.Add(1)
		return deviceGrant(), nil
	}, poll: func(_ context.Context, p oauthProfile, code []byte) (oauthTokenResult, oauthDevicePoll, error) {
		if string(code) != "device-private-sentinel" || p.registration.ClientID != "delidev-device-fixture" {
			t.Error("wrong Device ownership")
		}
		n := polls.Add(1)
		if n == 1 {
			return oauthTokenResult{}, oauthDevicePending, nil
		}
		if n == 2 {
			prior = time.Now()
			return oauthTokenResult{}, oauthDeviceSlowDown, nil
		}
		if time.Since(prior) < 5*time.Second {
			t.Error("slow_down interval not increased")
		}
		return tokenResult("baseten-access-sentinel", "baseten-refresh-sentinel", time.Now().Add(time.Hour)), oauthDeviceIssued, nil
	}}
	id := domain.NewID()
	start := f.deviceStart(t, id)
	if start.Flow != pb.AccountOAuthFlow_ACCOUNT_OAUTH_FLOW_DEVICE || start.UserCode != "ABCD-EFGH" || start.AuthorizationUrl != deviceFixtureURI {
		t.Fatal("wrong Device Start")
	}
	replay := f.deviceStart(t, id)
	if !replay.Replayed || replay.Attempt.Id != start.Attempt.Id || authorize.Load() != 1 {
		t.Fatal("Start repeated authorize")
	}
	_, e := f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(start.Attempt, domain.NewID()), AuthorizationCode: []byte("foreign-callback"), AuthorizationState: []byte("state")}))
	wantAccountCode(t, e, domain.PermissionDenied)
	connected := f.deviceStatus(t, start.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED)
	f.deviceJoined(t)
	if connected.Account == nil || accountBody(t, connected.Account).Health != domain.AccountUnverified || domain.ID(connected.RequestId).Validate() != nil {
		t.Fatal("wrong connected result")
	}
	r, e := f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(start.Attempt, domain.ID(connected.RequestId))}))
	if e != nil || !r.Msg.Replayed || r.Msg.Account.Id != connected.Account.Id {
		t.Fatalf("local receipt replay: %v", e)
	}
	if polls.Load() != 3 || authorize.Load() != 1 {
		t.Fatal("Device issuance repeated")
	}
	assertOAuthPrivate(t, f, "device-private-sentinel", "ABCD-EFGH", "baseten-access-sentinel", "baseten-refresh-sentinel")
}
func TestBasetenDeviceUnknownPollStopsAndRestartCannotResume(t *testing.T) {
	f := newDeviceFixture(t)
	var calls atomic.Int32
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) { return deviceGrant(), nil }, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
		calls.Add(1)
		return oauthTokenResult{}, oauthDeviceIssued, oauthCredentialProblem()
	}}
	id := domain.NewID()
	start := f.deviceStart(t, id)
	f.deviceStatus(t, start.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED)
	f.deviceJoined(t)
	f.deviceStart(t, id)
	f.restart(t)
	f.s.oauthRegistrations = map[domain.ProviderPresetID]providers.OAuthRegistration{domain.PresetBaseten: {ClientID: "delidev-device-fixture", VerificationURI: deviceFixtureURI, Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}}
	f.deviceStatus(t, start.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED)
	if calls.Load() != 1 {
		t.Fatal("uncertain poll repeated")
	}
}
func TestBasetenDeviceCancellationSuppressesLateTokens(t *testing.T) {
	f := newDeviceFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) { return deviceGrant(), nil }, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
		close(entered)
		<-release
		return tokenResult("late-access", "late-refresh", time.Now().Add(time.Hour)), oauthDeviceIssued, nil
	}}
	start := f.deviceStart(t, domain.NewID())
	<-entered
	_, e := f.s.CancelAccountOAuth(f.ctx, connect.NewRequest(&pb.CancelAccountOAuthRequest{Mutation: oauthMutation(start.Attempt, domain.NewID())}))
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	f.deviceJoined(t)
	status := f.deviceStatus(t, start.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CANCELED)
	if status.Account != nil || len(f.vault.values) != 0 {
		t.Fatal("late token publication")
	}
}
func TestBasetenDeviceWireClosedResponses(t *testing.T) {
	p := oauthProfile{preset: domain.PresetBaseten, authorization: "https://api.baseten.co/v1/users/auth/device/authorize", token: "https://api.baseten.co/v1/users/auth/device/token", registration: providers.OAuthRegistration{ClientID: "delidev-device-fixture", VerificationURI: deviceFixtureURI}}
	for _, tc := range []struct {
		name, body string
		status     int
		phase      oauthDevicePoll
		ok         bool
	}{
		{"pending", `{"error":"authorization_pending"}`, 400, oauthDevicePending, true}, {"slow", `{"error":"slow_down"}`, 400, oauthDeviceSlowDown, true}, {"wrong-status", `{"error":"authorization_pending"}`, 503, oauthDeviceIssued, false}, {"duplicate", `{"error":"authorization_pending","error":"slow_down"}`, 400, oauthDeviceIssued, false}, {"denied", `{"error":"access_denied"}`, 400, oauthDeviceIssued, false}, {"unknown", `{"error":"temporarily_unavailable"}`, 400, oauthDeviceIssued, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			c := ownedOAuthTokenClient{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != p.token || r.GetBody != nil {
					t.Error("wrong Device HTTP authority")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			result, phase, e := c.Poll(context.Background(), p, []byte("device-private"))
			result.tokens.clear()
			if (e == nil) != tc.ok || phase != tc.phase || calls != 1 {
				t.Fatalf("closed poll: %v", e)
			}
		})
	}
	c := ownedOAuthTokenClient{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"device_code":"private-device","user_code":"ABCD-EFGH","verification_uri":"https://evil.example/approve","expires_in":600,"interval":5}`))}, nil
	})}
	g, e := c.Authorize(context.Background(), p)
	g.clear()
	if e == nil {
		t.Fatal("foreign approval URI accepted")
	}
}

func TestBasetenDeviceVaultRecoveryNeverPollsAgain(t *testing.T) {
	f := newDeviceFixture(t)
	var polls atomic.Int32
	f.vault.putError = io.ErrUnexpectedEOF
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) { return deviceGrant(), nil }, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
		polls.Add(1)
		return tokenResult("protected-device-access", "protected-device-refresh", time.Now().Add(time.Hour)), oauthDeviceIssued, nil
	}}
	start := f.deviceStart(t, domain.NewID())
	status := f.deviceStatus(t, start.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED)
	f.deviceJoined(t)
	if domain.ID(status.RequestId).Validate() != nil {
		t.Fatal("missing original completion receipt")
	}
	f.vault.putError = nil
	r, e := f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(start.Attempt, domain.ID(status.RequestId))}))
	if e != nil || r.Msg.Account == nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED {
		t.Fatalf("local Device recovery: %v", e)
	}
	if polls.Load() != 1 {
		t.Fatal("local recovery repeated Device polling")
	}
}
func TestBasetenDeviceDeniedExpiredAndMissingRegistration(t *testing.T) {
	for _, scenario := range []string{"denied", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDeviceFixture(t)
			var calls atomic.Int32
			f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) {
				g := deviceGrant()
				if scenario == "expired" {
					g.expires = time.Now().Add(-time.Second)
				}
				return g, nil
			}, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
				calls.Add(1)
				return oauthTokenResult{}, oauthDeviceIssued, domain.Fail(domain.PermissionDenied, "denied", "")
			}}
			start := f.deviceStart(t, domain.NewID())
			want := pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_FAILED
			if scenario == "expired" {
				want = pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_EXPIRED
			}
			status := f.deviceStatus(t, start.Attempt.Id, want)
			f.deviceJoined(t)
			if status.Account != nil || len(f.vault.values) != 0 || calls.Load() > 1 {
				t.Fatal("terminal Device publication")
			}
		})
	}
	f := newDeviceFixture(t)
	f.s.oauthRegistrations = nil
	_, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID())}))
	wantAccountCode(t, e, domain.Unsupported)
}
func TestBasetenDeviceShutdownJoinsTheAuthorizeOwner(t *testing.T) {
	f := newDeviceFixture(t)
	entered := make(chan struct{})
	returned := make(chan error, 1)
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(ctx context.Context, _ oauthProfile) (oauthDeviceGrant, error) {
		close(entered)
		<-ctx.Done()
		return oauthDeviceGrant{}, ctx.Err()
	}, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
		t.Error("shutdown polled")
		return oauthTokenResult{}, oauthDeviceIssued, oauthCredentialProblem()
	}}
	go func() {
		_, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID())}))
		returned <- e
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- f.s.closeAccountSecrets() }()
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join Device authorize")
	}
	<-returned
	f.deviceJoined(t)
	if len(f.vault.values) != 0 {
		t.Fatal("shutdown published credentials")
	}
}
