// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAPIProfilesPreserveLegacyAndPinConnections(t *testing.T) {
	presets := Presets()
	if len(presets) != 35 {
		t.Fatal("incomplete registry")
	}
	for _, preset := range presets {
		t.Run(string(preset.ID), func(t *testing.T) {
			p := preset.Provider
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			legacy := p
			legacy.APIFormats = nil
			a := domain.Account{Type: domain.APIAccount}
			resolved, err := ResolveAccountProfile(legacy, a)
			if err != nil || resolved.LegacyAPIFormat() != p.LegacyAPIFormat() {
				t.Fatal("legacy format changed", err)
			}
			for _, profile := range p.APIFormats {
				a.APIProtocol = profile.Protocol
				resolved, err = ResolveAccountProfile(legacy, a)
				if err != nil || resolved.LegacyAPIFormat() != profile {
					t.Fatal("selected format lost", err)
				}
				a.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: profile.Authentication, ConnectedAt: time.Now(), APIFormat: &profile}
				if _, err = ResolveAccountProfile(p, a); err != nil {
					t.Fatal(err)
				}
				changed := p
				changed.APIFormats = append([]domain.ProviderAPIFormat(nil), p.APIFormats...)
				for i := range changed.APIFormats {
					if changed.APIFormats[i].Protocol == profile.Protocol {
						changed.APIFormats[i].Endpoint += "/changed"
					}
				}
				if _, err = ResolveAccountProfile(changed, a); domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("connection was silently retargeted", err)
				}
				a.Connection = nil
			}
		})
	}
}

func TestOpenRouterFormatsUseOriginalInspectionRoutes(t *testing.T) {
	var p domain.Provider
	for _, preset := range Presets() {
		if preset.ID == domain.PresetOpenRouter {
			p = preset.Provider
		}
	}
	if len(p.APIFormats) != 3 {
		t.Fatal("OpenRouter must offer three direct formats")
	}
	for _, profile := range p.APIFormats {
		t.Run(string(profile.Protocol), func(t *testing.T) {
			selected, err := ResolveAccountProfile(p, domain.Account{Type: domain.APIAccount, APIProtocol: profile.Protocol})
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				paths = append(paths, r.URL.Path)
				if r.Method != http.MethodGet || r.URL.Host != "openrouter.ai" || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("x-api-key") != "" {
					t.Error("incorrect inspection authority")
				}
				w := httptest.NewRecorder()
				if r.URL.Path == "/api/v1/key" {
					fmt.Fprint(w, `{"data":{"is_free_tier":false}}`)
				} else if r.URL.Path == "/api/v1/models" {
					query := r.URL.Query()
					if len(query) != 3 || query.Get("limit") != "500" || query.Get("offset") != "0" || query.Get("output_modalities") != "text" {
						t.Error("incorrect text-output catalog request")
						fmt.Fprint(w, `{"data":[{"id":"provider/image-only","context_length":0}]}`)
					} else {
						fmt.Fprint(w, `{"data":[{"id":"~anthropic/claude-opus-latest","context_length":8192,"architecture":{"input_modalities":["text","image","audio"],"output_modalities":["text"]}}],"total_count":1}`)
					}
				} else {
					t.Error("inference sent during inspection")
				}
				return w.Result(), nil
			})}
			o := inspect(context.Background(), client, selected, []byte("fixture-key"))
			if o.Problem() != nil || o.Authentication != CredentialAccepted || len(o.Models) != 1 || len(paths) != 2 {
				t.Fatal("inspection failed", o, paths)
			}
			if o.Models[0].ID != "~anthropic/claude-opus-latest" || o.Models[0].ContextLimit == nil || *o.Models[0].ContextLimit != 8192 || len(o.Models[0].InputModalities) != 3 || len(o.Models[0].OutputModalities) != 1 || o.Models[0].OutputModalities[0] != "text" {
				t.Fatal("multimodal text-output metadata changed", o.Models)
			}
			if HarnessMatches(domain.Codex, selected.Protocol) != (profile.Protocol == domain.OpenAIResponses) {
				t.Fatal("Codex compatibility ignored account format")
			}
		})
	}
}

func TestUnknownAccountFormatNeverFallsBack(t *testing.T) {
	p := domain.Provider{Name: "Fixture", Protocol: domain.OpenAIChat, Endpoint: "https://api.example.test/v1", Authentication: domain.BearerAuth}
	if _, err := ResolveAccountProfile(p, domain.Account{Type: domain.APIAccount, APIProtocol: domain.OpenAIResponses}); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("format fallback", err)
	}
	preset := domain.PresetOpenRouter
	p.PresetID = &preset
	if len(APIFormats(p)) != 1 {
		t.Fatal("copied identity granted profiles")
	}
}
