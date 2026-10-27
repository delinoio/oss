// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func copilotObject(s string) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(s), &m)
	return m
}
func TestCopilotAutoNegotiatesSelectedWireAndFreshSession(t *testing.T) {
	for _, wire := range []inference.Wire{inference.Chat, inference.Responses} {
		t.Run(string(wire), func(t *testing.T) {
			target := inference.Target{Provider: "account", BaseURL: "https://api.individual.githubcopilot.com", Model: "auto", Wire: inference.Chat, Headers: http.Header{copilotSessionHeader: {"previous-account-session"}}}
			credential := inference.Credential{AccountID: "account", Secret: "current-token", Headers: http.Header{copilotSessionHeader: {"previous-session"}}}
			calls := 0
			endpoint := "/chat/completions"
			if wire == inference.Responses {
				endpoint = "/responses"
			}
			sender := func(ctx context.Context, path string, body any, session string) (map[string]any, error) {
				calls++
				switch path {
				case "/models":
					if session != "" || body != nil {
						t.Fatal("catalog inherited session")
					}
					return copilotObject(`{"data":[{"id":"selected","model_picker_enabled":false,"supported_endpoints":["` + endpoint + `"]}]}`), nil
				case "/models/session":
					if session != "" {
						t.Fatal("session inherited token")
					}
					return copilotObject(`{"session_token":"new-session","available_models":["selected"]}`), nil
				case "/models/session/intent":
					if session != "new-session" {
						t.Fatal("incorrect session")
					}
					prompt := body.(map[string]any)["prompt"].(string)
					if len(utf16.Encode([]rune(prompt))) != 32768 || strings.Contains(prompt, "earlier") {
						t.Fatal("unbounded or historical intent")
					}
					return copilotObject(`{"candidate_models":["not-in-pool","selected"]}`), nil
				}
				t.Fatal("unexpected path")
				return nil, nil
			}
			raw, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "earlier"}, map[string]any{"role": "user", "content": strings.Repeat("😀", 20000)}}})
			next, c, expiry, err := resolveCopilotAuto(context.Background(), target, credential, raw, sender)
			if err != nil || calls != 3 || next.Wire != wire || next.Model != "selected" || next.BaseURL != target.BaseURL || c.AccountID != credential.AccountID || c.Secret != credential.Secret || c.Headers.Get(copilotSessionHeader) != "new-session" || next.Headers.Get(copilotSessionHeader) != "" || time.Until(expiry) > time.Minute || time.Until(expiry) < 59*time.Second {
				t.Fatalf("negotiation mismatch: wire=%s calls=%d err=%v", next.Wire, calls, err)
			}
			if credential.Headers.Get(copilotSessionHeader) != "previous-session" {
				t.Fatal("mutated input headers")
			}
		})
	}
}
func TestCopilotAutoOnlyRequiresExplicitValidRows(t *testing.T) {
	cases := []string{`{"data":[]}`, `{"data":[{"id":"named"}]}`, `{"data":[{"id":"named","model_picker_enabled":"false"}]}`, `{"data":[{"id":"a","model_picker_enabled":false},{"id":"b"}]}`, `{"data":"bad"}`}
	for _, catalog := range cases {
		calls := 0
		_, _, expiry, err := resolveCopilotAuto(context.Background(), inference.Target{Model: "named"}, inference.Credential{}, nil, func(context.Context, string, any, string) (map[string]any, error) {
			calls++
			return copilotObject(catalog), nil
		})
		if err != nil || calls != 1 || !expiry.IsZero() {
			t.Fatalf("legacy catalog changed eligibility: %s", catalog)
		}
	}
}
func TestCopilotAutoRejectsMalformedOrExpiredSession(t *testing.T) {
	for _, session := range []string{`{"session_token":"bad token","available_models":["a"]}`, `{"session_token":"valid","available_models":["a",2]}`, `{"session_token":"valid","available_models":["a"],"expires_at":1}`, `{"session_token":"valid","available_models":["a"],"expires_at":"future"}`} {
		calls := 0
		_, _, _, err := resolveCopilotAuto(context.Background(), inference.Target{}, inference.Credential{}, nil, func(context.Context, string, any, string) (map[string]any, error) {
			calls++
			if calls == 1 {
				return copilotObject(`{"data":[{"id":"a","model_picker_enabled":false}]}`), nil
			}
			return copilotObject(session), nil
		})
		if err == nil || calls != 2 {
			t.Fatal("invalid session accepted")
		}
	}
}
func TestCopilotRequestBudgetRevocationAndSanitizedErrors(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.Header.Get("Authorization") != "Bearer account-token" || r.Header.Get(copilotSessionHeader) != "" {
			t.Error("stale headers")
		}
		w.Header().Set("Retry-After", "9999")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, "secret-echo-never-returned")
	}))
	defer server.Close()
	policy := inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}
	gate := func(c context.Context, start func(context.Context) error) error { return start(c) }
	ctx := inference.WithPhysicalSendBudget(context.Background(), func(context.Context, func(context.Context) error) error {
		return &inference.Error{Status: 429, Code: "request_send_budget_exhausted"}
	})
	c := inference.Credential{Secret: "account-token", Headers: http.Header{copilotSessionHeader: {"stale"}}}
	_, err := copilotRequest(ctx, policy, gate, server.URL, c, "/models", nil, "")
	if err == nil || sends.Load() != 0 {
		t.Fatal("budget refusal still sent")
	}
	_, err = copilotRequest(context.Background(), policy, gate, server.URL, c, "/models", nil, "")
	var e *inference.Error
	if !errors.As(err, &e) || e.Status != 429 || e.RetryAfter != time.Hour || strings.Contains(err.Error(), "secret-echo") {
		t.Fatalf("unsafe refusal %v", err)
	}
}
func TestCopilotOrigins(t *testing.T) {
	for _, raw := range []string{"https://api.githubcopilot.com", "https://api.individual.githubcopilot.com/"} {
		if !copilotOrigin(raw) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"http://api.githubcopilot.com", "https://api.githubcopilot.com.evil.test", "https://user@api.githubcopilot.com", "https://api.githubcopilot.com?", "https://api.githubcopilot.com:444", "https://githubcopilot.com"} {
		if copilotOrigin(raw) {
			t.Fatal(raw)
		}
	}
}

func TestCopilotStoredAccountVersionFencesNegotiationWrite(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	p := admitRouting(t, a, key)
	d := providerData{Metadata: &v1.ProviderConnection{Id: "copilot", ProviderId: "github-copilot", OrganizationId: sc.OrganizationID, State: v1.ConnectionState_CONNECTION_STATE_READY, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH}, Credential: &providers.Credential{Scope: sc, Provider: "github-copilot", AccessToken: "initial-token", APIBaseURL: "https://api.githubcopilot.com"}}
	putRoutingProvider(t, a, sc, d)
	_, version, err := a.providerVersion(context.Background(), sc, "copilot")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := a.current(p)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); _, _ = io.WriteString(w, `{"data":[]}`) }))
	defer server.Close()
	gate := func(ctx context.Context, start func(context.Context) error) error {
		return inferenceError(a.s.WithAccountDispatch(ctx, lease, "copilot", version, start))
	}
	d.Credential.AccessToken = "replacement-token"
	putRoutingProvider(t, a, sc, d)
	_, err = copilotRequest(context.Background(), inference.DestinationPolicy{ApprovedOrigins: []string{server.URL}}, gate, server.URL, inference.Credential{Secret: "initial-token"}, "/models", nil, "")
	if err == nil || sends.Load() != 0 {
		t.Fatal("replaced account token physically sent")
	}
	target := inference.Target{Provider: "copilot", BaseURL: "https://api.githubcopilot.com", Model: "gpt-4.1", Wire: inference.Chat, Auth: inference.OAuth}
	_, _, _, err = a.negotiateCopilot(context.Background(), p, target, inference.Credential{AccountID: "copilot", Secret: "initial-token"}, nil, inference.DestinationPolicy{})
	if !errors.Is(err, inference.ErrNoCredential) {
		t.Fatalf("stale captured credential not refused: %v", err)
	}
}
