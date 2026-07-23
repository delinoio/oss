package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type httpClientFunc func(*http.Request) (*http.Response, error)

func (f httpClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

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
	for _, rawURL := range []string{
		"http://tenant.example/jwks",
		"https://user@tenant.example/jwks",
		"https://tenant.example/jwks?token=secret",
		"https://tenant.example/jwks#fragment",
	} {
		if _, err := NewJWKS(JWKSConfig{URL: rawURL}); err == nil {
			t.Fatalf("NewJWKS() accepted unsafe URL %q", rawURL)
		}
	}

	entry := jwkEntry{key: &rsa.PublicKey{N: big.NewInt(17), E: 65537}, alg: "RS256"}
	if _, err := matchAlgorithm(entry, "ES256"); err == nil {
		t.Fatal("matchAlgorithm() accepted mismatched algorithm")
	}

	ecTests := []struct {
		name      string
		curve     elliptic.Curve
		algorithm string
		wantError bool
	}{
		{name: "P-256 with ES256", curve: elliptic.P256(), algorithm: "ES256"},
		{name: "P-384 with ES384", curve: elliptic.P384(), algorithm: "ES384"},
		{name: "P-521 with ES512", curve: elliptic.P521(), algorithm: "ES512"},
		{name: "P-256 with ES384", curve: elliptic.P256(), algorithm: "ES384", wantError: true},
		{name: "P-384 with ES512", curve: elliptic.P384(), algorithm: "ES512", wantError: true},
		{name: "P-521 with ES256", curve: elliptic.P521(), algorithm: "ES256", wantError: true},
	}
	for _, test := range ecTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := matchAlgorithm(jwkEntry{key: &ecdsa.PublicKey{Curve: test.curve}}, test.algorithm)
			if (err != nil) != test.wantError {
				t.Fatalf("matchAlgorithm() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestJWKSRefreshDoesNotBlockFreshCachedKeys(t *testing.T) {
	t.Parallel()
	key := mustRSAKey(t)
	document := jwksJSON(t, "key-1", &key.PublicKey)
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
	}
	defer release()

	var requests atomic.Int64
	client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 2 {
			close(refreshStarted)
			<-releaseRefresh
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(document)),
			Request:    request,
		}, nil
	})
	source, err := NewJWKS(JWKSConfig{
		URL:      "https://tenant.example/jwks",
		Client:   client,
		CacheTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Key(context.Background(), "key-1", "RS256"); err != nil {
		t.Fatalf("initial key lookup: %v", err)
	}

	unknownDone := make(chan error, 1)
	go func() {
		_, err := source.Key(context.Background(), "unknown", "RS256")
		unknownDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("unknown-key refresh did not start")
	}

	knownDone := make(chan error, 1)
	go func() {
		_, err := source.Key(context.Background(), "key-1", "RS256")
		knownDone <- err
	}()
	select {
	case err := <-knownDone:
		if err != nil {
			t.Fatalf("fresh cached key lookup: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh cached key lookup blocked on refresh")
	}

	release()
	if err := <-unknownDone; err == nil || errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("unknown key error = %v, want invalid credential error", err)
	}
}

func TestJWKSRejectsHTTPSRedirectToHTTP(t *testing.T) {
	t.Parallel()
	key := mustRSAKey(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(jwksJSON(t, "key-1", &key.PublicKey))
	}))
	defer target.Close()

	redirect := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	source, err := NewJWKS(JWKSConfig{
		URL:    redirect.URL,
		Client: redirect.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Key(context.Background(), "key-1", "RS256"); err == nil {
		t.Fatal("Key() accepted signing keys loaded through an HTTP redirect")
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
