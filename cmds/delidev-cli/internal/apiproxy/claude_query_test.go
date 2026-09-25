package apiproxy

import (
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAnthropicBetaQueryPreservesOnlyExactScopedOperation(t *testing.T) {
	for _, operation := range []Operation{MessageCreate, MessageCountTokens} {
		t.Run(string(operation), func(t *testing.T) {
			path, reply := "/messages", `{"id":"msg_fixture","type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`
			if operation == MessageCountTokens {
				path, reply = "/messages/count_tokens", `{"input_tokens":3}`
			}
			f := newProxyFixture(t, domain.AnthropicMessages, []Operation{operation}, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/provider"+path || r.URL.RawQuery != "beta=true" || r.Header.Get("X-Api-Key") != fixtureKey || r.Header.Get("Anthropic-Beta") != "fixture-2026-09-25" {
					t.Error("native beta operation or authority changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reply))
			})
			response, raw, err := f.request(t, path+"?beta=true", `{"model":"fixed-model","messages":[]}`, func(r *http.Request) { r.Header.Set("Anthropic-Beta", "fixture-2026-09-25") })
			if err != nil || response.StatusCode != http.StatusOK || string(raw) != reply || f.calls.Load() != 1 {
				t.Fatal("exact native query was not forwarded", err)
			}
			assertNoProxySecrets(t, f, raw)
			for _, query := range []string{"beta=false", "beta=True", "Beta=true", "beta=1", "beta=%74rue", "%62eta=true", "beta=true&beta=true", "beta=true&route=other", "beta=true;route=other", "beta=true&", ""} {
				response, raw, err := f.request(t, path+"?"+query, `{"model":"fixed-model","messages":[]}`, nil)
				if err != nil || response.StatusCode != http.StatusNotFound || f.calls.Load() != 1 || f.authority.keys.Load() != 1 {
					t.Fatal("unsupported query obtained provider authority", query, err)
				}
				assertNoProxySecrets(t, f, raw)
			}
		})
	}
	for _, protocol := range []domain.APIProtocol{domain.OpenAIChat, domain.OpenAIResponses} {
		operation, path := ChatCompletion, "/chat/completions"
		if protocol == domain.OpenAIResponses {
			operation, path = ResponseCreate, "/responses"
		}
		f := newProxyFixture(t, protocol, []Operation{operation}, func(http.ResponseWriter, *http.Request) { t.Error("foreign protocol received beta query") })
		response, _, err := f.request(t, path+"?beta=true", `{"model":"fixed-model"}`, nil)
		if err != nil || response.StatusCode != http.StatusNotFound || f.authority.keys.Load() != 0 {
			t.Fatal("foreign protocol beta query accepted", err)
		}
	}
}
