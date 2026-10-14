// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
)

// loggedIn completes one login against the fake provider and returns the client,
// the endpoints and the resulting bundle (created at created).
func loggedIn(t *testing.T, idp *grokidp.IdP, created time.Time) (subscription.GrokOAuthClient, subscription.GrokEndpoints, subscription.GrokBundle) {
	t.Helper()
	client, endpoints, a, grant := exchangeFixture(t, idp)
	tokens, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.BuildGrokBundle(created, tokens, claims, info)
	if err != nil {
		t.Fatal(err)
	}
	return client, endpoints, bundle
}

func refreshOnce(t *testing.T, client subscription.GrokOAuthClient, endpoints subscription.GrokEndpoints, current subscription.GrokBundle, now time.Time) (subscription.GrokBundle, error) {
	t.Helper()
	tokens, err := client.Refresh(context.Background(), []byte(currentRefreshToken(t, current)))
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	claims, info, err := client.RefreshedClaims(context.Background(), endpoints, tokens)
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	return subscription.RefreshGrokBundle(now, current, tokens, claims, info)
}

func currentRefreshToken(t *testing.T, b subscription.GrokBundle) string {
	t.Helper()
	file, err := b.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	text := string(file)
	const marker = `"refresh_token":"`
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatal("bundle has no refresh token")
	}
	rest := text[start+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func TestGrokRefreshIsAPublicClientRefreshTokenGrant(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
	before := idp.TokenRequests()
	next, err := refreshOnce(t, client, endpoints, bundle, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if idp.TokenRequests() != before+1 {
		t.Fatal("refresh must send exactly one token request")
	}
	form := idp.LastTokenForm()
	if len(form) != 3 || form.Get("grant_type") != "refresh_token" || form.Get("client_id") != subscription.GrokClientID || form.Get("refresh_token") != "fake-refresh-token" || form.Get("client_secret") != "" || idp.LastTokenHeaders().Get("Authorization") != "" {
		t.Fatal("refresh request is not a public-client refresh_token grant")
	}
	if subscription.GrokTokenEndpoint != "https://auth.x.ai/oauth2/token" {
		t.Fatal("refresh token endpoint drifted")
	}
	if err := subscription.GrokRefreshed(bundle, next); err != nil {
		t.Fatal("a rotated refresh is not accepted as a refresh")
	}
	if got := currentRefreshToken(t, next); got != idp.CurrentRefreshToken() || got == "fake-refresh-token" {
		t.Fatal("rotation was not applied")
	}
}

func TestGrokRefreshKeepsTheRefreshTokenWithoutRotation(t *testing.T) {
	for name, mode := range map[string]grokidp.RefreshMode{"omitted": grokidp.RefreshOmit, "unchanged": grokidp.RefreshUnchanged} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			idp.RefreshMode = mode
			client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
			next, err := refreshOnce(t, client, endpoints, bundle, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if currentRefreshToken(t, next) != "fake-refresh-token" {
				t.Fatal("the current refresh token was not kept")
			}
			// The access key changed and the expiry moved forward, which is what a
			// refresh must prove when the refresh token is not rotated.
			if err := subscription.GrokRefreshed(bundle, next); err != nil {
				t.Fatal("an unrotated refresh was not accepted")
			}
		})
	}
}

func TestGrokRefreshWithoutAnIDTokenUsesTheUserInfoSubject(t *testing.T) {
	idp := grokidp.New(t)
	idp.OmitRefreshIDToken = true
	client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
	next, err := refreshOnce(t, client, endpoints, bundle, time.Now())
	if err != nil || subscription.GrokRefreshed(bundle, next) != nil {
		t.Fatalf("refresh without an ID token failed: %v", err)
	}
	// A userinfo response without a subject is no identity evidence at all.
	idp.UserInfoSubject = ""
	idp.UserInfoStatus = http.StatusNoContent
	if _, err := refreshOnce(t, client, endpoints, next, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("a refresh without any identity evidence was accepted")
	}
}

func TestGrokRefreshRefusesAChangedIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*grokidp.IdP){
		"subject":   func(i *grokidp.IdP) { i.Subject = "someone-else" },
		"principal": func(i *grokidp.IdP) { i.PrincipalType, i.PrincipalID = "team-member", "p-2" },
	} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
			mutate(idp)
			_, err := refreshOnce(t, client, endpoints, bundle, time.Now())
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonIdentity {
				t.Fatalf("identity change was not refused: %v", err)
			}
		})
	}
}

func TestGrokRefreshRefusesAnUnverifiableIDToken(t *testing.T) {
	for name, mutate := range map[string]func(*grokidp.IdP){
		"wrong key": func(i *grokidp.IdP) { i.SignWithWrongKey = true },
		"expired":   func(i *grokidp.IdP) { i.ExpiredIDToken = true },
		"issuer":    func(i *grokidp.IdP) { i.OverrideIssuer = "https://evil.example" },
		"audience":  func(i *grokidp.IdP) { i.OverrideAudience = "someone-else" },
	} {
		t.Run(name, func(t *testing.T) {
			idp := grokidp.New(t)
			client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
			mutate(idp)
			_, err := refreshOnce(t, client, endpoints, bundle, time.Now())
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonIDToken {
				t.Fatalf("%s was accepted: %v", name, err)
			}
		})
	}
}

func TestGrokRefreshClassifiesRejectedAndUncertainOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*grokidp.IdP)
		reason subscription.GrokOAuthReason
	}{
		{"invalid_grant", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 400, `{"error":"invalid_grant"}` }, subscription.GrokReasonTokenRejected},
		{"unauthorized", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 401, `{"error":"invalid_client"}` }, subscription.GrokReasonTokenRejected},
		{"server error", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, `unavailable` }, subscription.GrokReasonTokenUncertain},
		{"redirect", func(i *grokidp.IdP) { i.TokenStatus = 302 }, subscription.GrokReasonTokenUncertain},
		{"truncated body", func(i *grokidp.IdP) { i.TruncateTokenBody = true }, subscription.GrokReasonTokenUncertain},
		{"malformed success", func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` }, subscription.GrokReasonTokenResponse},
		{"not json", func(i *grokidp.IdP) { i.TokenBody = `not json` }, subscription.GrokReasonTokenResponse},
		{"zero expiry", func(i *grokidp.IdP) {
			i.TokenBody = `{"access_token":"x","token_type":"Bearer","expires_in":0}`
		}, subscription.GrokReasonTokenResponse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idp := grokidp.New(t)
			client, _, _ := loggedIn(t, idp, time.Now())
			c.setup(idp)
			before := idp.TokenRequests()
			_, err := client.Refresh(context.Background(), []byte("fake-refresh-token"))
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != c.reason {
				t.Fatalf("reason = %v, %v", reason, err)
			}
			if idp.TokenRequests() != before+1 {
				t.Fatal("refresh was sent more than once")
			}
		})
	}
	t.Run("transport failure", func(t *testing.T) {
		client := subscription.GrokOAuthClient{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset by 203.0.113.9") })}
		_, err := client.Refresh(context.Background(), []byte("secret-refresh-token"))
		if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain || strings.Contains(err.Error(), "203.0.113.9") || strings.Contains(err.Error(), "secret-refresh-token") {
			t.Fatalf("transport failure: %v", err)
		}
	})
	t.Run("replayed rotated token", func(t *testing.T) {
		idp := grokidp.New(t)
		client, _, _ := loggedIn(t, idp, time.Now())
		if _, err := client.Refresh(context.Background(), []byte("fake-refresh-token")); err != nil {
			t.Fatal(err)
		}
		_, err := client.Refresh(context.Background(), []byte("fake-refresh-token"))
		if reason, _ := subscription.GrokOAuthFailure(err); reason != subscription.GrokReasonTokenRejected {
			t.Fatal("the provider accepted a rotated-away token")
		}
	})
}

func TestGrokRefreshCarriesOverProfileFieldsTheProviderOmits(t *testing.T) {
	idp := grokidp.New(t)
	idp.TeamID = "team-9"
	client, endpoints, bundle := loggedIn(t, idp, time.Now().Add(-5*time.Hour))
	idp.TeamID, idp.Email, idp.FirstName = "", "", ""
	next, err := refreshOnce(t, client, endpoints, bundle, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	file, _ := next.AuthFile()
	for _, want := range []string{`"team_id":"team-9"`, `"email":"fixture@example.invalid"`, `"first_name":"Fixture"`} {
		if !strings.Contains(string(file), want) {
			t.Fatalf("refresh dropped %s", want)
		}
	}
	if next.Identity().TeamID != "team-9" {
		t.Fatal("identity lost its team")
	}
}
