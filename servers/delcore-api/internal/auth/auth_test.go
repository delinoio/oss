// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/delinoio/oss/servers/delcore-api/internal/config"
	"github.com/golang-jwt/jwt/v4"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogtoValidation(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	verifier, e := New(context.Background(), config.Config{Issuer: issuer, Audience: "delcore", OperatorRole: "operator", IdentityKey: "fixture-only-identity-key-32-bytes", TimeoutSeconds: 2})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		change func(jwt.MapClaims)
		ok     bool
	}{{"user", func(c jwt.MapClaims) {}, true}, {"operator", func(c jwt.MapClaims) { c["roles"] = []string{"operator"} }, true}, {"service", func(c jwt.MapClaims) { c["client_id"] = "service"; c["sub"] = "service"; delete(c, "email_verified") }, true}, {"delegated", func(c jwt.MapClaims) { c["client_id"] = "service" }, true}, {"delegated_unverified", func(c jwt.MapClaims) { c["client_id"] = "service"; delete(c, "email_verified") }, false}, {"issuer", func(c jwt.MapClaims) { c["iss"] = "https://other.example.test" }, false}, {"audience", func(c jwt.MapClaims) { c["aud"] = "other" }, false}, {"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, false}, {"unverified", func(c jwt.MapClaims) { c["email_verified"] = false }, false}} {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{"iss": issuer, "aud": "delcore", "sub": "user", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "user@example.test", "email_verified": true}
			tc.change(claims)
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test"
			raw, e := token.SignedString(key)
			if e != nil {
				t.Fatal(e)
			}
			id, e := verifier.Verify(context.Background(), "Bearer "+raw)
			if (e == nil) != tc.ok {
				t.Fatal(id, e)
			}
			if tc.name == "operator" && !id.Operator {
				t.Fatal("operator role missing")
			}
			if tc.name == "delegated" && (id.ClientID != "" || id.SubjectHash == "") {
				t.Fatal("delegated user became M2M")
			}
			if tc.name == "service" && id.SubjectHash != "" {
				t.Fatal("M2M became a user")
			}
		})
	}
}
