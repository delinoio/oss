package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestLogtoVerifier(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := jose.JSONWebKey{Key: &privateKey.PublicKey, KeyID: "test-key", Algorithm: string(jose.RS256), Use: "sig"}
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issuer": issuer, "jwks_uri": issuer + "/jwks",
				"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			_ = json.NewEncoder(response).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	issuer = server.URL

	verifier, err := NewLogtoVerifier(context.Background(), issuer, "devhud-api", [][]byte{[]byte("01234567890123456789012345678901")})
	if err != nil {
		t.Fatal(err)
	}
	valid := signToken(t, privateKey, issuer, "devhud-api", "logto-user", time.Now().Add(time.Hour))
	identity, err := verifier.Verify(context.Background(), "Bearer "+valid)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "logto-user" || identity.DisplayName != "Dev User" || len(identity.Fingerprint) != 32 {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	for _, scheme := range []string{"bearer", "BEARER", "BeArEr"} {
		if _, err := verifier.Verify(context.Background(), scheme+" "+valid); err != nil {
			t.Errorf("scheme %q was rejected: %v", scheme, err)
		}
	}

	invalidAudience := signToken(t, privateKey, issuer, "other", "logto-user", time.Now().Add(time.Hour))
	expired := signToken(t, privateKey, issuer, "devhud-api", "logto-user", time.Now().Add(-time.Hour))
	for name, authorization := range map[string]string{
		"missing":          "",
		"malformed":        "Basic value",
		"missing token":    "Bearer",
		"extra token":      "Bearer " + valid + " extra",
		"invalid audience": "Bearer " + invalidAudience,
		"expired":          "Bearer " + expired,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), authorization); err == nil {
				t.Fatal("Verify succeeded")
			}
		})
	}
}

func TestLogtoVerifierBoundsJWKSRefresh(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	jwksRequested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issuer": issuer, "jwks_uri": issuer + "/jwks",
				"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			jwksRequested <- struct{}{}
			<-request.Context().Done()
			http.Error(response, "temporarily unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	issuer = server.URL

	constructorContext, cancelConstructor := context.WithCancel(context.Background())
	verifier, err := newLogtoVerifier(constructorContext, issuer, "devhud-api", [][]byte{[]byte("01234567890123456789012345678901")}, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cancelConstructor()
	token := signToken(t, privateKey, issuer, "devhud-api", "logto-user", time.Now().Add(time.Hour))
	started := time.Now()
	if _, err := verifier.Verify(context.Background(), "Bearer "+token); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("Verify error = %v, want ErrVerificationUnavailable", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("JWKS refresh took %v, want a bounded verification", elapsed)
	}
	select {
	case <-jwksRequested:
	default:
		t.Fatal("JWKS refresh did not outlive the constructor context")
	}
}

func signToken(t *testing.T, key *rsa.PrivateKey, issuer, audience, subject string, expires time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer: issuer, Subject: subject, Audience: jwt.Audience{audience}, Expiry: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(time.Now()),
	}).Claims(map[string]any{"name": "Dev User", "email": "dev@example.com"}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
