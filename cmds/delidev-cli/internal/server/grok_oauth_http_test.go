// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/golang-jwt/jwt/v4"
)

func TestGrokBrowserProfilePinsOriginalIPv4PKCE(t *testing.T) {
	verifier := []byte(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{21}, 32)))
	state, nonce := strings.Repeat("s", 32), strings.Repeat("n", 32)
	text, err := grokAuthorizeURL("http://127.0.0.1:42311/callback", verifier, state, nonce)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(text)
	q := u.Query()
	if u.Scheme != "https" || u.Host != "auth.x.ai" || u.Path != "/oauth2/authorize" || q.Get("client_id") != subscription.GrokClientID || q.Get("state") != state || q.Get("nonce") != nonce || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == string(verifier) || q.Get("redirect_uri") != "http://127.0.0.1:42311/callback" {
		t.Fatal("browser profile changed its original authority")
	}
	for _, callback := range []string{"http://localhost:42311/callback", "http://[::1]:42311/callback", "http://127.1:42311/callback", "http://127.0.0.1:042311/callback", "http://127.0.0.1:0/callback", "http://127.0.0.1:42311/callback?", "http://127.0.0.1:42311/%63allback", "http://user@127.0.0.1:42311/callback", "http://127.0.0.1:42311/callback#code"} {
		if _, err := grokAuthorizeURL(callback, verifier, state, nonce); err == nil {
			t.Fatal("foreign callback acquired browser authority")
		}
	}
}

func TestGrokOAuthHTTPRejectsRedirectsAndUncertainResend(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusBadGateway} {
		calls := 0
		c := grokOAuthHTTP{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != grokTokenEndpoint || r.GetBody != nil || r.Header.Get("Cookie") != "" || r.Header.Get("X-Grok-Client-Version") != subscription.GrokVersion {
				t.Error("token request authority changed")
			}
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("code") != "fixture + code" {
				t.Error("exact code bytes changed")
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://foreign.invalid/token"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}
		result, err := c.token(context.Background(), map[string][]byte{"code": []byte("fixture + code")}, "", "")
		if err == nil || calls != 1 || len(result.access) != 0 || len(result.refresh) != 0 {
			t.Fatal("an uncertain operation retried or published material")
		}
	}
	if _, _, err := (grokOAuthHTTP{}).request(context.Background(), http.MethodPost, grokTokenEndpoint, nil, nil); err == nil {
		t.Fatal("missing explicit route fell back to ambient networking")
	}
}

func TestGrokIDTokenValidatesSignatureIssuerAudienceNonceAndTime(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": "fixture-key", "x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}}})
	c := grokOAuthHTTP{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != grokJWKS || r.Header.Get("Authorization") != "" {
			t.Error("JWKS lookup changed authority")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(jwks)), Header: make(http.Header)}, nil
	})}
	for _, scenario := range []string{"valid", "issuer", "audience", "nonce", "expired", "future-issued", "azp", "multiple-audiences", "signature", "unknown-kid", "missing-kid"} {
		t.Run(scenario, func(t *testing.T) {
			claims := jwt.MapClaims{"iss": subscription.GrokIssuer, "aud": subscription.GrokClientID, "sub": "fixture-user", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": "fixture-nonce"}
			switch scenario {
			case "issuer":
				claims["iss"] = "https://foreign.invalid"
			case "audience":
				claims["aud"] = "different-client"
			case "nonce":
				claims["nonce"] = "different-nonce"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "future-issued":
				claims["iat"] = time.Now().Add(time.Hour).Unix()
			case "azp":
				claims["azp"] = "different-client"
			case "multiple-audiences":
				claims["aud"] = []string{subscription.GrokClientID, "another-client"}
			}
			token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
			token.Header["kid"] = "fixture-key"
			if scenario == "unknown-kid" {
				token.Header["kid"] = "foreign-key"
			}
			if scenario == "missing-kid" {
				delete(token.Header, "kid")
			}
			material, err := token.SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "signature" {
				other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				material, _ = token.SignedString(other)
			}
			identity, err := c.verifyID(context.Background(), []byte(material), []byte("fixture-access"), "fixture-nonce")
			if scenario == "valid" {
				if err != nil || identity.User != "fixture-user" || identity.Service != domain.SubscriptionGrok {
					t.Fatalf("valid signed identity rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid signed identity acquired account authority")
			}
		})
	}
}

func TestGrokJWKSRejectsUnboundedDuplicatePrivateAndForeignKeys(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"iss": subscription.GrokIssuer, "aud": subscription.GrokClientID, "sub": "fixture-user", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": "fixture-nonce"}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = "fixture-key"
	material, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"duplicate-id", "private", "algorithm", "use", "oversized", "duplicate-member", "missing-key"} {
		t.Run(scenario, func(t *testing.T) {
			jwk := map[string]any{"kty": "EC", "crv": "P-256", "use": "sig", "alg": "ES256", "kid": "fixture-key", "x": base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}
			keys := []any{jwk}
			switch scenario {
			case "duplicate-id":
				keys = append(keys, jwk)
			case "private":
				jwk["d"] = base64.RawURLEncoding.EncodeToString(key.D.FillBytes(make([]byte, 32)))
			case "algorithm":
				jwk["alg"] = "ES384"
			case "use":
				jwk["use"] = "enc"
			case "missing-key":
				jwk["kid"] = "another-key"
			}
			raw, _ := json.Marshal(map[string]any{"keys": keys})
			if scenario == "oversized" {
				raw = []byte(`{"keys":[],"extra":"` + strings.Repeat("a", 64<<10) + `"}`)
			}
			if scenario == "duplicate-member" {
				raw = []byte(`{"keys":[],"keys":[]}`)
			}
			calls := 0
			c := grokOAuthHTTP{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}
			if _, err := c.verifyID(context.Background(), []byte(material), []byte("fixture-access"), "fixture-nonce"); err == nil || calls != 1 {
				t.Fatal("invalid keys acquired verification or retry authority")
			}
		})
	}
}
