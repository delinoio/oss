// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription/grokidp"
)

func grokClient(idp *grokidp.IdP) subscription.GrokOAuthClient {
	return subscription.GrokOAuthClient{Transport: idp.Transport}
}

func TestGrokOAuthProfileIsPinned(t *testing.T) {
	if subscription.GrokIssuer != "https://auth.x.ai" || subscription.GrokClientID != "b1a00492-073a-47ea-816f-4c329264a828" || subscription.GrokReferrer != "grok-build" ||
		subscription.GrokCallbackHost != "127.0.0.1" || subscription.GrokCallbackPath != "/callback" || subscription.GrokMinCallbackPort != 1024 || subscription.GrokOpaqueValueMax != 512 {
		t.Fatal("fixed Grok OAuth profile drifted")
	}
	if subscription.GrokScope() != "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write" {
		t.Fatal("Grok scope list drifted")
	}
	if subscription.GrokRedirectURI(43817) != "http://127.0.0.1:43817/callback" {
		t.Fatal("redirect URI shape drifted")
	}
}

func TestGrokDiscoveryIsValidatedAgainstTheIssuer(t *testing.T) {
	idp := grokidp.New(t)
	endpoints, err := grokClient(idp).Discover(context.Background())
	if err != nil || endpoints.Authorization != "https://auth.x.ai/oauth2/authorize" || endpoints.Token != "https://auth.x.ai/oauth2/token" ||
		endpoints.UserInfo != "https://auth.x.ai/oauth2/userinfo" || endpoints.JWKS != "https://auth.x.ai/.well-known/jwks.json" {
		t.Fatalf("discovery: %+v %v", endpoints, err)
	}
	for name, mutate := range map[string]func(*grokidp.IdP){
		"issuer mismatch":  func(i *grokidp.IdP) { i.DiscoveryIssuer = "https://auth.x.ai/" },
		"foreign endpoint": func(i *grokidp.IdP) { i.DiscoveryHost = "evil.example" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := grokidp.New(t)
			mutate(bad)
			_, err := grokClient(bad).Discover(context.Background())
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonDiscovery {
				t.Fatalf("discovery accepted %s: %v", name, err)
			}
		})
	}
}

// Provider member names are case-sensitive: a differently cased spelling of a
// member the client reads is an alias the contract does not define, so a
// discovery document that carries one is refused in every cell instead of being
// matched the way encoding/json folds struct field names.
func TestGrokDiscoveryRequiresExactMemberNames(t *testing.T) {
	const base = "https://auth.x.ai"
	members := []struct{ name, value string }{
		{"issuer", `"` + subscription.GrokIssuer + `"`},
		{"authorization_endpoint", `"` + base + `/oauth2/authorize"`},
		{"token_endpoint", `"` + base + `/oauth2/token"`},
		{"userinfo_endpoint", `"` + base + `/oauth2/userinfo"`},
		{"jwks_uri", `"` + base + `/.well-known/jwks.json"`},
		{"response_types_supported", `["code"]`},
		{"code_challenge_methods_supported", `["S256"]`},
		{"token_endpoint_auth_methods_supported", `["none"]`},
		{"id_token_signing_alg_values_supported", `["ES256"]`},
	}
	document := func(rename func(name string) string, extra string) string {
		body := "{"
		for i, m := range members {
			if i > 0 {
				body += ","
			}
			body += `"` + rename(m.name) + `":` + m.value
		}
		return body + extra + "}"
	}
	exact := func(name string) string { return name }
	discover := func(body string) error {
		idp := grokidp.New(t)
		idp.DiscoveryBody = body
		_, err := grokClient(idp).Discover(context.Background())
		return err
	}
	if err := discover(document(exact, "")); err != nil {
		t.Fatalf("the control document was refused: %v", err)
	}
	if err := discover(document(exact, `,"Unrelated":1`)); err != nil {
		t.Fatalf("an unrelated member was refused: %v", err)
	}
	for _, m := range members {
		for _, alias := range []string{strings.ToUpper(m.name), strings.ToUpper(m.name[:1]) + m.name[1:]} {
			t.Run("only "+alias, func(t *testing.T) {
				body := document(func(name string) string {
					if name == m.name {
						return alias
					}
					return name
				}, "")
				reason, ok := subscription.GrokOAuthFailure(discover(body))
				if !ok || reason != subscription.GrokReasonDiscovery {
					t.Fatalf("a document that spells %s as %s was accepted", m.name, alias)
				}
			})
			t.Run("beside "+alias, func(t *testing.T) {
				reason, ok := subscription.GrokOAuthFailure(discover(document(exact, `,"`+alias+`":`+m.value)))
				if !ok || reason != subscription.GrokReasonDiscovery {
					t.Fatalf("a document that adds the alias %s beside %s was accepted", alias, m.name)
				}
			})
		}
	}
}

func TestGrokAuthorizationURLShapeAndRandomness(t *testing.T) {
	idp := grokidp.New(t)
	endpoints, err := grokClient(idp).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Clear()
	if len(a.Verifier) != 43 || len(a.State) != 43 || len(a.Nonce) != 43 || len(a.Challenge) != 43 || !subscription.GrokOpaqueValue(a.State) || !subscription.GrokOpaqueValue(a.Nonce) || a.State == a.Nonce {
		t.Fatal("authorization material has the wrong shape")
	}
	raw := endpoints.AuthorizationURL(subscription.GrokRedirectURI(43817), a)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "https" || u.Host != "auth.x.ai" || u.Path != "/oauth2/authorize" || len(q) != 9 || q.Get("response_type") != "code" || q.Get("client_id") != subscription.GrokClientID ||
		q.Get("redirect_uri") != "http://127.0.0.1:43817/callback" || q.Get("scope") != subscription.GrokScope() || q.Get("code_challenge_method") != "S256" || q.Get("referrer") != "grok-build" ||
		q.Get("code_challenge") != a.Challenge || q.Get("state") != a.State || q.Get("nonce") != a.Nonce || strings.Contains(u.RawQuery, "+") {
		t.Fatalf("authorization URL drifted: %s", u.Redacted())
	}
	state, port, ok := subscription.GrokAuthorizationDetails(raw)
	if !ok || state != a.State || port != 43817 {
		t.Fatal("the server-produced URL fails its own receiver-compatible validator")
	}
	other, _ := subscription.NewGrokAuthorization(nil)
	defer other.Clear()
	if other.State == a.State || other.Nonce == a.Nonce || bytes.Equal(other.Verifier, a.Verifier) {
		t.Fatal("authorization material repeated")
	}
	for name, bad := range map[string]string{
		"extra key":       raw + "&extra=1",
		"low port":        strings.Replace(raw, "%3A43817", "%3A80", 1),
		"wrong host":      strings.Replace(raw, "auth.x.ai", "auth.example", 1),
		"duplicate state": raw + "&state=" + a.State,
		"localhost":       strings.Replace(raw, "127.0.0.1", "localhost", 1),
		"wrong path":      strings.Replace(raw, "%2Fcallback", "%2Fother", 1),
	} {
		if _, _, ok := subscription.GrokAuthorizationDetails(bad); ok {
			t.Fatalf("validator accepted %s", name)
		}
	}
}

// exchangeFixture drives one approved authorization against the fake provider.
func exchangeFixture(t *testing.T, idp *grokidp.IdP) (subscription.GrokOAuthClient, subscription.GrokEndpoints, subscription.GrokAuthorization, grokidp.Grant) {
	t.Helper()
	client := grokClient(idp)
	endpoints, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Clear)
	grant := idp.Approve(t, endpoints.AuthorizationURL(subscription.GrokRedirectURI(43817), a))
	return client, endpoints, a, grant
}

func TestGrokExchangeIsAPublicClientWithPKCEAndMapsToABundle(t *testing.T) {
	idp := grokidp.New(t)
	idp.PrincipalType, idp.PrincipalID, idp.TeamID = "team-member", "principal-77", "team-9"
	client, endpoints, a, grant := exchangeFixture(t, idp)
	tokens, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	form := idp.LastTokenForm()
	if idp.TokenRequests() != 1 || form.Get("grant_type") != "authorization_code" || form.Get("client_id") != subscription.GrokClientID || form.Get("client_secret") != "" || idp.LastTokenHeaders().Get("Authorization") != "" || form.Get("code_verifier") == "" {
		t.Fatal("token request is not a PKCE public-client exchange")
	}
	claims, err := client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	bundle, err := subscription.BuildGrokBundle(now, tokens, claims, info)
	if err != nil {
		t.Fatal(err)
	}
	identity := bundle.Identity()
	if identity.UserID != "fake-user-1" || identity.PrincipalType != "team-member" || identity.PrincipalID != "principal-77" || identity.TeamID != "team-9" || !identity.Expiry.Equal(now.Add(6*time.Hour)) {
		t.Fatal("identity mapping drifted")
	}
	file, err := bundle.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"key":"fake-access-token"`, `"refresh_token":"fake-refresh-token"`, `"email":"fixture@example.invalid"`, `"first_name":"Fixture"`, `"oidc_issuer":"https://auth.x.ai"`, `"auth_mode":"oidc"`} {
		if !strings.Contains(string(file), want) {
			t.Fatalf("auth.json lacks %s", want)
		}
	}
	// A second redemption of the same code is refused by the provider.
	if _, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI); err == nil {
		t.Fatal("code was redeemable twice")
	}
}

func TestGrokMappingFallsBackToTheUserPrincipalAndRefusesHalfPrincipals(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints, a, grant := exchangeFixture(t, idp)
	tokens, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	info, _ := client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims)
	bundle, err := subscription.BuildGrokBundle(time.Now(), tokens, claims, info)
	if err != nil || bundle.Identity().PrincipalType != "user" || bundle.Identity().PrincipalID != "fake-user-1" {
		t.Fatalf("documented principal fallback drifted: %v", err)
	}
	half := grokidp.New(t)
	half.PrincipalType = "user"
	client, endpoints, a, grant = exchangeFixture(t, half)
	tokens, _ = client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	claims, _ = client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	info, _ = client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims)
	if _, err := subscription.BuildGrokBundle(time.Now(), tokens, claims, info); err == nil {
		t.Fatal("a principal type without an identifier was accepted")
	}
}

func TestGrokIDTokenNegativeMatrix(t *testing.T) {
	cases := map[string]func(*grokidp.IdP){
		"wrong nonce":    func(i *grokidp.IdP) { i.OverrideNonce = "attacker-chosen-nonce" },
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
			client, endpoints, a, grant := exchangeFixture(t, idp)
			tokens, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonIDToken {
				t.Fatalf("ID token accepted: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), tokens.IDToken) || strings.Contains(err.Error(), a.Nonce)) {
				t.Fatal("error leaked a token or nonce")
			}
		})
	}
}

func TestGrokExchangeClassifiesRejectedAndUncertainOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*grokidp.IdP)
		reason subscription.GrokOAuthReason
	}{
		{"invalid_grant", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 400, `{"error":"invalid_grant"}` }, subscription.GrokReasonTokenRejected},
		{"server error", func(i *grokidp.IdP) { i.TokenStatus, i.TokenBody = 503, `unavailable` }, subscription.GrokReasonTokenUncertain},
		{"redirect", func(i *grokidp.IdP) { i.TokenStatus = 302 }, subscription.GrokReasonTokenUncertain},
		{"malformed success", func(i *grokidp.IdP) { i.TokenBody = `{"access_token":"x"}` }, subscription.GrokReasonTokenResponse},
		{"missing refresh token", func(i *grokidp.IdP) { i.NoRefreshToken = true }, subscription.GrokReasonTokenResponse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idp := grokidp.New(t)
			c.setup(idp)
			client, endpoints, a, grant := exchangeFixture(t, idp)
			_, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
			if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != c.reason {
				t.Fatalf("reason = %v, %v", reason, err)
			}
			if idp.TokenRequests() != 1 {
				t.Fatal("exchange was sent more than once")
			}
		})
	}
	t.Run("transport failure", func(t *testing.T) {
		client := subscription.GrokOAuthClient{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })}
		_, err := client.ExchangeCode(context.Background(), subscription.GrokEndpoints{Token: "https://auth.x.ai/oauth2/token"}, []byte("code"), []byte("verifier"), "http://127.0.0.1:43817/callback")
		if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenUncertain || strings.Contains(err.Error(), "reset") {
			t.Fatalf("transport failure: %v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGrokUserInfoSubjectMustMatch(t *testing.T) {
	idp := grokidp.New(t)
	idp.UserInfoSubject = "someone-else"
	client, endpoints, a, grant := exchangeFixture(t, idp)
	tokens, _ := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	claims, err := client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UserInfo(context.Background(), endpoints, tokens.AccessToken, claims); err == nil {
		t.Fatal("a userinfo subject mismatch was accepted")
	}
}

func TestGrokOAuthValuesAreRedactedWhenFormatted(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints, a, grant := exchangeFixture(t, idp)
	tokens, _ := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), a.Verifier, grant.RedirectURI)
	claims, _ := client.ValidateIDToken(context.Background(), endpoints, tokens.IDToken, a.Nonce)
	for _, value := range []any{tokens, &tokens, claims, a} {
		for _, text := range []string{fmtValue("%v", value), fmtValue("%+v", value), fmtValue("%#v", value)} {
			for _, secret := range []string{tokens.AccessToken, tokens.RefreshToken, tokens.IDToken, a.State, a.Nonce, string(a.Verifier)} {
				if secret != "" && strings.Contains(text, secret) {
					t.Fatalf("formatted value leaked a secret: %s", text)
				}
			}
		}
	}
}

func fmtValue(format string, value any) string { return fmt.Sprintf(format, value) }

func TestGrokPKCEIsEnforcedOnBothSides(t *testing.T) {
	idp := grokidp.New(t)
	client, endpoints, a, grant := exchangeFixture(t, idp)
	// A verifier that does not match the challenge is rejected by the provider.
	_, err := client.ExchangeCode(context.Background(), endpoints, []byte(grant.Code), []byte("a-different-verifier-value-that-does-not-match"), grant.RedirectURI)
	if reason, ok := subscription.GrokOAuthFailure(err); !ok || reason != subscription.GrokReasonTokenRejected {
		t.Fatalf("wrong verifier: %v", err)
	}
	// A URL without a challenge, or with a non-S256 method, is never published.
	good := endpoints.AuthorizationURL(subscription.GrokRedirectURI(43817), a)
	for name, bad := range map[string]string{
		"no challenge": strings.Replace(good, "code_challenge="+a.Challenge+"&", "", 1),
		"plain method": strings.Replace(good, "code_challenge_method=S256", "code_challenge_method=plain", 1),
	} {
		if _, _, ok := subscription.GrokAuthorizationDetails(bad); ok {
			t.Fatalf("validator accepted %s", name)
		}
	}
}
