// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type oauthTestTransport func(*http.Request) (*http.Response, error)

func (f oauthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOAuthExchangeUsesOneFixedS256POST(t *testing.T) {
	calls := 0
	const code = " exact-opaque-승인-code "
	const verifier = "fixture-verifier-private-to-server-0123456789"
	client := &http.Client{Transport: oauthTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://openrouter.ai/api/v1/auth/keys" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.GetBody != nil {
			t.Fatal("exchange request escaped its closed contract")
		}
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 3 || payload["code"] != code || payload["code_verifier"] != verifier || payload["code_challenge_method"] != "S256" {
			t.Fatal("exchange changed its opaque inputs")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("missing bounded deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"key":"fixture-returned-api-key","email":"private-provider-identity"}`)), Header: make(http.Header)}, nil
	})}
	key, err := exchangeOpenRouterCode(context.Background(), client, []byte(code), []byte(verifier))
	defer clear(key)
	if err != nil || string(key) != "fixture-returned-api-key" || calls != 1 {
		t.Fatal("valid exchange did not return its one key")
	}
}

func TestOAuthExchangeUnknownOutcomeNeverRetriesOrReflects(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		status int
		body   string
		fail   bool
	}{
		{"lost-response", 0, "", true},
		{"denied", 403, "raw-upstream-secret", false},
		{"redirect", 302, "", false},
		{"missing-key", 200, `{"email":"private-email"}`, false},
		{"malformed", 200, `{"key":`, false},
		{"duplicate", 200, `{"key":"first-secret","key":"second-secret"}`, false},
		{"trailing", 200, `{"key":"fixture-key"} {}`, false},
		{"invalid-key", 200, `{"key":"space invalid"}`, false},
		{"invalid-utf8", 200, "{\"key\":\"\xff\"}", false},
		{"oversized", 200, `{"key":"` + strings.Repeat("x", oauthResponseLimit) + `"}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: oauthTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if fixture.fail {
					return nil, errors.New("raw-upstream-secret")
				}
				return &http.Response{StatusCode: fixture.status, Body: io.NopCloser(strings.NewReader(fixture.body)), Header: make(http.Header)}, nil
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			key, err := exchangeOpenRouterCode(context.Background(), client, []byte("fixture-code"), []byte(strings.Repeat("v", 43)))
			if len(key) != 0 || err == nil || calls != 1 {
				t.Fatal("unknown exchange outcome returned a key or retried")
			}
			var problem *domain.Error
			if !errors.As(err, &problem) || problem.Code != domain.RecoveryRequired {
				t.Fatal("unknown exchange outcome lost recovery guidance")
			}
			raw, _ := json.Marshal(problem)
			for _, secret := range []string{"raw-upstream-secret", "fixture-code", "fixture-verifier", "private-email", "first-secret", "second-secret"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("upstream data escaped sanitized outcome")
				}
			}
		})
	}
}

func TestOAuthAuthorizationHasS256AndOptionalLocalCallback(t *testing.T) {
	callback := "http://localhost:51423/oauth/" + strings.Repeat("a", 43)
	for _, target := range []string{"", callback} {
		verifier, authorization, err := NewOpenRouterAuthorization(target, bytes.NewReader(bytes.Repeat([]byte{7}, 32)))
		if err != nil {
			t.Fatal(err)
		}
		defer clear(verifier)
		if len(verifier) != 43 {
			t.Fatal("invalid PKCE verifier size")
		}
		u, err := url.Parse(authorization)
		expected := sha256.Sum256(verifier)
		q := u.Query()
		count := 3
		if target != "" {
			count++
		}
		if err != nil || u.Scheme != "https" || u.Host != "openrouter.ai" || u.Path != "/auth" || u.User != nil || u.Fragment != "" || len(q) != count || q.Get("callback_url") != target || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(expected[:]) || q.Get("key_label") != "DeliDev" {
			t.Fatal("authorization URL escaped its closed contract")
		}
		if strings.Contains(authorization, string(verifier)) {
			t.Fatal("verifier entered authorization URL")
		}
	}
	verifier, authorization, err := NewOpenRouterAuthorization("", strings.NewReader("short"))
	if err == nil || verifier != nil || authorization != "" {
		t.Fatal("partial entropy created an authorization")
	}
	verifier, authorization, err = NewOpenRouterAuthorization("https://untrusted.example/callback", bytes.NewReader(make([]byte, 32)))
	if err == nil || verifier != nil || authorization != "" {
		t.Fatal("unowned callback created an authorization")
	}
}

func TestOAuthExchangeRejectsInvalidInputBeforeNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: oauthTestTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("network must not run") })}
	for _, input := range []struct{ code, verifier []byte }{
		{nil, bytes.Repeat([]byte("v"), 43)},
		{[]byte("code\n"), bytes.Repeat([]byte("v"), 43)},
		{bytes.Repeat([]byte("x"), domain.MaxOAuthCodeBytes+1), bytes.Repeat([]byte("v"), 43)},
		{[]byte("code"), nil},
		{[]byte("code"), bytes.Repeat([]byte("v"), 129)},
		{[]byte("code"), bytes.Repeat([]byte(" "), 43)},
	} {
		key, err := exchangeOpenRouterCode(context.Background(), client, input.code, input.verifier)
		if err == nil || key != nil || calls != 0 {
			t.Fatal("invalid input reached the provider")
		}
	}
}
