// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestProviderAcceptanceOllamaNative(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streamed"}[stream], func(t *testing.T) {
			var calls atomic.Int32
			f := newProviderAcceptance(t, "ollama-cloud", "glm-5.3", func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host != "ollama.com" || r.URL.Path != "/api/chat" || r.Method != "POST" {
					t.Error("Ollama did not use native chat endpoint")
				}
				b := providerAcceptanceBody(t, r)
				if b["model"] != "glm-5.3" || b["stream"] != stream {
					t.Error("native model/stream mismatch")
				}
				if _, ok := b["messages"].([]any); !ok {
					t.Error("native messages missing")
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				io.WriteString(w, "{\"model\":\"glm-5.3\",\"message\":{\"role\":\"assistant\",\"content\":\"fixture ok\",\"thinking\":\"fixture thought\"},\"done\":false}\n{\"model\":\"glm-5.3\",\"message\":{\"role\":\"assistant\",\"content\":\"\"},\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":2,\"eval_count\":1}\n")
			})
			b, _ := json.Marshal(map[string]any{"model": "fixture", "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": stream})
			w := f.call("/v1/chat/completions", string(b), nil)
			providerAcceptanceStatus(t, w, 200)
			if calls.Load() != 1 || !strings.Contains(w.Body.String(), "fixture ok") {
				t.Fatal("native response missing or duplicate send")
			}
			if stream && !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal("missing stream terminal")
			}
		})
	}
}
func TestProviderAcceptanceAzureResponses(t *testing.T) {
	var calls atomic.Int32
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "azure-openai", "deployment", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "fixture.openai.azure.com" || r.URL.Path != "/openai/v1/responses" || r.URL.RawQuery != "" {
			t.Error("Azure v1 Responses URL changed")
		}
		if r.Header.Get("Api-Key") != f.secret || r.Header.Get("Authorization") != "" {
			t.Error("Azure key-only authentication violated")
		}
		b := providerAcceptanceBody(t, r)
		if b["model"] != "deployment" || b["store"] != false || b["max_output_tokens"] != float64(128) {
			t.Error("Azure model/store policy missing")
		}
		providerAcceptanceResponse(w, providerAcceptanceResponses)
	})
	floor := int64(128)
	f.data.Metadata.Configuration = &v1.ProviderConfiguration{MinMaxOutputTokens: &floor}
	f.save()
	w := f.call("/v1/responses", `{"model":"fixture","input":"hello","max_output_tokens":1,"stream":false}`, nil)
	providerAcceptanceStatus(t, w, 200)
	if calls.Load() != 1 || !strings.Contains(w.Body.String(), "fixture ok") {
		t.Fatal("Azure response missing")
	}
}
func TestProviderAcceptanceDSHReimport(t *testing.T) {
	var calls atomic.Int32
	var reject atomic.Bool
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "dsh-account", "deepseek-flash", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "api.deepseek.com" || r.URL.Path != "/anthropic/v1/messages" {
			t.Error("DSH endpoint mismatch")
		}
		if r.Header.Get("X-Dsh-Auth-Token") != f.secret || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("DSH authoritative headers missing")
		}
		for _, k := range []string{"Authorization", "X-Api-Key", "Api-Key", "Anthropic-Beta", "X-Claude-Code-Session-Id", "X-Client-Request-Id"} {
			if r.Header.Get(k) != "" {
				t.Errorf("forbidden DSH header %s", k)
			}
		}
		if reject.Load() {
			w.WriteHeader(401)
			providerAcceptanceResponse(w, `{"error":{"message":"expired `+f.secret+`"}}`)
			return
		}
		providerAcceptanceResponse(w, `{"id":"msg_fixture","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"fixture ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
	})
	request := `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"max_tokens":25,"stream":false}`
	providerAcceptanceStatus(t, f.call("/v1/messages", request, nil), 200)
	reject.Store(true)
	w := f.call("/v1/messages", request, nil)
	if w.Code < 400 || calls.Load() != 2 || !strings.Contains(w.Body.String(), "dsh_reimport_required") {
		t.Fatal("DSH 401 did not stop with reimport refusal")
	}
	_, _, e := f.s.GetPrivateVersion(context.Background(), f.scope, string(providerKind), f.data.Metadata.Id)
	if e != store.ErrReauthentication {
		t.Fatal("DSH credential not fenced for reimport")
	}
	before := calls.Load()
	w = f.call("/v1/messages", request, nil)
	if w.Code < 400 || calls.Load() != before {
		t.Fatal("DSH stale credential sent again")
	}
	// A newly imported generation is eligible, without reviving the old generation.
	f.data.Credential.AccessToken = "replacement-" + f.secret
	f.secret = f.data.Credential.AccessToken
	f.save()
	reject.Store(false)
	providerAcceptanceStatus(t, f.call("/v1/messages", request, nil), 200)
}
func TestProviderAcceptanceCommandCode(t *testing.T) {
	for _, mode := range []string{"success", "plan", "credits"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var f *providerAcceptanceFixture
			f = newProviderAcceptance(t, "command-code", "glm-5.2", func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host != "api.commandcode.ai" || r.URL.Path != "/alpha/generate" {
					t.Error("subscription API endpoint mismatch")
				}
				if r.Header.Get("X-Command-Code-Version") != "0.52.1" {
					t.Error("subscription CLI version replaced by storage generation")
				}
				b := providerAcceptanceBody(t, r)
				params, _ := b["params"].(map[string]any)
				if params["stream"] != true || params["model"] != "zai-org/GLM-5.2" || b["config"] == nil {
					t.Error("ModelMessage payload missing")
				}
				messages, _ := params["messages"].([]any)
				if len(messages) != 1 {
					t.Error("subscription ModelMessage list mismatch")
				} else {
					m, _ := messages[0].(map[string]any)
					parts, _ := m["content"].([]any)
					if m["role"] != "user" || len(parts) != 1 {
						t.Error("subscription ModelMessage content missing")
					} else {
						text, _ := parts[0].(map[string]any)
						if text["type"] != "text" || text["text"] != "hello" {
							t.Error("subscription text conversion mismatch")
						}
					}
				}
				if b["memory"] != "" || b["skills"] != nil {
					t.Error("remote request contains local context")
				}
				switch mode {
				case "plan":
					w.WriteHeader(403)
					io.WriteString(w, `{"success":false,"error":{"code":"FORBIDDEN","message":"MODEL_NOT_IN_PLAN: `+f.secret+`"}}`)
				case "credits":
					w.WriteHeader(400)
					io.WriteString(w, `{"success":false,"error":{"code":"BAD_REQUEST","message":"You have insufficient credits to make this request. `+f.secret+`"}}`)
				default:
					w.Header().Set("Content-Type", "application/x-ndjson")
					io.WriteString(w, "{\"type\":\"text-delta\",\"text\":\"fixture ok\"}\n{\"type\":\"finish\",\"finishReason\":\"stop\",\"totalUsage\":{\"inputTokens\":2,\"outputTokens\":1}}\n")
				}
			})
			w := f.call("/v1/chat/completions", `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"stream":false}`, nil)
			switch mode {
			case "success":
				providerAcceptanceStatus(t, w, 200)
				if !strings.Contains(w.Body.String(), "fixture ok") {
					t.Fatal("NDJSON text missing")
				}
			case "plan":
				providerAcceptanceStatus(t, w, 400)
				if !strings.Contains(w.Body.String(), "model_not_in_plan") {
					t.Fatal("plan refusal classification missing")
				}
			case "credits":
				providerAcceptanceStatus(t, w, 429)
				if !strings.Contains(w.Body.String(), "insufficient_credits") {
					t.Fatal("credits refusal classification missing")
				}
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected subscription replay")
			}
		})
	}
}
