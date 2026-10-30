// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"fmt"
	"strings"
)

const emptyOutput = "[ocx] empty tool output: the tool ran but produced no stdout or return value; do not treat this as success, failure, or user-provided input."

func emptyText(v any) bool {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range a {
		m := obj(x)
		switch m["type"] {
		case "text", "input_text", "output_text":
			s, ok := m["text"].(string)
			if !ok || strings.TrimSpace(s) != "" {
				return false
			}
		case "refusal":
			s, ok := m["refusal"].(string)
			if !ok || strings.TrimSpace(s) != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func toolKey(m map[string]any) (key string, output bool) {
	id := str(m["call_id"])
	if id == "" {
		return "", false
	}
	switch m["type"] {
	case "function_call", "local_shell_call":
		return "f:" + id, false
	case "custom_tool_call":
		return "c:" + id, false
	case "function_call_output":
		return "f:" + id, true
	case "custom_tool_call_output":
		return "c:" + id, true
	}
	return "", false
}
func pairOutputs(in []any, repairOrphans bool) []any {
	calls, outputs := map[string]bool{}, map[string]bool{}
	for _, x := range in {
		k, o := toolKey(obj(x))
		if k != "" {
			if o {
				outputs[k] = true
			} else {
				calls[k] = true
			}
		}
	}
	out, pending := []any{}, []any{}
	flush := func() { out = append(out, pending...); pending = nil }
	for _, x := range in {
		m := obj(x)
		k, o := toolKey(m)
		if k != "" && !o {
			out = append(out, x)
			if !outputs[k] {
				name := str(m["name"])
				if name == "" {
					name = str(m["call_id"])
				}
				kind := "function_call_output"
				if strings.HasPrefix(k, "c:") {
					kind = "custom_tool_call_output"
				}
				pending = append(pending, map[string]any{"type": kind, "call_id": m["call_id"], "output": fmt.Sprintf("[ocx] no tool result was recorded for %q; execution status unknown — do not treat this as success, failure, or user-provided input.", name)})
			}
			continue
		}
		flush()
		if repairOrphans && o && !calls[k] {
			if s, ok := m["output"].(string); ok {
				out = append(out, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "[tool output for " + str(m["call_id"]) + "]\n" + s}}})
				continue
			}
		}
		out = append(out, x)
	}
	flush()
	return out
}

// This follows the strict Responses plaintext contract only. Authenticated replay
// restoration and explicit missing-state errors must have already run.
func backfillToolReasoning(in []any) []any {
	starts := map[int]bool{}
	start := -1
	reasoning, tool := false, false
	closeTurn := func() {
		if start >= 0 && tool && !reasoning {
			starts[start] = true
		}
		start = -1
		reasoning = false
		tool = false
	}
	for i, x := range in {
		m := obj(x)
		kind := str(m["type"])
		assistant := kind == "reasoning" || (kind == "message" && m["role"] == "assistant") || strings.HasSuffix(kind, "_call")
		if !assistant {
			closeTurn()
			continue
		}
		if start < 0 {
			start = i
		}
		reasoning = reasoning || kind == "reasoning"
		tool = tool || kind == "function_call" || kind == "custom_tool_call" || kind == "local_shell_call"
	}
	closeTurn()
	out := []any{}
	for i, x := range in {
		if starts[i] {
			out = append(out, map[string]any{"type": "reasoning", "summary": []any{}, "content": []any{map[string]any{"type": "reasoning_text", "text": " "}}})
		}
		out = append(out, x)
	}
	return out
}
