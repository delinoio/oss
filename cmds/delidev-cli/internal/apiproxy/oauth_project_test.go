// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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
	var called atomic.Bool
	h.client.Transport = oauthProxyTransport(func(r *http.Request) (*http.Response, error) {
		called.Store(true)
		if r.URL.Host != "generativelanguage.googleapis.com" || r.Header.Get("x-goog-user-project") != "my-ai-project" || r.Header.Get("Authorization") != "Bearer "+fixtureKey || r.Header.Get("x-goog-api-key") != "" {
			t.Error("wrong connection billing authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"result"},"finish_reason":"stop"}]}`)), Request: r}, nil
	})
	f.server.Close()
	f.server = httptest.NewServer(h)
	response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, func(r *http.Request) {
		r.Header.Set("x-goog-user-project", "foreign-project")
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !called.Load() {
		t.Fatalf("relay: %d, %s", response.StatusCode, raw)
	}
	assertNoProxySecrets(t, f, raw)
	f.authority.scope.Provider.PresetID = nil
	called.Store(false)
	response, _, err = f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if called.Load() || response.StatusCode == 200 {
		t.Fatal("foreign provider received Google quota")
	}
}
