// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
)

func deviceFixture(t *testing.T, idp *grokidp.IdP) (subscription.GrokOAuthClient, subscription.GrokEndpoints) {
	t.Helper()
	client := grokClient(idp)
	endpoints, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return client, endpoints
}

func TestGrokDeviceProfileIsPinned(t *testing.T) {
	if subscription.GrokDeviceAuthorizationEndpoint != "https://auth.x.ai/oauth2/device/code" || subscription.GrokDeviceGrantType != "urn:ietf:params:oauth:grant-type:device_code" ||
		subscription.GrokVerificationHost != "accounts.x.ai" || subscription.GrokDeviceDefaultInterval != 5*time.Second || subscription.GrokDeviceSlowDownStep != 5*time.Second {
		t.Fatal("Grok device profile drifted")
	}
}

func TestGrokDeviceAuthorizationUsesTheBrowserProfileAndBoundsTheGrant(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints := deviceFixture(t, idp)
	if endpoints.DeviceAuthorization != "https://auth.x.ai/oauth2/device/code" {
		t.Fatalf("device endpoint: %s", endpoints.DeviceAuthorization)
	}
	grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Clear()
	form := idp.LastDeviceForm()
	if idp.DeviceRequests() != 1 || form.Get("client_id") != subscription.GrokClientID || form.Get("scope") != subscription.GrokScope() || form.Get("client_secret") != "" || len(form) != 2 {
		t.Fatal("device request is not the browser profile's public client and scopes")
	}
	if grant.UserCode != "ABCD-1234" || grant.VerificationURL != "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234" || grant.ExpiresIn != 600*time.Second || grant.Interval != 5*time.Second || string(grant.DeviceCode) != idp.IssuedDeviceCode() {
		t.Fatal("device grant was not mapped")
	}
	for _, text := range []string{fmt.Sprintf("%v", grant), fmt.Sprintf("%+v", grant), fmt.Sprintf("%#v", grant), grant.String()} {
		if strings.Contains(text, "ABCD") || strings.Contains(text, "fake-device") || strings.Contains(text, "accounts.x.ai") {
			t.Fatal("grant formatting revealed a device value")
		}
	}
}

func TestGrokDeviceAuthorizationFallsBackToThePlainURLAndTheFixedEndpoint(t *testing.T) {
	idp := grokidp.New(t)
	idp.DeviceOmitComplete, idp.DeviceHideDiscovery, idp.DeviceInterval = true, true, 0
	client, endpoints := deviceFixture(t, idp)
	if endpoints.DeviceAuthorization != subscription.GrokDeviceAuthorizationEndpoint {
		t.Fatal("an unadvertised device endpoint did not fall back to the fixed profile value")
	}
	grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Clear()
	if grant.VerificationURL != "https://accounts.x.ai/oauth2/device" || grant.Interval != subscription.GrokDeviceDefaultInterval {
		t.Fatal("plain verification URL or the default interval is wrong")
	}
}

func TestGrokDeviceAuthorizationRejectsUnsafeGrants(t *testing.T) {
	cases := map[string]func(*grokidp.IdP){
		"foreign verification host": func(i *grokidp.IdP) { i.DeviceVerification = "https://evil.example/oauth2/device" },
		"http verification":         func(i *grokidp.IdP) { i.DeviceVerification = "http://accounts.x.ai/oauth2/device" },
		"credentials in the url":    func(i *grokidp.IdP) { i.DeviceVerification = "https://user@accounts.x.ai/oauth2/device" },
		"complete url wrong host":   func(i *grokidp.IdP) { i.DeviceCompleteURI = "https://auth.x.ai/oauth2/device?user_code=ABCD-1234" },
		"complete url other code":   func(i *grokidp.IdP) { i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?user_code=ZZZZ-9999" },
		"complete url extra query": func(i *grokidp.IdP) {
			i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&x=1"
		},
		"lowercase user code":  func(i *grokidp.IdP) { i.DeviceUserCode = "abcd-1234" },
		"short user code":      func(i *grokidp.IdP) { i.DeviceUserCode = "AB" },
		"zero expiry":          func(i *grokidp.IdP) { i.DeviceExpiresIn = 0 },
		"huge expiry":          func(i *grokidp.IdP) { i.DeviceExpiresIn = 86401 * 2 },
		"interval above bound": func(i *grokidp.IdP) { i.DeviceInterval = 61 },
		"missing device code": func(i *grokidp.IdP) {
			i.DeviceBody = `{"user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`
		},
		"device code with a quote": func(i *grokidp.IdP) {
			i.DeviceBody = `{"device_code":"a\"b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`
		},
		"device code with a space": func(i *grokidp.IdP) {
			i.DeviceBody = `{"device_code":"a b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`
		},
		"no verification url": func(i *grokidp.IdP) { i.DeviceBody = `{"device_code":"abc","user_code":"ABCD-1234","expires_in":60}` },
		"server error":        func(i *grokidp.IdP) { i.DeviceStatus, i.DeviceBody = http.StatusInternalServerError, "oops" },
		"oauth error": func(i *grokidp.IdP) {
			i.DeviceStatus, i.DeviceBody = http.StatusBadRequest, `{"error":"invalid_client"}`
		},
		"not json": func(i *grokidp.IdP) { i.DeviceBody = "<html>" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			mutate(idp)
			client, endpoints := deviceFixture(t, idp)
			_, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonDeviceAuthorization {
				t.Fatalf("unsafe grant was accepted: %v", err)
			}
		})
	}
}

func TestGrokDiscoveryRejectsAForeignDeviceEndpoint(t *testing.T) {
	// The fake provider advertises its device endpoint on the discovery host.
	idp := grokidp.New(t)
	idp.DiscoveryHost = "evil.example"
	if _, err := grokClient(idp).Discover(context.Background()); err == nil {
		t.Fatal("discovery accepted endpoints on a foreign host")
	}
}

func TestGrokDevicePollClassifiesEveryOutcome(t *testing.T) {
	idp := grokidp.New(t)
	idp.DeviceScript = []string{"authorization_pending", "slow_down", "access_denied", "expired_token", "invalid_grant", ""}
	client, endpoints := deviceFixture(t, idp)
	grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Clear()
	poll := func() (subscription.GrokTokens, subscription.GrokDevicePoll, error) {
		return client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
	}
	for _, want := range []subscription.GrokDevicePoll{subscription.GrokDevicePending, subscription.GrokDeviceSlowDown, subscription.GrokDeviceDenied, subscription.GrokDeviceExpired} {
		if _, got, err := poll(); err != nil || got != want {
			t.Fatalf("poll outcome %v, want %v (%v)", got, want, err)
		}
	}
	if _, _, err := poll(); err == nil {
		t.Fatal("an unknown OAuth error was not a rejection")
	} else if reason, _ := subscription.GrokOAuthFailure(err); reason != subscription.GrokReasonTokenRejected {
		t.Fatalf("rejection reason: %v", reason)
	}
	tokens, got, err := poll()
	if err != nil || got != subscription.GrokDeviceTokens || tokens.AccessToken != "fake-access-token" || tokens.RefreshToken != "fake-refresh-token" {
		t.Fatalf("success poll: %v %v", got, err)
	}
	form := idp.LastTokenForm()
	if form.Get("grant_type") != subscription.GrokDeviceGrantType || form.Get("client_id") != subscription.GrokClientID || form.Get("device_code") != idp.IssuedDeviceCode() || form.Get("client_secret") != "" || idp.LastTokenHeaders().Get("Authorization") != "" || len(form) != 3 {
		t.Fatal("device poll is not a public-client device_code grant")
	}
	if idp.DevicePolls() != 6 || idp.TokenRequests() != 6 {
		t.Fatalf("each poll must be exactly one request: %d", idp.DevicePolls())
	}
}

func TestGrokDevicePollUncertainResponsesAreNeverASuccessOrAnOAuthAnswer(t *testing.T) {
	cases := map[string]struct {
		setup  func(*grokidp.IdP)
		reason subscription.GrokOAuthReason
	}{
		"server error":      {func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, "unavailable" }, subscription.GrokReasonTokenUncertain},
		"bad gateway html":  {func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 502, "<html>" }, subscription.GrokReasonTokenUncertain},
		"rate limited":      {func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 429, `{"error":"slow_down"}` }, subscription.GrokReasonTokenUncertain},
		"truncated success": {func(i *grokidp.IdP) { i.TruncateTokenBody = true }, subscription.GrokReasonTokenUncertain},
		"malformed success": {func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` }, subscription.GrokReasonTokenResponse},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Clear()
			tc.setup(idp)
			_, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != tc.reason {
				t.Fatalf("reason %v, want %v", err, tc.reason)
			}
			if idp.TokenRequests() != 1 {
				t.Fatal("a poll was retried")
			}
		})
	}
}

// Both grants build the same bundle for the same token response.
func TestGrokDeviceAndBrowserTokensMapToIdenticalBundles(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	build := func(device bool) []byte {
		idp := grokidp.New(t)
		idp.PrincipalType, idp.PrincipalID, idp.TeamID = "team-member", "principal-77", "team-9"
		client, endpoints := deviceFixture(t, idp)
		var tokens subscription.GrokTokens
		var claims subscription.GrokClaims
		if device {
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Clear()
			tokens, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if err != nil {
				t.Fatal(err)
			}
			claims, err = client.ValidateDeviceIDToken(context.Background(), endpoints, tokens.IDToken)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			a, err := subscription.NewGrokAuthorization(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Clear()
			grant := idp.Approve(t, endpoints.AuthorizationURL(subscription.GrokRedirectURI(43817), a))
			tokens, err = client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
			if err != nil {
				t.Fatal(err)
			}
			claims, err = client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
			if err != nil {
				t.Fatal(err)
			}
		}
		info, err := client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := subscription.BuildGrokBundle(now, tokens, claims, info)
		if err != nil {
			t.Fatal(err)
		}
		file, err := bundle.AuthFile()
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	browser, device := build(false), build(true)
	if string(browser) != string(device) {
		t.Fatal("browser and device login produced different bundles for the same token response")
	}
}

func TestGrokDeviceIDTokenHasNoNonceButEveryOtherCheck(t *testing.T) {
	cases := map[string]func(*grokidp.IdP){
		"bad signature":  func(i *grokidp.IdP) { i.SignWithWrongKey = true },
		"expired":        func(i *grokidp.IdP) { i.ExpiredIDToken = true },
		"wrong issuer":   func(i *grokidp.IdP) { i.OverrideIssuer = "https://evil.example" },
		"wrong audience": func(i *grokidp.IdP) { i.OverrideAudience = "another-client" },
		"no sub":         func(i *grokidp.IdP) { i.Subject = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			mutate(idp)
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Clear()
			tokens, _, err := client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ValidateDeviceIDToken(context.Background(), endpoints, tokens.IDToken); err == nil {
				t.Fatal("an invalid device ID token was accepted")
			}
		})
	}
}

func TestGrokUserCodeAndVerificationURLValidators(t *testing.T) {
	for _, ok := range []string{"ABCD-1234", "WXYZ", "A1B2-C3D4-E5F6"} {
		if !subscription.GrokUserCodeValid(ok) {
			t.Fatalf("user code %q rejected", ok)
		}
	}
	for _, bad := range []string{"", "abc", "AB", "ABCD 1234", "ABCD-1234\n", strings.Repeat("A", 33)} {
		if subscription.GrokUserCodeValid(bad) {
			t.Fatalf("user code %q accepted", bad)
		}
	}
	for _, bad := range []string{"", "https://accounts.x.ai", "https://accounts.x.ai:8443/d", "https://accounts.x.ai/d#frag", "https://accounts.x.ai/d?other=1", "ftp://accounts.x.ai/d", "https://accounts.x.ai.evil/d"} {
		if _, ok := subscription.GrokVerificationURL(bad, ""); ok {
			t.Fatalf("verification URL %q accepted", bad)
		}
	}
}
