// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"net/http"
	"strings"
	"testing"
)

type oauthProjectTransport func(*http.Request) (*http.Response, error)

func (f oauthProjectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOAuthQuotaProjectDestinationAndInspection(t *testing.T) {
	preset := domain.PresetGemini
	p := domain.Provider{PresetID: &preset, Endpoint: "https://generativelanguage.googleapis.com/v1beta/openai", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth, Name: "Google Gemini", Enabled: new(true)}
	req, _ := http.NewRequest(http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/models", nil)
	if ApplyOAuthProject(req, p, "my-ai-project") != nil || req.Header.Get("x-goog-user-project") != "my-ai-project" {
		t.Fatal("missing quota header")
	}
	for _, destination := range []string{"https://foreign.test/models", "https://generativelanguage.googleapis.com.evil.test/models", "http://generativelanguage.googleapis.com/models", "https://generativelanguage.googleapis.com:443/models"} {
		r, _ := http.NewRequest(http.MethodGet, destination, nil)
		if ApplyOAuthProject(r, p, "my-ai-project") == nil {
			t.Fatal("foreign project destination")
		}
	}
	foreign := domain.PresetHuggingFace
	p.PresetID = &foreign
	if ApplyOAuthProject(req, p, "my-ai-project") == nil {
		t.Fatal("foreign preset granted project")
	}
	p.PresetID = &preset
	client := &http.Client{Transport: oauthProjectTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer google-oauth-token" || r.Header.Get("x-goog-api-key") != "" || r.Header.Get("x-goog-user-project") != "my-ai-project" {
			t.Error("OAuth treated as API key")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Request: r}, nil
	})}
	// Use the same private inspection context installed by InspectOAuth; the
	// transport fixture proves headers without making an account or HTTP call.
	o := inspect(context.WithValue(context.Background(), oauthProjectKey{}, "my-ai-project"), client, p, []byte("google-oauth-token"))
	if o.Failure != NoFailure {
		t.Fatal(o.Failure)
	}
}
