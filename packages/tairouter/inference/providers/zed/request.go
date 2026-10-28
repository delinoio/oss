// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func buildProviderRequest(b object, wire inference.Wire, provider Provider) (object, error) {
	var err error
	if provider == Google {
		return googleRequest(b, wire)
	}
	want := inference.Chat
	if provider == OpenAI {
		want = inference.Responses
	}
	if provider == Anthropic {
		want = inference.Anthropic
	}
	if wire != want {
		b, err = translate(b, wire, want)
		if err != nil {
			return nil, err
		}
	}
	switch provider {
	case OpenAI:
		// These private Responses controls are not accepted by Zed's parser.
		for _, k := range []string{"access_programs"} {
			delete(b, k)
		}
		if s, ok := b["input"].(string); ok {
			b["input"] = []any{object{"type": "message", "role": "user", "content": s}}
		}
		if items, ok := b["input"].([]any); ok {
			filtered := make([]any, 0, len(items))
			for _, item := range items {
				if objectOf(item)["type"] != "configuration_update" {
					filtered = append(filtered, item)
				}
			}
			b["input"] = filtered
		}
		for _, v := range arrayOf(b["input"]) {
			m := objectOf(v)
			if m == nil {
				return nil, requestError("invalid_zed_input")
			}
			delete(m, "internal_chat_message_metadata_passthrough")
			// Tool call/output items keep their native shape and identifiers.
			if t := stringOf(m["type"]); t != "" && t != "message" {
				continue
			}
			m["type"] = "message"
			if m["role"] == nil {
				m["role"] = "user"
			}
			if m["role"] == "developer" {
				m["role"] = "system"
			}
			if s, ok := m["content"].(string); ok {
				m["content"] = []any{object{"type": "input_text", "text": s}}
			}
			for _, p := range arrayOf(m["content"]) {
				if q := objectOf(p); q["type"] == "text" {
					q["type"] = "input_text"
				}
			}
		}
		if tools, ok := b["tools"].([]any); ok {
			tools = supportedTools(tools)
			for _, tool := range tools {
				delete(objectOf(tool), "defer_loading")
			}
			b["tools"] = tools
			c := objectOf(b["tool_choice"])
			if c["type"] == "allowed_tools" {
				allowed := supportedTools(arrayOf(c["tools"]))
				if len(allowed) == 0 {
					b["tool_choice"] = "none"
				} else {
					c["tools"] = allowed
				}
			} else if (c != nil && !supportedTool(c)) || (b["tool_choice"] == "required" && len(tools) == 0) {
				b["tool_choice"] = "none"
			}
		}
	case Anthropic:
		for _, v := range arrayOf(b["messages"]) {
			m := objectOf(v)
			if m == nil {
				return nil, requestError("invalid_zed_message")
			}
			if s, ok := m["content"].(string); ok {
				m["content"] = []any{object{"type": "text", "text": s}}
			}
		}
		if b["max_tokens"] == nil {
			b["max_tokens"] = 8192
		}
		applyShortCache(b)
	case XAI:
		b["stream_options"] = object{"include_usage": true}
	default:
		return nil, inference.ErrUnsupported
	}
	return b, nil
}

func supportedTool(m object) bool { return m["type"] == "function" || m["type"] == "custom" }
func supportedTools(input []any) []any {
	out := []any{}
	for _, v := range input {
		if supportedTool(objectOf(v)) {
			out = append(out, v)
		}
	}
	return out
}

// Cross-wire requests use a bounded canonical message list. Unsupported rich fields fail
// explicitly instead of silently discarding instructions, reasoning, or tool semantics.
func translate(b object, from, to inference.Wire) (object, error) {
	if choice := objectOf(b["tool_choice"]); choice["type"] == "allowed_tools" {
		names := map[string]bool{}
		for _, v := range arrayOf(choice["tools"]) {
			s := objectOf(v)
			names[stringOf(s["name"])] = true
		}
		tools := []any{}
		for _, v := range arrayOf(b["tools"]) {
			t := objectOf(v)
			name := stringOf(t["name"])
			if f := objectOf(t["function"]); f != nil {
				name = stringOf(f["name"])
			}
			if names[name] {
				tools = append(tools, v)
			}
		}
		b["tools"] = tools
		b["tool_choice"] = "auto"
		if choice["mode"] == "required" {
			b["tool_choice"] = "required"
		}
		if len(tools) == 0 {
			b["tool_choice"] = "none"
		}
	}
	allowed := map[string]bool{"model": true, "stream": true, "messages": true, "input": true, "instructions": true, "system": true, "tools": true, "tool_choice": true, "temperature": true, "top_p": true, "max_tokens": true, "max_completion_tokens": true, "max_output_tokens": true, "stream_options": true, "store": true, "parallel_tool_calls": true}
	for _, k := range strings.Fields("reasoning reasoning_effort thinking response_format text stop stop_sequences metadata user") {
		allowed[k] = true
	}
	for k := range b {
		if !allowed[k] {
			return nil, requestError("unsupported_zed_translation_field")
		}
	}
	messages, err := canonicalMessages(b, from)
	if err != nil {
		return nil, err
	}
	out := object{}
	out["model"] = b["model"]
	applyTranslatedOptions(out, b, from, to)
	for _, k := range []string{"temperature", "top_p"} {
		if v, ok := b[k]; ok {
			out[k] = v
		}
	}
	max := b["max_output_tokens"]
	if max == nil {
		max = b["max_completion_tokens"]
	}
	if max == nil {
		max = b["max_tokens"]
	}
	var result, system []any
	for _, v := range messages {
		m := objectOf(v)
		role := stringOf(m["role"])
		content := m["content"]
		if to == inference.Chat {
			for _, v := range arrayOf(m["tool_calls"]) {
				c := objectOf(v)
				if c["type"] == "custom" {
					custom := objectOf(c["custom"])
					c["type"] = "function"
					c["function"] = object{"name": custom["name"], "arguments": jsonString(object{"input": custom["input"]})}
					delete(c, "custom")
				}
			}
			delete(m, "custom_result")
			result = append(result, m)
			continue
		}
		if to == inference.Responses {
			result = append(result, responseReasoning(m)...)
			if role == "tool" {
				converted, err := contentFor(content, inference.Responses, role)
				if err != nil {
					return nil, err
				}
				kind := "function_call_output"
				if m["custom_result"] == true {
					kind = "custom_tool_call_output"
				}
				result = append(result, object{"type": kind, "call_id": m["tool_call_id"], "output": converted})
				continue
			}
			if content != nil {
				converted, err := contentFor(content, to, role)
				if err != nil {
					return nil, err
				}
				result = append(result, object{"type": "message", "role": role, "content": converted})
			}
			for _, v := range arrayOf(m["tool_calls"]) {
				c := objectOf(v)
				f := objectOf(c["function"])
				item := object{"type": "function_call", "call_id": c["id"], "name": f["name"], "arguments": f["arguments"]}
				if c["type"] == "custom" {
					custom := objectOf(c["custom"])
					item = object{"type": "custom_tool_call", "call_id": c["id"], "name": custom["name"], "input": custom["input"]}
				}
				for _, k := range []string{"extra_content", "thoughtSignature", "providerMetadata"} {
					if c[k] != nil {
						item[k] = c[k]
					}
				}
				result = append(result, item)
			}
			continue
		}
		parts := reasoningBlocks(m)
		if content != nil {
			converted, err := contentFor(content, to, role)
			if err != nil {
				return nil, err
			}
			parts = append(parts, arrayOf(converted)...)
		}
		if role == "system" || role == "developer" {
			system = append(system, parts...)
			continue
		}
		if role == "tool" {
			role = "user"
			parts = []any{object{"type": "tool_result", "tool_use_id": m["tool_call_id"], "content": parts}}
			if m["is_error"] == true {
				objectOf(parts[0])["is_error"] = true
			}
		}
		for _, v := range arrayOf(m["tool_calls"]) {
			c := objectOf(v)
			f := objectOf(c["function"])
			if c["type"] == "custom" {
				custom := objectOf(c["custom"])
				args, _ := json.Marshal(object{"input": custom["input"]})
				f = object{"name": custom["name"], "arguments": string(args)}
			}
			args, err := decodeObject([]byte(stringOf(f["arguments"])))
			if err != nil {
				return nil, requestError("invalid_tool_arguments")
			}
			parts = append(parts, object{"type": "tool_use", "id": c["id"], "name": f["name"], "input": args})
		}
		result = append(result, object{"role": role, "content": parts})
	}
	switch to {
	case inference.Chat:
		out["messages"] = result
		if max != nil {
			out["max_completion_tokens"] = max
		}
	case inference.Responses:
		out["input"] = result
		if max != nil {
			out["max_output_tokens"] = max
		}
	case inference.Anthropic:
		out["messages"] = result
		if len(system) > 0 {
			out["system"] = system
		}
		if max != nil {
			out["max_tokens"] = max
		}
	}
	if tools, ok := b["tools"].([]any); ok {
		converted := []any{}
		for _, v := range tools {
			f := objectOf(v)
			custom := f["type"] == "custom"
			if custom {
				if to == inference.Responses {
					converted = append(converted, f)
					continue
				}
				f = customFunction(f)
			}
			if from == inference.Chat {
				if !custom && f["type"] != "function" {
					return nil, inference.ErrUnsupported
				}
				if !custom {
					f = objectOf(f["function"])
				}
			}
			if from == inference.Responses && !custom && f["type"] != "function" {
				return nil, inference.ErrUnsupported
			}
			if from == inference.Anthropic && f["type"] != nil {
				return nil, inference.ErrUnsupported
			}
			params := f["parameters"]
			if from == inference.Anthropic {
				params = f["input_schema"]
			}
			fn := object{"name": f["name"], "description": f["description"], "parameters": params}
			if f["strict"] != nil {
				fn["strict"] = f["strict"]
			}
			switch to {
			case inference.Chat:
				converted = append(converted, object{"type": "function", "function": fn})
			case inference.Responses:
				fn["type"] = "function"
				converted = append(converted, fn)
			case inference.Anthropic:
				t := object{"name": f["name"], "description": f["description"], "input_schema": params}
				if f["strict"] != nil {
					t["strict"] = f["strict"]
				}
				converted = append(converted, t)
			}
		}
		out["tools"] = converted
	}
	if choice, ok := b["tool_choice"]; ok {
		name, mode := "", stringOf(choice)
		if c := objectOf(choice); c != nil {
			if from == inference.Chat {
				name = stringOf(objectOf(c["function"])["name"])
				if name == "" {
					name = stringOf(objectOf(c["custom"])["name"])
				}
			} else {
				name = stringOf(c["name"])
			}
			if from == inference.Anthropic {
				mode = stringOf(c["type"])
				if mode == "any" {
					mode = "required"
				}
			}
		}
		if name != "" {
			switch to {
			case inference.Chat:
				out["tool_choice"] = object{"type": "function", "function": object{"name": name}}
			case inference.Responses:
				kind := "function"
				if objectOf(choice)["type"] == "custom" {
					kind = "custom"
				}
				out["tool_choice"] = object{"type": kind, "name": name}
			case inference.Anthropic:
				out["tool_choice"] = object{"type": "tool", "name": name}
			}
		} else {
			if mode != "auto" && mode != "none" && mode != "required" {
				return nil, inference.ErrUnsupported
			}
			if to == inference.Anthropic {
				if mode == "required" {
					mode = "any"
				}
				out["tool_choice"] = object{"type": mode}
			} else {
				out["tool_choice"] = mode
			}
		}
	}
	if parallel, ok := b["parallel_tool_calls"]; ok {
		if to == inference.Anthropic {
			value, valid := parallel.(bool)
			if !valid {
				return nil, requestError("invalid_parallel_tool_calls")
			}
			choice := objectOf(out["tool_choice"])
			if choice == nil {
				choice = object{"type": "auto"}
				out["tool_choice"] = choice
			}
			choice["disable_parallel_tool_use"] = !value
		} else {
			out["parallel_tool_calls"] = parallel
		}
	}
	if to == inference.Anthropic {
		finishAnthropicOptions(out, b)
	}
	return out, nil
}

func textContent(v any) (any, error) {
	return richContent(v)
}

func canonicalMessages(b object, wire inference.Wire) ([]any, error) {
	out := []any{}
	switch wire {
	case inference.Chat:
		for _, v := range arrayOf(b["messages"]) {
			m := objectOf(v)
			if m == nil {
				return nil, requestError("invalid_zed_message")
			}
			for key := range m {
				switch key {
				case "role", "content", "tool_calls", "tool_call_id", "name", "reasoning_content", "reasoning_details", "extra_content", "is_error", "custom_result":
				default:
					return nil, inference.ErrUnsupported
				}
			}
			for _, call := range arrayOf(m["tool_calls"]) {
				c := objectOf(call)
				if (c["type"] != "function" || objectOf(c["function"]) == nil) && (c["type"] != "custom" || objectOf(c["custom"]) == nil) {
					return nil, inference.ErrUnsupported
				}
			}
			c, err := textContent(m["content"])
			if err != nil {
				return nil, err
			}
			m["content"] = c
			out = append(out, m)
		}
	case inference.Responses:
		if b["instructions"] != nil {
			out = append(out, object{"role": "system", "content": b["instructions"]})
		}
		input := b["input"]
		if s, ok := input.(string); ok {
			input = []any{object{"role": "user", "content": s}}
		}
		for _, v := range arrayOf(input) {
			m := objectOf(v)
			switch stringOf(m["type"]) {
			case "", "message":
				c, err := textContent(m["content"])
				if err != nil {
					return nil, err
				}
				out = append(out, object{"role": m["role"], "content": c})
			case "function_call":
				call := object{"id": m["call_id"], "type": "function", "function": object{"name": m["name"], "arguments": m["arguments"]}}
				for _, k := range []string{"extra_content", "providerMetadata", "thoughtSignature"} {
					if m[k] != nil {
						call[k] = m[k]
					}
				}
				out = append(out, object{"role": "assistant", "tool_calls": []any{call}})
			case "custom_tool_call":
				out = append(out, object{"role": "assistant", "tool_calls": []any{object{"id": m["call_id"], "type": "custom", "custom": object{"name": m["name"], "input": m["input"]}}}})
			case "function_call_output", "custom_tool_call_output":
				content, err := richContent(m["output"])
				if err != nil {
					return nil, err
				}
				out = append(out, object{"role": "tool", "tool_call_id": m["call_id"], "content": content, "custom_result": m["type"] == "custom_tool_call_output"})
			case "reasoning":
				r, err := envelopeMessage(m)
				if err != nil {
					return nil, err
				}
				out = append(out, r)
			default:
				return nil, inference.ErrUnsupported
			}
		}
	case inference.Anthropic:
		if b["system"] != nil {
			c, err := textContent(b["system"])
			if err != nil {
				return nil, err
			}
			out = append(out, object{"role": "system", "content": c})
		}
		for _, v := range arrayOf(b["messages"]) {
			m := objectOf(v)
			if s, ok := m["content"].(string); ok {
				out = append(out, object{"role": m["role"], "content": s})
				continue
			}
			for _, p := range arrayOf(m["content"]) {
				q := objectOf(p)
				switch q["type"] {
				case "text", "image", "document":
					content, err := richContent([]any{q})
					if err != nil {
						return nil, err
					}
					out = append(out, object{"role": m["role"], "content": content})
				case "thinking", "redacted_thinking", "server_tool_use", "web_search_tool_result":
					out = append(out, object{"role": "assistant", "reasoning_content": q["thinking"], "extra_content": object{"anthropic": object{"content": []any{q}}}})
				case "tool_use":
					args, _ := json.Marshal(q["input"])
					out = append(out, object{"role": "assistant", "tool_calls": []any{object{"id": q["id"], "type": "function", "function": object{"name": q["name"], "arguments": string(args)}}}})
				case "tool_result":
					c, err := textContent(q["content"])
					if err != nil {
						return nil, err
					}
					out = append(out, object{"role": "tool", "tool_call_id": q["tool_use_id"], "content": c, "is_error": q["is_error"]})
				default:
					return nil, inference.ErrUnsupported
				}
			}
		}
	default:
		return nil, inference.ErrUnsupported
	}
	if len(out) == 0 {
		return nil, requestError("missing_zed_messages")
	}
	customIDs := map[string]bool{}
	for _, v := range out {
		for _, call := range arrayOf(objectOf(v)["tool_calls"]) {
			c := objectOf(call)
			if c["type"] == "custom" {
				customIDs[stringOf(c["id"])] = true
			}
		}
	}
	for _, v := range out {
		m := objectOf(v)
		if m["role"] == "tool" && customIDs[stringOf(m["tool_call_id"])] {
			m["custom_result"] = true
		}
	}
	return mergeAssistantMessages(out), nil
}
