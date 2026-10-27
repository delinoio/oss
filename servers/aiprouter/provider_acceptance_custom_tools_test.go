// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderAcceptanceXAICustomTools(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streamed"}[stream], func(t *testing.T) {
			var sends atomic.Int32
			patch := "*** Begin Patch\n+fixture \\\"quote\\\"\n*** End Patch"
			f := newProviderAcceptance(t, "xai", "grok-4.20-multi-agent-0309", func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if r.Host != "api.x.ai" || r.URL.Path != "/v1/responses" {
					t.Error("xAI native Responses preset route lost")
				}
				b := providerAcceptanceBody(t, r)
				tools, _ := b["tools"].([]any)
				if len(tools) != 1 {
					t.Error("custom tool declaration missing")
				} else {
					tool, _ := tools[0].(map[string]any)
					params, _ := tool["parameters"].(map[string]any)
					props, _ := params["properties"].(map[string]any)
					input, _ := props["input"].(map[string]any)
					if tool["type"] != "function" || tool["name"] != "edit__apply_patch" || input["type"] != "string" || tool["format"] != nil {
						t.Error("custom tool was not lowered to function input wrapper")
					}
				}
				choice, _ := b["tool_choice"].(map[string]any)
				if choice["type"] != "function" || choice["name"] != "edit__apply_patch" {
					t.Error("custom tool choice not lowered")
				}
				history, _ := b["input"].([]any)
				if len(history) != 3 {
					t.Error("custom history changed size")
				} else {
					call, _ := history[1].(map[string]any)
					result, _ := history[2].(map[string]any)
					var args map[string]any
					text, _ := call["arguments"].(string)
					if json.Unmarshal([]byte(text), &args) != nil || call["type"] != "function_call" || call["name"] != "edit__apply_patch" || args["input"] != "prior patch" || result["type"] != "function_call_output" || result["output"] != "applied" {
						t.Error("custom tool history/result not lowered")
					}
				}
				args, _ := json.Marshal(map[string]any{"input": patch})
				item := map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call_new", "name": "edit__apply_patch", "arguments": string(args), "status": "completed"}
				response := map[string]any{"id": "resp_custom", "object": "response", "status": "completed", "model": "grok-4.20-multi-agent-0309", "output": []any{item}, "usage": map[string]any{"input_tokens": 2, "output_tokens": 1}}
				if !stream {
					raw, _ := json.Marshal(response)
					providerAcceptanceResponse(w, string(raw))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(kind string, event map[string]any) {
					event["type"] = kind
					raw, _ := json.Marshal(event)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
				}
				emit("response.created", map[string]any{"response": map[string]any{"id": "resp_custom", "object": "response", "status": "in_progress", "output": []any{}}})
				emit("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call_new", "name": "edit__apply_patch", "arguments": "", "status": "in_progress"}})
				// Split escaped JSON across events; no partial wrapper should become custom input.
				for _, part := range []string{string(args[:len(args)/2]), string(args[len(args)/2:])} {
					emit("response.function_call_arguments.delta", map[string]any{"output_index": 0, "item_id": "fc_fixture", "delta": part})
				}
				emit("response.function_call_arguments.done", map[string]any{"output_index": 0, "item_id": "fc_fixture"})
				emit("response.output_item.done", map[string]any{"output_index": 0, "item": item})
				emit("response.completed", map[string]any{"response": response})
			})
			request, _ := json.Marshal(map[string]any{"model": "fixture", "stream": stream, "tools": []any{map[string]any{"type": "namespace", "name": "edit", "tools": []any{map[string]any{"type": "custom", "name": "apply_patch", "format": map[string]any{"type": "text"}}}}}, "tool_choice": map[string]any{"type": "custom", "namespace": "edit", "name": "apply_patch"}, "input": []any{map[string]any{"role": "user", "content": "edit fixture"}, map[string]any{"type": "custom_tool_call", "namespace": "edit", "name": "apply_patch", "call_id": "call_old", "input": "prior patch"}, map[string]any{"type": "custom_tool_call_output", "call_id": "call_old", "output": "applied"}}})
			w := f.call("/v1/responses", string(request), nil)
			providerAcceptanceStatus(t, w, 200)
			assertItem := func(v any) {
				t.Helper()
				m, _ := v.(map[string]any)
				if m["type"] != "custom_tool_call" || m["id"] != "ctc_fixture" || m["call_id"] != "call_new" || m["name"] != "apply_patch" || m["namespace"] != "edit" || m["input"] != patch || m["arguments"] != nil {
					t.Error("custom response identity/input not restored")
				}
			}
			if !stream {
				var out map[string]any
				acceptanceOK(t, json.Unmarshal(w.Body.Bytes(), &out))
				items, _ := out["output"].([]any)
				if len(items) != 1 {
					t.Fatal("custom response output missing")
				}
				assertItem(items[0])
			} else {
				done, completed := false, false
				for _, block := range strings.Split(w.Body.String(), "\n\n") {
					var event string
					var data map[string]any
					for _, line := range strings.Split(block, "\n") {
						if strings.HasPrefix(line, "event: ") {
							event = strings.TrimPrefix(line, "event: ")
						}
						if strings.HasPrefix(line, "data: ") {
							_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
						}
					}
					if data == nil {
						continue
					}
					if event != "" && event != data["type"] {
						t.Error("SSE event name and rewritten payload type disagree")
					}
					switch data["type"] {
					case "response.function_call_arguments.delta", "response.function_call_arguments.done":
						t.Error("function argument event escaped custom tool restoration")
					case "response.custom_tool_call_input.delta":
						if data["delta"] != "" {
							t.Error("partial JSON wrapper leaked as custom input")
						}
					case "response.custom_tool_call_input.done":
						done = true
						if data["input"] != patch || data["item_id"] != "ctc_fixture" {
							t.Error("completed custom stream input mismatch")
						}
					case "response.output_item.done":
						assertItem(data["item"])
					case "response.completed":
						completed = true
						response, _ := data["response"].(map[string]any)
						items, _ := response["output"].([]any)
						if len(items) != 1 {
							t.Error("terminal custom snapshot missing")
						} else {
							assertItem(items[0])
						}
					}
				}
				if !done || !completed {
					t.Error("custom stream terminal events missing")
				}
			}
			if sends.Load() != 1 {
				t.Fatal("custom tool request unexpectedly replayed")
			}
		})
	}
}
