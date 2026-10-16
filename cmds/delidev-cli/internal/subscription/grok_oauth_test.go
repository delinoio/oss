// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

const grokTestRedirect = "http://127.0.0.1:49152/callback"

type grokFlow struct {
	idp       *grokidptest.IdP
	profile   subscription.GrokOAuthProfile
	endpoints subscription.GrokEndpoints
	auth      *subscription.GrokAuthorization
	callback  string
}

func newGrokFlow(t *testing.T) *grokFlow {
	t.Helper()
	f := &grokFlow{idp: grokidptest.New(), profile: subscription.DefaultGrokOAuthProfile()}
	var err error
	if f.endpoints, err = subscription.DiscoverGrokEndpoints(context.Background(), f.idp.Client(), f.profile); err != nil {
		t.Fatal(err)
	}
	if f.auth, err = subscription.NewGrokAuthorization(nil); err != nil {
		t.Fatal(err)
	}
	authorization := f.profile.AuthorizationURL(f.endpoints, grokTestRedirect, f.auth)
	if f.callback, err = f.idp.Authorize(authorization); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *grokFlow) exchange() (subscription.GrokTokens, error) {
	return subscription.ExchangeGrokCode(context.Background(), f.idp.Client(), f.profile, f.endpoints, grokidptest.Code(f.callback), f.auth.Verifier(), grokTestRedirect)
}

func TestGrokAuthorizationURLHasTheObservedShape(t *testing.T) {
	f := newGrokFlow(t)
	raw := f.profile.AuthorizationURL(f.endpoints, grokTestRedirect, f.auth)
	prefix := "https://auth.x.ai/oauth2/authorize?response_type=code&client_id=b1a00492-073a-47ea-816f-4c329264a828&redirect_uri=http%3A%2F%2F127.0.0.1%3A49152%2Fcallback&scope=openid%20profile%20email%20offline_access%20grok-cli%3Aaccess%20api%3Aaccess%20conversations%3Aread%20conversations%3Awrite%20workspaces%3Aread%20workspaces%3Awrite&code_challenge="
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, "&referrer=grok-build") || strings.Contains(raw, "+") {
		t.Fatal("authorization URL differs from the observed shape")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if len(q) != 9 || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") != f.auth.State || q.Get("nonce") != f.auth.Nonce || q.Get("redirect_uri") != grokTestRedirect {
		t.Fatal("authorization query is wrong")
	}
	if f.auth.State == f.auth.Nonce || len(f.auth.State) != 43 || len(f.auth.Verifier()) != 43 {
		t.Fatal("state, nonce and verifier must be distinct 43-character values")
	}
}

func TestGrokAuthorizationClearsVerifier(t *testing.T) {
	a, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		t.Fatal(err)
	}
	view := a.Verifier()
	a.Clear()
	for _, b := range view {
		if b != 0 {
			t.Fatal("verifier bytes were not overwritten")
		}
	}
	if a.Verifier() != nil {
		t.Fatal("verifier reference was retained")
	}
}

func TestGrokDiscoveryValidatesTheIssuerAndEndpoints(t *testing.T) {
	profile := subscription.DefaultGrokOAuthProfile()
	for name, serve := range map[string]func(w http.ResponseWriter){
		"issuer": func(w http.ResponseWriter) { fmt.Fprint(w, discoveryJSON(`"issuer":"https://evil.example"`)) },
		"foreign": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"token_endpoint":"https://evil.example/oauth2/token"`))
		},
		"plain": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"code_challenge_methods_supported":["S256","plain"]`))
		},
		"no-s256": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"code_challenge_methods_supported":["plain"]`))
		},
		"no-none": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"token_endpoint_auth_methods_supported":["client_secret_basic"]`))
		},
		"alg": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"id_token_signing_alg_values_supported":["RS256"]`))
		},
		"http": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"jwks_uri":"http://auth.x.ai/.well-known/jwks.json"`))
		},
		"query": func(w http.ResponseWriter) {
			fmt.Fprint(w, discoveryJSON(`"userinfo_endpoint":"https://auth.x.ai/oauth2/userinfo?x=1"`))
		},
		"not-json":  func(w http.ResponseWriter) { fmt.Fprint(w, "<html>") },
		"not-found": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) },
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return recorded(serve), nil })}
			_, err := subscription.DiscoverGrokEndpoints(context.Background(), client, profile)
			if reason, _ := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonDiscovery {
				t.Fatalf("discovery accepted %s: %v", name, err)
			}
		})
	}
}

func TestGrokExchangeIsAPublicClientRequest(t *testing.T) {
	f := newGrokFlow(t)
	tokens, err := f.exchange()
	if err != nil {
		t.Fatal(err)
	}
	form := f.idp.TokenFormSeen[0]
	if f.idp.TokenRequests != 1 || form.Get("grant_type") != "authorization_code" || form.Get("client_id") != subscription.GrokClientID || form.Get("redirect_uri") != grokTestRedirect || form.Has("client_secret") || f.idp.TokenAuthHeaders[0] != "" {
		t.Fatal("token request is not the public-client authorization-code grant")
	}
	if tokens.ExpiresIn != 6*time.Hour || strings.Contains(fmt.Sprintf("%v %+v %#v %s", tokens, tokens, tokens, tokens), f.idp.AccessToken) {
		t.Fatal("token response was mishandled or leaked through a formatter")
	}
}

func TestGrokExchangeClassifiesOutcomes(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		f := newGrokFlow(t)
		f.idp.TokenStatus = http.StatusBadRequest
		_, err := f.exchange()
		if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenRejected || uncertain {
			t.Fatalf("a 4xx answer is a definitive rejection: %v", err)
		}
	})
	t.Run("server-error", func(t *testing.T) {
		f := newGrokFlow(t)
		f.idp.TokenStatus = http.StatusBadGateway
		_, err := f.exchange()
		if _, uncertain := subscription.GrokOAuthReasonOf(err); !uncertain {
			t.Fatal("a 5xx answer must stay uncertain")
		}
	})
	t.Run("transport", func(t *testing.T) {
		f := newGrokFlow(t)
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })}
		_, err := subscription.ExchangeGrokCode(context.Background(), client, f.profile, f.endpoints, "code", f.auth.Verifier(), grokTestRedirect)
		if _, uncertain := subscription.GrokOAuthReasonOf(err); !uncertain || strings.Contains(err.Error(), "connection reset") {
			t.Fatal("a transport error must be uncertain and value-free")
		}
	})
	t.Run("bad-success", func(t *testing.T) {
		f := newGrokFlow(t)
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return recorded(func(w http.ResponseWriter) { fmt.Fprint(w, `{"access_token":"a","token_type":"mac","expires_in":5}`) }), nil
		})}
		_, err := subscription.ExchangeGrokCode(context.Background(), client, f.profile, f.endpoints, "code", f.auth.Verifier(), grokTestRedirect)
		if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenResponse || uncertain {
			t.Fatalf("incomplete success was accepted: %v", err)
		}
	})
}

func TestGrokIDTokenValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*grokFlow)
		reason subscription.GrokOAuthReason
	}{
		{"valid", func(*grokFlow) {}, ""},
		{"signature", func(f *grokFlow) { f.idp.SignWithOtherKey = true }, subscription.GrokReasonSignature},
		{"issuer", func(f *grokFlow) { f.idp.IDTokenIssuer = "https://evil.example" }, subscription.GrokReasonIssuer},
		{"audience", func(f *grokFlow) { f.idp.IDTokenAudience = "another-client" }, subscription.GrokReasonAudience},
		{"nonce", func(f *grokFlow) { f.idp.NonceOverride = "another-nonce" }, subscription.GrokReasonNonce},
		{"expired", func(f *grokFlow) { f.idp.IDTokenLifetime = -time.Minute }, subscription.GrokReasonExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGrokFlow(t)
			c.mutate(f)
			tokens, err := f.exchange()
			if err != nil {
				t.Fatal(err)
			}
			keys, err := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := subscription.VerifyGrokIDToken(tokens.IDToken, keys, f.profile, f.auth.Nonce, time.Now())
			if c.reason == "" {
				if err != nil || claims.Subject != f.idp.Subject {
					t.Fatalf("valid token rejected: %v", err)
				}
				return
			}
			if reason, _ := subscription.GrokOAuthReasonOf(err); reason != c.reason {
				t.Fatalf("got %q want %q", reason, c.reason)
			}
		})
	}
}

func TestGrokIDTokenRejectsOtherAlgorithmsAndUnknownKeys(t *testing.T) {
	f := newGrokFlow(t)
	tokens, _ := f.exchange()
	keys, _ := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	for name, raw := range map[string]string{
		"none":      "eyJhbGciOiJub25lIiwia2lkIjoiZmFrZS1lczI1Ni1rZXkifQ.e30.",
		"hs256":     "eyJhbGciOiJIUzI1NiIsImtpZCI6ImZha2UtZXMyNTYta2V5In0.e30.AAAA",
		"garbage":   "not-a-jwt",
		"truncated": tokens.IDToken[:len(tokens.IDToken)-4],
	} {
		if _, err := subscription.VerifyGrokIDToken(raw, keys, f.profile, f.auth.Nonce, time.Now()); err == nil {
			t.Fatalf("%s token was accepted", name)
		}
	}
	if _, err := subscription.VerifyGrokIDToken(tokens.IDToken, nil, f.profile, f.auth.Nonce, time.Now()); err == nil {
		t.Fatal("missing keys were accepted")
	}
}

func TestGrokBundleMappingRoundTripsThroughTheBundleLibrary(t *testing.T) {
	f := newGrokFlow(t)
	tokens, err := f.exchange()
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	now := time.Now()
	claims, err := subscription.VerifyGrokIDToken(tokens.IDToken, keys, f.profile, f.auth.Nonce, now)
	if err != nil {
		t.Fatal(err)
	}
	info, err := subscription.FetchGrokUserinfo(context.Background(), f.idp.Client(), f.endpoints, tokens.AccessToken, claims)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := subscription.BuildGrokBundle(tokens, claims, info, now)
	if err != nil {
		t.Fatal(err)
	}
	identity := bundle.Identity()
	if identity.UserID != "fake-user-1" || identity.PrincipalType != "user" || identity.PrincipalID != "fake-principal-1" || identity.TeamID != "fake-team-1" || !identity.Expiry.After(now.Add(5*time.Hour)) {
		t.Fatal("bundle identity is wrong")
	}
	raw, err := bundle.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"oidc_issuer":"https://auth.x.ai"`) || !strings.Contains(string(raw), `"auth_mode":"oidc"`) || !strings.Contains(string(raw), subscription.GrokScopeKey) {
		t.Fatal("auth.json lacks the pinned issuer, mode or scope key")
	}
	parsed, err := subscription.ParseGrokAuthFile(raw)
	if err != nil || !parsed.Equal(bundle) {
		t.Fatalf("auth.json does not round-trip: %v", err)
	}
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	sealed, err := subscription.SealGrokBundle(server, account, generation, bundle)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := subscription.OpenGrokBundle(sealed, server, account, generation)
	if err != nil || !opened.Equal(bundle) {
		t.Fatalf("sealed bundle does not round-trip: %v", err)
	}
	if text := fmt.Sprintf("%v %+v %#v", bundle, claims, info); !f.idp.Redacted(text) {
		t.Fatal("a formatter leaked token material")
	}
}

func TestGrokMappingFailsClosedWithoutAPrincipalOrOnSubjectMismatch(t *testing.T) {
	f := newGrokFlow(t)
	f.idp.PrincipalID = ""
	tokens, _ := f.exchange()
	keys, _ := subscription.FetchGrokJWKS(context.Background(), f.idp.Client(), f.endpoints)
	claims, err := subscription.VerifyGrokIDToken(tokens.IDToken, keys, f.profile, f.auth.Nonce, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	info, err := subscription.FetchGrokUserinfo(context.Background(), f.idp.Client(), f.endpoints, tokens.AccessToken, claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscription.BuildGrokBundle(tokens, claims, info, time.Now()); err == nil {
		t.Fatal("a bundle without a principal was built")
	}
	g := newGrokFlow(t)
	g.idp.UserinfoSubject = "someone-else"
	tokens, _ = g.exchange()
	keys, _ = subscription.FetchGrokJWKS(context.Background(), g.idp.Client(), g.endpoints)
	claims, _ = subscription.VerifyGrokIDToken(tokens.IDToken, keys, g.profile, g.auth.Nonce, time.Now())
	if _, err := subscription.FetchGrokUserinfo(context.Background(), g.idp.Client(), g.endpoints, tokens.AccessToken, claims); err == nil {
		t.Fatal("a userinfo subject mismatch was accepted")
	}
}

func discoveryJSON(override string) string {
	fields := map[string]string{
		"issuer": `"issuer":"https://auth.x.ai"`, "authorization_endpoint": `"authorization_endpoint":"https://auth.x.ai/oauth2/authorize"`,
		"token_endpoint": `"token_endpoint":"https://auth.x.ai/oauth2/token"`, "jwks_uri": `"jwks_uri":"https://auth.x.ai/.well-known/jwks.json"`,
		"userinfo_endpoint": `"userinfo_endpoint":"https://auth.x.ai/oauth2/userinfo"`, "response_types_supported": `"response_types_supported":["code"]`,
		"code_challenge_methods_supported": `"code_challenge_methods_supported":["S256"]`, "token_endpoint_auth_methods_supported": `"token_endpoint_auth_methods_supported":["none"]`,
		"id_token_signing_alg_values_supported": `"id_token_signing_alg_values_supported":["ES256"]`,
	}
	key := strings.Trim(strings.SplitN(override, ":", 2)[0], `"`)
	fields[key] = override
	parts := make([]string, 0, len(fields))
	for _, v := range fields {
		parts = append(parts, v)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestGrokPKCEIsEnforcedByTheFakeAndTheExchange(t *testing.T) {
	f := newGrokFlow(t)
	authorization := f.profile.AuthorizationURL(f.endpoints, grokTestRedirect, f.auth)
	u, _ := url.Parse(authorization)
	q := u.Query()
	q.Del("code_challenge")
	u.RawQuery = q.Encode()
	if _, err := f.idp.Authorize(u.String()); err == nil {
		t.Fatal("an authorization request without a PKCE challenge was accepted")
	}
	_, err := subscription.ExchangeGrokCode(context.Background(), f.idp.Client(), f.profile, f.endpoints, grokidptest.Code(f.callback), []byte("a-different-verifier-value-that-does-not-match"), grokTestRedirect)
	if reason, uncertain := subscription.GrokOAuthReasonOf(err); reason != subscription.GrokReasonTokenRejected || uncertain {
		t.Fatalf("a wrong verifier was not rejected: %v", err)
	}
}
