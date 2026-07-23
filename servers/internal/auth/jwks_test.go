package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Add(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func TestJWKSCacheAndRotation(t *testing.T) {
	t.Parallel()
	key1 := mustRSAKey(t)
	key2 := mustRSAKey(t)
	var requests atomic.Int64
	var document atomic.Value
	document.Store(jwksJSON(t, "key-1", &key1.PublicKey))

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(document.Load().([]byte))
	}))
	defer server.Close()

	clock := &mutableClock{now: time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)}
	source, err := NewJWKS(JWKSConfig{
		URL:      server.URL,
		Client:   server.Client(),
		Clock:    clock,
		CacheTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := source.Key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("initial key lookup: %v", err)
	}
	if _, err := source.Key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("cached key lookup: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("request count after cache hit = %d, want 1", got)
	}

	document.Store(jwksJSON(t, "key-2", &key2.PublicKey))
	rotated, err := source.Key(context.Background(), "key-2", "RS256")
	if err != nil {
		t.Fatalf("rotated key lookup: %v", err)
	}
	if rotated.(*rsa.PublicKey).N.Cmp(key2.N) != 0 {
		t.Fatal("rotated lookup returned the wrong key")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("unknown kid request count = %d, want forced refresh", got)
	}

	clock.Add(2 * time.Hour)
	if _, err := source.Key(context.Background(), "key-2", "RS256"); err != nil {
		t.Fatalf("expired cache refresh: %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("expired cache request count = %d, want 3", got)
	}
}

func TestJWKSRejectsAlgorithmMismatchAndUnsafeURL(t *testing.T) {
	t.Parallel()
	if _, err := NewJWKS(JWKSConfig{URL: "http://tenant.example/jwks"}); err == nil {
		t.Fatal("NewJWKS() accepted non-HTTPS URL")
	}

	entry := jwkEntry{key: &rsa.PublicKey{N: big.NewInt(17), E: 65537}, alg: "RS256"}
	if _, err := matchAlgorithm(entry, "ES256"); err == nil {
		t.Fatal("matchAlgorithm() accepted mismatched algorithm")
	}
}

func jwksJSON(t *testing.T, keyID string, key *rsa.PublicKey) []byte {
	t.Helper()
	exponent := big.NewInt(int64(key.E)).Bytes()
	document := map[string]any{
		"keys": []map[string]string{{
			"kid": keyID,
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(exponent),
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
