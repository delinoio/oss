// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestHostedComposedXAICustomApplyPatch(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: fixture.txt\n+quoted \"text\" and \\ slash\n*** End Patch"
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "sse"}[stream], func(t *testing.T) {
			var sends atomic.Int32
			args, _ := json.Marshal(map[string]any{"input": patch})
			item := map[string]any{"type": "function_call", "id": "fc_patch", "call_id": "call_patch", "name": "apply_patch", "arguments": string(args), "status": "completed"}
			snapshot := map[string]any{"id": "resp_patch", "object": "response", "model": "grok-4.7", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 4, "output_tokens": 7}}
			f := newProviderAcceptance(t, "xai", "grok-4.7", func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if r.URL.Path != "/v1/responses" {
					t.Errorf("xAI endpoint=%s", r.URL.Path)
				}
				body := providerAcceptanceBody(t, r)
				tools, _ := body["tools"].([]any)
				if len(tools) != 1 {
					t.Errorf("lowered tools=%v", tools)
					return
				}
				tool, _ := tools[0].(map[string]any)
				if tool["type"] != "function" || tool["name"] != "apply_patch" {
					t.Errorf("custom declaration not lowered: %v", tool)
				}
				choice, _ := body["tool_choice"].(map[string]any)
				if choice["type"] != "function" || choice["name"] != "apply_patch" {
					t.Errorf("selector not lowered: %v", choice)
				}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(snapshot)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event map[string]any) {
					raw, _ := json.Marshal(event)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
					w.(http.Flusher).Flush()
				}
				emit(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_patch", "object": "response", "status": "in_progress", "output": []any{}}})
				emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_patch", "call_id": "call_patch", "name": "apply_patch", "arguments": ""}})
				// Split in the middle of escaped JSON, so a public custom input cannot be
				// reconstructed by merely relabeling raw function-argument deltas.
				for _, part := range []string{string(args[:len(args)/2]), string(args[len(args)/2:])} {
					emit(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_patch", "delta": part})
				}
				emit(map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "item_id": "fc_patch", "arguments": string(args)})
				emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
				emit(map[string]any{"type": "response.completed", "response": snapshot})
			})
			f.data.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
			f.data.Credential = nil
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{ModelWires: map[string]v1.ProviderWire{"grok-4.7": v1.ProviderWire_PROVIDER_WIRE_RESPONSES}}
			f.save()
			if _, _, err := resolvedProviderRoute(t.Context(), f.data, "grok-4.7"); err != nil {
				t.Fatalf("resolve fixture: %v", err)
			}
			request, _ := json.Marshal(map[string]any{"model": "fixture", "input": "Create fixture.txt", "stream": stream, "tools": []any{map[string]any{"type": "custom", "name": "apply_patch", "format": map[string]any{"type": "text"}}}, "tool_choice": map[string]any{"type": "custom", "name": "apply_patch"}})
			response := f.call("/v1/responses", string(request), nil)
			providerAcceptanceStatus(t, response, 200)
			assertItem := func(item map[string]any) {
				t.Helper()
				if item["type"] != "custom_tool_call" || item["name"] != "apply_patch" || item["input"] != patch || item["call_id"] != "call_patch" {
					t.Fatalf("public custom identity lost: %+v", item)
				}
				if _, exists := item["arguments"]; exists {
					t.Fatal("function argument wrapper leaked")
				}
			}
			if !stream {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				output, _ := body["output"].([]any)
				if len(output) != 1 {
					t.Fatalf("public output=%s", response.Body.String())
				}
				call, _ := output[0].(map[string]any)
				assertItem(call)
			} else {
				done, terminal := false, false
				for _, line := range strings.Split(response.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
						continue
					}
					kind, _ := event["type"].(string)
					if strings.Contains(kind, "function_call_arguments") {
						t.Fatalf("lowered SSE event leaked: %s", kind)
					}
					switch kind {
					case "response.custom_tool_call_input.delta":
						if event["delta"] != "" {
							t.Fatal("escaped function JSON leaked as custom delta")
						}
					case "response.custom_tool_call_input.done":
						done = true
						if event["input"] != patch {
							t.Fatalf("custom input=%v", event["input"])
						}
					case "response.output_item.done":
						call, _ := event["item"].(map[string]any)
						assertItem(call)
					case "response.completed":
						terminal = true
						body, _ := event["response"].(map[string]any)
						output, _ := body["output"].([]any)
						if len(output) != 1 {
							t.Fatal("terminal custom output missing")
						}
						call, _ := output[0].(map[string]any)
						assertItem(call)
					}
				}
				if !done || !terminal {
					t.Fatalf("missing custom input/terminal events: %s", response.Body.String())
				}
			}
			if sends.Load() != 1 {
				t.Fatalf("custom lowering retried: %d", sends.Load())
			}
		})
	}
}
