// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type oauthCredentialAuthority struct {
	*fixtureAuthority
	project string
}

func (a oauthCredentialAuthority) Acquire(ctx context.Context, token string) (*Lease, error) {
	l, e := a.fixtureAuthority.Acquire(ctx, token)
	if e == nil {
		l.Credential = func(context.Context) (Credential, error) {
			return Credential{Key: []byte(fixtureKey), QuotaProject: a.project}, nil
		}
	}
	return l, e
}

type oauthProxyTransport func(*http.Request) (*http.Response, error)

func (f oauthProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGoogleOAuthRelayUsesOnlyConnectionQuotaProject(t *testing.T) {
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected fixture destination") })
	preset := domain.PresetGemini
	f.authority.scope.Provider.PresetID = &preset
	f.authority.scope.Provider.Endpoint = "https://generativelanguage.googleapis.com/v1beta/openai"
	h := New(oauthCredentialAuthority{f.authority, "my-ai-project"}, slog.New(slog.NewJSONHandler(f.logs, nil)))
	called := false
	h.client.Transport = oauthProxyTransport(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.Host != "generativelanguage.googleapis.com" || r.Header.Get("x-goog-user-project") != "my-ai-project" || r.Header.Get("Authorization") != "Bearer "+fixtureKey || r.Header.Get("x-goog-api-key") != "" {
			t.Error("wrong connection billing authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"result"},"finish_reason":"stop"}]}`)), Request: r}, nil
	})
	request := httptest.NewRequest(http.MethodPost, Prefix+"/chat/completions", strings.NewReader(`{"model":"fixed-model","messages":[]}`))
	request.RemoteAddr = "127.0.0.1:55451"
	request.Header.Set("Authorization", "Bearer "+fixtureToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-goog-user-project", "foreign-project")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != 200 || !called {
		t.Fatalf("relay: %d", recorder.Code)
	}
	f.authority.scope.Provider.PresetID = nil
	called = false
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, Prefix+"/chat/completions", strings.NewReader(`{"model":"fixed-model","messages":[]}`))
	request.RemoteAddr = "127.0.0.1:55451"
	request.Header.Set("Authorization", "Bearer "+fixtureToken)
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(recorder, request)
	if called || recorder.Code == 200 {
		t.Fatal("foreign provider received Google quota")
	}
}
