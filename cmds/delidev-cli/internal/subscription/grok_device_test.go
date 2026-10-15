// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
		"complete url without the user code": func(i *grokidp.IdP) {
			i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?source=cli"
		},
		"complete url repeated parameter": func(i *grokidp.IdP) {
			i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&source=cli&source=web"
		},
		"complete url control character": func(i *grokidp.IdP) {
			i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&source=c%0Ali"
		},
		"complete url unsafe parameter name": func(i *grokidp.IdP) {
			i.DeviceCompleteURI = "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&a%20b=1"
		},
		"lowercase user code":  func(i *grokidp.IdP) { i.DeviceUserCode = "abcd-1234" },
		"short user code":      func(i *grokidp.IdP) { i.DeviceUserCode = "AB" },
		"zero expiry":          func(i *grokidp.IdP) { i.DeviceExpiresIn = 0 },
		"huge expiry":          func(i *grokidp.IdP) { i.DeviceExpiresIn = 86401 * 2 },
		"interval above bound": func(i *grokidp.IdP) { i.DeviceInterval = 61 },
		"missing device code": func(i *grokidp.IdP) {
			i.DeviceBody = `{"user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`
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
	for _, bad := range []string{"", "https://accounts.x.ai", "https://accounts.x.ai:8443/d", "https://accounts.x.ai/d#frag", "ftp://accounts.x.ai/d", "https://accounts.x.ai.evil/d"} {
		if _, ok := subscription.GrokVerificationURL(bad, ""); ok {
			t.Fatalf("verification URL %q accepted", bad)
		}
	}
}

// The complete verification URI is the provider's own shortcut: a routing or
// tracking parameter next to the correct user code must not abort the whole device
// authorization, and the safe parameters are preserved.
func TestGrokDeviceAuthorizationAcceptsSafeExtraParametersInTheCompleteURI(t *testing.T) {
	for name, complete := range map[string]string{
		"source":          "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&source=cli",
		"before the code": "https://accounts.x.ai/oauth2/device?source=cli&user_code=ABCD-1234",
		"several":         "https://accounts.x.ai/oauth2/device?user_code=ABCD-1234&source=cli&utm_campaign=grok-build&lang=en-US",
	} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.DeviceCompleteURI = complete
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatalf("a complete URI with safe extra parameters aborted the authorization: %v", err)
			}
			defer grant.Clear()
			if grant.VerificationURL != complete || grant.UserCode != "ABCD-1234" {
				t.Fatal("the complete verification URI was altered")
			}
		})
	}
	for _, good := range []string{"https://accounts.x.ai/d?other=1", "https://accounts.x.ai/d?user_code=ABCD-1234&source=cli"} {
		if _, ok := subscription.GrokVerificationURL(good, "ABCD-1234"); !ok {
			t.Fatalf("verification URL %q rejected", good)
		}
	}
}

// The device poll shares the token response rules: a definitive answer is one
// whole JSON object. An OAuth error object followed by other data is not
// definitive, because the device code may have been redeemed; it must stay
// uncertain instead of being read as pending, slow_down, denied or expired.
func TestGrokDevicePollRejectsTrailingDataInEveryAnswer(t *testing.T) {
	for _, answer := range []string{"authorization_pending", "slow_down", "access_denied", "expired_token", "invalid_grant"} {
		for _, trailer := range []string{` {"x":1}`, `junk`} {
			t.Run(answer+trailer, func(t *testing.T) {
				idp := grokidp.New(t)
				idp.DeviceScript = []string{answer}
				idp.TokenTrailer = trailer
				client, endpoints := deviceFixture(t, idp)
				grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
				if err != nil {
					t.Fatal(err)
				}
				defer grant.Clear()
				_, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
				if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain {
					t.Fatalf("an %s answer with trailing data was not uncertain: %v", answer, err)
				}
			})
		}
	}
	t.Run("whole objects stay definitive", func(t *testing.T) {
		idp := grokidp.New(t)
		idp.DeviceScript = []string{"authorization_pending", "slow_down", "access_denied", "expired_token"}
		client, endpoints := deviceFixture(t, idp)
		grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		defer grant.Clear()
		want := []subscription.GrokDevicePoll{subscription.GrokDevicePending, subscription.GrokDeviceSlowDown, subscription.GrokDeviceDenied, subscription.GrokDeviceExpired}
		for _, w := range want {
			if _, got, err := client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode); err != nil || got != w {
				t.Fatalf("poll = %v, %v; want %v", got, err, w)
			}
		}
	})
}

// A repeated member makes a device answer ambiguous: the decoder keeps the last
// one, so a repeated error could turn a rejection into a pending answer. An
// answer that also carries a token member is ambiguous for the same reason.
func TestGrokDeviceResponsesRejectRepeatedMembers(t *testing.T) {
	t.Run("poll answer", func(t *testing.T) {
		for _, body := range []string{
			`{"error":"authorization_pending","error":"access_denied"}`,
			`{"error":"slow_down","Error":"slow_down"}`,
			// An answer that also carries a token member contradicts itself.
			`{"error":"authorization_pending","access_token":"issued"}`,
			`{"error":"access_denied","refresh_token":""}`,
		} {
			idp := grokidp.New(t)
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			idp.TokenStatus, idp.TokenBody = http.StatusBadRequest, body
			_, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain {
				t.Fatalf("a poll answer %s was not uncertain: %v", body, err)
			}
			grant.Clear()
		}
	})
	t.Run("success answer with an error member", func(t *testing.T) {
		for _, body := range []string{
			`{"access_token":"a","refresh_token":"r","id_token":"i","token_type":"Bearer","expires_in":3600,"error":"authorization_pending"}`,
			`{"access_token":"a","refresh_token":"r","id_token":"i","token_type":"Bearer","expires_in":3600,"error":""}`,
		} {
			idp := grokidp.New(t)
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			idp.TokenStatus, idp.TokenBody = http.StatusOK, body
			_, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain {
				t.Fatalf("a contradictory success answer %s was not uncertain: %v", body, err)
			}
			grant.Clear()
		}
	})
	t.Run("authorization", func(t *testing.T) {
		idp := grokidp.New(t)
		idp.DeviceBody = `{"device_code":"abc","user_code":"ABCD-1234","user_code":"WXYZ-9999","verification_uri":"https://accounts.x.ai/d","expires_in":60}`
		client, endpoints := deviceFixture(t, idp)
		if _, err := client.RequestDeviceAuthorization(context.Background(), endpoints); err == nil {
			t.Fatal("a device authorization that repeats a member was accepted")
		}
	})
}

// Member names are case-sensitive in every device answer: a differently cased
// spelling of a member the client reads is an alias, never the member, so it can
// neither stand in for a pending/denied answer nor sit beside a valid
// authorization member.
func TestGrokDeviceResponsesRequireExactMemberNames(t *testing.T) {
	t.Run("poll answer", func(t *testing.T) {
		for _, body := range []string{
			`{"ERROR":"authorization_pending"}`,
			`{"Error":"slow_down"}`,
			`{"ERROR":"access_denied"}`,
			`{"error":"authorization_pending","Error_Description":"wait"}`,
			`{"error":"expired_token","Access_Token":"x"}`,
		} {
			idp := grokidp.New(t)
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatal(err)
			}
			idp.TokenStatus, idp.TokenBody = http.StatusBadRequest, body
			_, _, err = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain {
				t.Fatalf("a poll answer %s was not uncertain: %v", body, err)
			}
			grant.Clear()
		}
	})
	t.Run("authorization", func(t *testing.T) {
		const control = `"device_code":"abc","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","verification_uri_complete":"https://accounts.x.ai/d?user_code=ABCD-1234","expires_in":60,"interval":5`
		for name, body := range map[string]string{
			"only an upper-case device_code":         `{"DEVICE_CODE":"abc","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
			"an upper-case alias beside device_code": `{` + control + `,"DEVICE_CODE":"other"}`,
			"a capitalized user_code alias":          `{` + control + `,"User_Code":"WXYZ-9999"}`,
			"a verification_uri alias":               `{` + control + `,"Verification_URI":"https://accounts.x.ai/other"}`,
			"a verification_uri_complete alias":      `{` + control + `,"VERIFICATION_URI_COMPLETE":"https://accounts.x.ai/other"}`,
			"an expires_in alias":                    `{` + control + `,"Expires_In":1}`,
			"an interval alias":                      `{` + control + `,"INTERVAL":1}`,
		} {
			t.Run(name, func(t *testing.T) {
				idp := grokidp.New(t)
				idp.DeviceBody = body
				client, endpoints := deviceFixture(t, idp)
				if _, err := client.RequestDeviceAuthorization(context.Background(), endpoints); err == nil {
					t.Fatalf("a device authorization with %s was accepted", name)
				}
			})
		}
		idp := grokidp.New(t)
		idp.DeviceBody = `{` + control + `,"unrelated":1}`
		client, endpoints := deviceFixture(t, idp)
		grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
		if err != nil {
			t.Fatalf("the control authorization was refused: %v", err)
		}
		grant.Clear()
	})
}

// The device code is validated on its decoded value, not on one particular JSON
// encoding: a legal escape of a printable character is accepted, anything that
// decodes to a forbidden byte is not.
func TestGrokDeviceCodeIsValidatedAfterDecoding(t *testing.T) {
	for name, body := range map[string]string{
		"escaped slash":               `{"device_code":"abc\/def","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"unicode escape":              `{"device_code":"ABCD","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"escaped quote":               `{"device_code":"a\"b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"escaped backslash":           `{"device_code":"a\\b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"unicode quote":               `{"device_code":"a\u0022b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"unicode backslash":           `{"device_code":"a\u005cb","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"unicode backslash uppercase": `{"device_code":"a\u005Cb","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"only quote and backslash":    `{"device_code":"\"\\","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"lowercase escaped":           `{"device_code":"~~abc","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
	} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.DeviceBody = body
			client, endpoints := deviceFixture(t, idp)
			grant, err := client.RequestDeviceAuthorization(context.Background(), endpoints)
			if err != nil {
				t.Fatalf("a legal escape was refused: %v", err)
			}
			defer grant.Clear()
			if len(grant.DeviceCode) == 0 || strings.Contains(string(grant.DeviceCode), `\u`) || strings.Contains(string(grant.DeviceCode), `\/`) {
				t.Fatalf("the device code was not decoded: %q", grant.DeviceCode)
			}
			// The decoded bytes, including quote and backslash, reach the token
			// endpoint intact through form encoding.
			want := string(grant.DeviceCode)
			_, _, _ = client.PollDeviceToken(context.Background(), endpoints, grant.DeviceCode)
			if got := idp.LastTokenForm().Get("device_code"); got != want {
				t.Fatalf("the poll sent %q, want the decoded %q", got, want)
			}
		})
	}
	for name, body := range map[string]string{
		"escaped control":   `{"device_code":"a\u0000b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"escaped space":     `{"device_code":"a b","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"escaped non-ascii": `{"device_code":"aéb","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"newline escape":    `{"device_code":"a\nb","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"unknown escape":    `{"device_code":"a\qb","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
		"short unicode":     `{"device_code":"a\u00","user_code":"ABCD-1234","verification_uri":"https://accounts.x.ai/d","expires_in":60}`,
	} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.DeviceBody = body
			client, endpoints := deviceFixture(t, idp)
			if _, err := client.RequestDeviceAuthorization(context.Background(), endpoints); err == nil {
				t.Fatal("a device code that decodes to a forbidden byte was accepted")
			}
		})
	}
}

// The server, the desktop renderer and the native host share one verification
// address rule. This test runs the table that the renderer (vitest) and the
// native host (cargo) also run, so no layer can accept an address another layer
// refuses: every row states the expected result of each layer, and the server
// (which also receives the displayed user code) must match the renderer.
func TestGrokVerificationURLMatchesTheSharedDesktopTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "apps", "delidev", "src", "grok-url-validation-fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		DeviceVerification []struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			UserCode string `json:"userCode"`
			Server   bool   `json:"server"`
			Native   bool   `json:"native"`
			Renderer bool   `json:"renderer"`
		} `json:"deviceVerification"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.DeviceVerification) < 60 {
		t.Fatalf("the shared table has only %d rows", len(fixtures.DeviceVerification))
	}
	for _, row := range fixtures.DeviceVerification {
		t.Run(row.Name, func(t *testing.T) {
			_, valid := subscription.GrokVerificationURL(row.URL, row.UserCode)
			if got := valid && subscription.GrokUserCodeValid(row.UserCode); got != row.Server {
				t.Fatalf("server verdict %v, table says %v for %q", got, row.Server, row.URL)
			}
			if row.Server != row.Renderer {
				t.Fatalf("the table lets the server (%v) and the renderer (%v) disagree", row.Server, row.Renderer)
			}
			if row.Server && !row.Native {
				t.Fatalf("the server accepts %q but the native host refuses it", row.URL)
			}
		})
	}
}
