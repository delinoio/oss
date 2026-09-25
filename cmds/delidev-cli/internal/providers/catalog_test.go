package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOpenRouterCatalogPaginatesOnlyFixedAuthority(t *testing.T) {
	for _, mode := range []string{"current", "legacy", "duplicate", "incomplete", "bad-count", "too-many"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				w := httptest.NewRecorder()
				if r.URL.Scheme != "https" || r.URL.Host != "openrouter.ai" || r.Header.Get("Authorization") != "Bearer fixture-router-key" {
					t.Fatal("catalog escaped authority or selected credential")
				}
				if calls == 1 {
					if r.URL.Path != "/api/v1/key" || r.URL.RawQuery != "" {
						t.Fatal("credential check was bypassed")
					}
					fmt.Fprint(w, `{"data":{"is_free_tier":false}}`)
					return w.Result(), nil
				}
				query := r.URL.Query()
				if r.URL.Path != "/api/v1/models" || len(query) != 3 || query.Get("limit") != "500" || query.Get("output_modalities") != "all" || query.Get("offset") != strconv.Itoa((calls-2)*500) {
					t.Fatal("invalid fixed pagination request")
				}
				start, end := (calls-2)*500, (calls-1)*500
				total := 501
				if mode == "too-many" {
					total = 10001
				}
				if end > total {
					end = total
				}
				if mode == "incomplete" {
					end = 1
				}
				data := []map[string]any{}
				for i := start; i < end; i++ {
					id := fmt.Sprintf("catalog-%05d", i)
					if mode == "duplicate" && calls == 3 {
						id = "catalog-00000"
					}
					data = append(data, map[string]any{"id": id, "name": "Friendly model", "context_length": 1234, "architecture": map[string]any{"input_modalities": []string{"text", "image"}, "output_modalities": []string{"text"}}, "supported_parameters": []string{"tools", "reasoning"}})
				}
				result := map[string]any{"data": data, "next_page": "https://untrusted.example/credentials", "links": map[string]string{"next": "https://untrusted.example/other"}}
				if mode != "legacy" {
					result["total_count"] = total
				}
				if mode == "bad-count" {
					result["total_count"] = nil
				}
				if err := json.NewEncoder(w).Encode(result); err != nil {
					t.Fatal(err)
				}
				return w.Result(), nil
			})}
			p := domain.Provider{Name: "Router", Endpoint: "https://openrouter.ai/api/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth}
			o := inspect(context.Background(), client, p, []byte("fixture-router-key"))
			switch mode {
			case "current", "legacy":
				if o.Problem() != nil || len(o.Models) != 501 || calls != 3 {
					t.Fatalf("pagination: %+v calls=%d", o, calls)
				}
				m := o.Models[0]
				if m.Name != "Friendly model" || m.ContextLimit == nil || *m.ContextLimit != 1234 || len(m.InputModalities) != 2 || m.Tools == nil || !*m.Tools || m.Reasoning == nil || !*m.Reasoning {
					t.Fatalf("metadata missing: %+v", m)
				}
			case "too-many":
				if o.Failure != ResponseTooLarge || len(o.Models) != 0 {
					t.Fatal("oversized catalog partially returned")
				}
			default:
				if o.Failure != InvalidResponse || len(o.Models) != 0 {
					t.Fatalf("invalid pagination accepted: %+v", o)
				}
			}
		})
	}
}

func TestCatalogAdvisoryMetadataRejectsMalformedOrReflectedValues(t *testing.T) {
	const key = "fixturekey"
	for _, field := range []map[string]any{
		{"name": key}, {"name": base64.StdEncoding.EncodeToString([]byte(key))},
		{"architecture": map[string]any{"input_modalities": []string{key}}},
		{"architecture": map[string]any{"output_modalities": []string{"Text"}}},
		{"architecture": map[string]any{"input_modalities": 3}},
		{"supported_parameters": []any{"tools", 7}},
		{"context_length": -1}, {"context_length": "1000"},
	} {
		field["id"] = "safe-model"
		raw, _ := json.Marshal(map[string]any{"data": []any{field}})
		if _, _, err := parseModels(raw, domain.OpenAIChat, []byte(key)); err == nil {
			t.Fatalf("invalid metadata accepted: %s", raw)
		}
	}
	models, _, err := parseModels([]byte(`{"data":[{"id":"unknown","name":null,"context_length":null,"architecture":null,"supported_parameters":null},{"id":"unsupported","supported_parameters":[]}]}`), domain.OpenAIChat, nil)
	if err != nil || len(models) != 2 || models[0].Tools != nil || models[0].Reasoning != nil || models[1].Tools == nil || *models[1].Tools {
		t.Fatalf("unknown/explicit absence conflated: %+v %v", models, err)
	}
}

func TestCatalogContextLimitsCannotReflectNumericKeys(t *testing.T) {
	for _, protocol := range []domain.APIProtocol{domain.OpenAIChat, domain.OpenAIResponses, domain.AnthropicMessages} {
		field := "context_length"
		if protocol == domain.AnthropicMessages {
			field = "max_input_tokens"
		}
		for _, test := range []struct {
			name, key, limit string
			valid            bool
		}{
			{"exact", "1234", "1234", false},
			{"embedded", "12345678", "9123456789", false},
			{"maximum", "18446744073709551615", "18446744073709551615", false},
			{"unrelated", "1234", "8192", true},
			{"keyless", "", "1234", true},
		} {
			t.Run(string(protocol)+"/"+test.name, func(t *testing.T) {
				raw := []byte(fmt.Sprintf(`{"data":[{"id":"safe-first"},{"id":"safe-second",%q: %s }]}`, field, test.limit))
				models, next, err := parseModels(raw, protocol, []byte(test.key))
				if !test.valid {
					if err == nil || models != nil || next != "" {
						t.Fatal("reflected numeric metadata returned a publishable catalog")
					}
					return
				}
				if err != nil || len(models) != 2 || models[1].ContextLimit == nil || strconv.FormatUint(*models[1].ContextLimit, 10) != test.limit {
					t.Fatal("safe context metadata changed", err)
				}
			})
		}
	}
}

func TestProviderPresetsAreIndependentValidConfiguration(t *testing.T) {
	items := Presets()
	if len(items) != 9 {
		t.Fatal("missing required provider presets")
	}
	seen := map[domain.ProviderPresetID]bool{}
	for _, item := range items {
		if seen[item.ID] || item.ID == "" || !item.Provider.Discovery || item.Documentation == "" || item.KeyGuidance == "" || item.Compatibility == "" {
			t.Fatal("invalid preset identity or guidance")
		}
		if err := item.Provider.Validate(); err != nil {
			t.Fatalf("preset %s: %v", item.ID, err)
		}
		seen[item.ID] = true
	}
	items[0].Provider.Endpoint = "modified"
	if Presets()[0].Provider.Endpoint == "modified" {
		t.Fatal("editing a preset changed shared defaults")
	}
}
