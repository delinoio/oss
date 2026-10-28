// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestProviderAcceptanceWorkBuddyIdentityAndSSE(t *testing.T) {
	var mu sync.Mutex
	var identities []http.Header
	var f *providerAcceptanceFixture
	f = newProviderAcceptance(t, "workbuddy-ai", "default-model", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "www.workbuddy.ai" || r.URL.Path != "/v2/chat/completions" {
			t.Error("WorkBuddy endpoint mismatch")
		}
		if r.Header.Get("Authorization") != "Bearer "+f.secret || r.Header.Get("X-User-Id") != f.data.Credential.Metadata["uid"] || r.Header.Get("X-Domain") != "fixture-domain" {
			t.Error("WorkBuddy selected credential identity mismatch")
		}
		if r.Header.Get("X-Enterprise-Id") != f.data.Credential.Metadata["enterpriseId"] || r.Header.Get("X-Tenant-Id") != f.data.Credential.Metadata["enterpriseId"] {
			t.Error("WorkBuddy team identity mismatch")
		}
		if r.Header.Get("X-Api-Key") != "" || r.Header.Get("Api-Key") != "" {
			t.Error("WorkBuddy secondary credential leaked")
		}
		b := providerAcceptanceBody(t, r)
		if b["stream"] != true || b["max_tokens"] != float64(25) || b["max_completion_tokens"] != nil {
			t.Error("WorkBuddy SSE/output budget normalization missing")
		}
		messages, _ := b["messages"].([]any)
		if len(messages) == 0 || messages[0].(map[string]any)["role"] != "system" {
			t.Error("WorkBuddy system message missing")
		}
		tools, _ := b["tools"].([]any)
		alias := ""
		if len(tools) > 0 {
			fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
			alias, _ = fn["name"].(string)
		}
		if alias == "" || alias == "lookup.item" {
			t.Error("WorkBuddy tool alias missing")
		}
		mu.Lock()
		identities = append(identities, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "chat_workbuddy", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_fixture", "type": "function", "function": map[string]any{"name": alias, "arguments": "{}"}}}}, "finish_reason": nil}}}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		io.WriteString(w, "data: {\"id\":\"chat_workbuddy\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	})
	request := func(stream bool, follow bool) string {
		messages := []any{map[string]any{"role": "user", "content": "Hello"}}
		if follow {
			messages = append(messages, map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_fixture", "type": "function", "function": map[string]any{"name": "lookup.item", "arguments": "{}"}}}}, map[string]any{"role": "tool", "tool_call_id": "call_fixture", "content": "result"})
		}
		b, _ := json.Marshal(map[string]any{"model": "fixture", "stream": stream, "max_completion_tokens": 25, "messages": messages, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup.item", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}, "tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "lookup.item"}}})
		return string(b)
	}
	for i := 0; i < 2; i++ {
		w := f.call("/v1/chat/completions", request(i == 1, i == 1), http.Header{"Session_id": {"fixture-session"}})
		providerAcceptanceStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), "lookup.item") {
			t.Fatal("WorkBuddy tool name not restored")
		}
	}
	mu.Lock()
	first, second := identities[0], identities[1]
	mu.Unlock()
	if first.Get("X-Conversation-Request-ID") == "" || first.Get("X-Conversation-Request-ID") != second.Get("X-Conversation-Request-ID") {
		t.Error("same user turn lost identity across HTTP requests")
	}
	f.data.Credential.Metadata["uid"] = "other-fixture-user"
	f.data.Credential.AccountID = "other-account"
	f.save()
	w := f.call("/v1/chat/completions", request(false, false), http.Header{"Session_id": {"fixture-session"}})
	providerAcceptanceStatus(t, w, 200)
	mu.Lock()
	third := identities[2]
	mu.Unlock()
	if first.Get("X-Conversation-ID") == third.Get("X-Conversation-ID") {
		t.Fatal("WorkBuddy account replacement reused conversation identity")
	}
}
