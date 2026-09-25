package apiproxy

import (
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAnthropicNativeAuthenticationUsesOneExactScopedCredential(t *testing.T) {
	for _, operation := range []Operation{MessageCreate, MessageCountTokens} {
		t.Run(string(operation), func(t *testing.T) {
			path, reply := "/messages", `{"id":"msg_fixture","type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`
			if operation == MessageCountTokens {
				path, reply = "/messages/count_tokens", `{"input_tokens":3}`
			}
			f := newProxyFixture(t, domain.AnthropicMessages, []Operation{operation}, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != fixtureKey || len(r.Header.Values("X-Api-Key")) != 1 || r.Header.Get("Authorization") != "" {
					t.Error("scoped credentials crossed the server-only key boundary")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reply))
			})
			response, raw, err := f.request(t, path, `{"model":"fixed-model","messages":[]}`, func(r *http.Request) { r.Header.Set("X-Api-Key", fixtureToken) })
			if err != nil || response.StatusCode != http.StatusOK || string(raw) != reply || f.calls.Load() != 1 || f.authority.acquires.Load() != 1 || f.authority.keys.Load() != 1 {
				t.Fatal("identical native authentication sources were not forwarded once", err)
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}

func TestNativeAuthenticationRejectsAmbiguousPairsBeforeAuthority(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol domain.APIProtocol
		modify   func(http.Header)
	}{
		{"different-token", domain.AnthropicMessages, func(h http.Header) { h.Set("X-Api-Key", fixtureToken[:len(fixtureToken)-1]+"Z") }},
		{"malformed-token", domain.AnthropicMessages, func(h http.Header) { h.Set("Authorization", "Bearer invalid"); h.Set("X-Api-Key", "invalid") }},
		{"lowercase-scheme", domain.AnthropicMessages, func(h http.Header) { h.Set("Authorization", "bearer "+fixtureToken) }},
		{"double-authorization", domain.AnthropicMessages, func(h http.Header) { h.Add("Authorization", "Bearer "+fixtureToken) }},
		{"double-api-key", domain.AnthropicMessages, func(h http.Header) { h.Add("X-Api-Key", fixtureToken) }},
		{"comma-authorization", domain.AnthropicMessages, func(h http.Header) { h.Set("Authorization", "Bearer "+fixtureToken+", Bearer "+fixtureToken) }},
		{"comma-api-key", domain.AnthropicMessages, func(h http.Header) { h.Set("X-Api-Key", fixtureToken+", "+fixtureToken) }},
		{"chat-pair", domain.OpenAIChat, nil},
		{"responses-pair", domain.OpenAIResponses, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation, path := MessageCreate, "/messages"
			if tc.protocol == domain.OpenAIChat {
				operation, path = ChatCompletion, "/chat/completions"
			} else if tc.protocol == domain.OpenAIResponses {
				operation, path = ResponseCreate, "/responses"
			}
			f := newProxyFixture(t, tc.protocol, []Operation{operation}, func(http.ResponseWriter, *http.Request) { t.Error("ambiguous credential reached provider") })
			response, raw, err := f.request(t, path, `{"model":"fixed-model","messages":[]}`, func(r *http.Request) {
				r.Header.Set("X-Api-Key", fixtureToken)
				if tc.modify != nil {
					tc.modify(r.Header)
				}
			})
			if err != nil || response.StatusCode != http.StatusUnauthorized || f.authority.acquires.Load() != 0 || f.authority.keys.Load() != 0 || f.calls.Load() != 0 {
				t.Fatal("ambiguous credential obtained provider authority", err)
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}
