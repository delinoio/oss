// SPDX-License-Identifier: Apache-2.0
// Native protocol behavior is adapted from opencodex; see LICENSE.upstream.
package ollama

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

func nativeContent(v any, imagesAllowed bool) (string, []any, error) {
	if v == nil {
		return "", nil, nil
	}
	if s, ok := v.(string); ok {
		return s, nil, nil
	}
	parts, ok := v.([]any)
	if !ok {
		return "", nil, bad("ollama_invalid_content")
	}
	var text strings.Builder
	var images []any
	for _, p := range parts {
		m := obj(p)
		switch str(m["type"]) {
		case "text", "input_text", "output_text":
			text.WriteString(str(m["text"]))
		case "image_url", "input_image":
			if !imagesAllowed {
				return "", nil, bad("ollama_developer_image_unsupported")
			}
			s := str(m["image_url"])
			if s == "" {
				s = str(obj(m["image_url"])["url"])
			}
			if strings.HasPrefix(s, "data:") {
				header, data, ok := strings.Cut(s, ",")
				if !ok || !strings.HasPrefix(strings.ToLower(header), "data:image/") || !strings.HasSuffix(header, ";base64") {
					return "", nil, bad("ollama_invalid_image")
				}
				s = data
			}
			s = strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return -1
				}
				return r
			}, s)
			if s == "" {
				return "", nil, bad("ollama_invalid_image")
			}
			if _, e := base64.StdEncoding.DecodeString(s); e != nil {
				if _, e = base64.RawStdEncoding.DecodeString(s); e != nil {
					return "", nil, bad("ollama_invalid_image")
				}
			}
			images = append(images, s)
		default:
			return "", nil, bad("ollama_content_unsupported")
		}
	}
	return text.String(), images, nil
}

type historyCall struct {
	id, name string
	results  []object
	settled  bool
}

func nativeMessages(input []any) ([]any, error) {
	out := []any{}
	deferred := []any{}
	var pending []*historyCall
	issued := map[string]*historyCall{}
	lateBytes := 0
	flush := func() {
		for _, c := range pending {
			content := []string{}
			images := []any{}
			for _, r := range c.results {
				content = append(content, str(r["content"]))
				images = append(images, arr(r["images"])...)
			}
			if len(content) == 0 {
				content = append(content, fmt.Sprintf("[ocx] no tool result was recorded for %q; execution status unknown — do not treat this as success, failure, or user-provided input.", c.name))
			}
			m := object{"role": "tool", "tool_call_id": c.id, "tool_name": c.name, "content": strings.Join(content, "\n")}
			if len(images) > 0 {
				m["images"] = images
			}
			out = append(out, m)
			c.settled = true
		}
		pending = nil
		out = append(out, deferred...)
		deferred = nil
	}
	for _, raw := range input {
		m := obj(raw)
		role := str(m["role"])
		text, images, e := nativeContent(m["content"], role != "developer")
		if e != nil {
			return nil, e
		}
		if m["is_error"] == true {
			text = "ERROR: " + text
		}
		n := object{"role": role, "content": text}
		if len(images) > 0 {
			n["images"] = images
		}
		if role == "tool" {
			c := issued[str(m["tool_call_id"])]
			if c == nil {
				return nil, bad("ollama_orphan_tool_result")
			}
			if name := str(m["name"]); name != "" && name != c.name {
				return nil, bad("ollama_mismatched_tool_result")
			}
			if !c.settled {
				c.results = append(c.results, n)
			} else {
				prefix := fmt.Sprintf("[ocx] additional output for previously issued tool %q (%s):\n", c.name, c.id)
				encoded, _ := json.Marshal(prefix)
				lateBytes += len(encoded)
				if lateBytes > MaxLateAttributionBytes {
					return nil, bad("ollama_late_attribution_limit")
				}
				n["role"] = "user"
				n["content"] = prefix + text
				if len(pending) > 0 {
					deferred = append(deferred, n)
				} else {
					out = append(out, n)
				}
			}
			continue
		}
		if role == "developer" {
			n["role"] = "system"
		}
		if role == "assistant" {
			if thinking := str(m["reasoning_content"]); thinking != "" {
				n["thinking"] = thinking
			}
		}
		calls := arr(m["tool_calls"])
		if len(pending) > 0 {
			unresolved := false
			for _, c := range pending {
				unresolved = unresolved || len(c.results) == 0
			}
			if role == "user" || role == "developer" || (role == "assistant" && len(calls) == 0 && unresolved) {
				deferred = append(deferred, n)
				continue
			}
			flush()
		}
		switch role {
		case "system", "developer", "user", "assistant":
		default:
			return nil, bad("ollama_invalid_role")
		}
		if len(calls) > 0 {
			if role != "assistant" {
				return nil, bad("ollama_invalid_tool_call")
			}
			native := []any{}
			for i, raw := range calls {
				c := obj(raw)
				f := obj(c["function"])
				id, name := str(c["id"]), str(f["name"])
				if id == "" || name == "" || issued[id] != nil {
					return nil, bad("ollama_invalid_tool_identity")
				}
				var args object
				switch v := f["arguments"].(type) {
				case string:
					args, e = decode([]byte(v))
				case map[string]any:
					args = v
				default:
					e = bad("ollama_invalid_tool_arguments")
				}
				if e != nil || args == nil {
					return nil, bad("ollama_invalid_tool_arguments")
				}
				call := &historyCall{id: id, name: name}
				issued[id] = call
				pending = append(pending, call)
				native = append(native, object{"type": "function", "id": id, "function": object{"index": i, "name": name, "arguments": args}})
			}
			n["tool_calls"] = native
		}
		out = append(out, n)
	}
	flush()
	return out, nil
}
func buildBody(b object, kind, model string) (object, error) {
	if model == "" {
		model = str(b["model"])
	}
	if model == "" {
		return nil, bad("missing_model")
	}
	messages, ok := b["messages"].([]any)
	if !ok {
		return nil, bad("ollama_missing_messages")
	}
	native, e := nativeMessages(messages)
	if e != nil {
		return nil, e
	}
	out := object{"model": model, "messages": native, "stream": b["stream"] == true}
	if effort, ok := b["reasoning_effort"]; ok {
		switch str(effort) {
		case "none", "disabled", "false":
			out["think"] = false
		case "enabled", "adaptive", "true":
			out["think"] = true
		case "minimal":
			out["think"] = "low"
		case "low", "medium", "high", "max":
			out["think"] = effort
		default:
			return nil, bad("ollama_unsupported_reasoning")
		}
	}
	if f := obj(b["response_format"]); f != nil && str(f["type"]) != "text" {
		if kind == "cloud" {
			return nil, bad("ollama_cloud_structured_output_unsupported")
		}
		switch str(f["type"]) {
		case "json_object":
			out["format"] = "json"
		case "json_schema":
			schema := obj(obj(f["json_schema"])["schema"])
			if schema == nil {
				return nil, bad("ollama_invalid_schema")
			}
			out["format"] = schema
		default:
			return nil, bad("ollama_invalid_format")
		}
	}
	options := object{}
	for _, pair := range [][2]string{{"max_tokens", "num_predict"}, {"max_completion_tokens", "num_predict"}, {"temperature", "temperature"}, {"top_p", "top_p"}, {"stop", "stop"}, {"presence_penalty", "presence_penalty"}, {"frequency_penalty", "frequency_penalty"}} {
		if v, ok := b[pair[0]]; ok {
			options[pair[1]] = v
		}
	}
	if len(options) > 0 {
		out["options"] = options
	}
	choice := b["tool_choice"]
	tools := arr(b["tools"])
	if len(tools) > 0 && choice != "none" {
		if choice == "required" || (obj(choice) != nil && str(obj(choice)["type"]) != "allowed_tools") {
			return nil, bad("ollama_required_tool_unsupported")
		}
		allowed := map[string]bool{}
		if c := obj(choice); c != nil {
			if c["mode"] == "required" {
				return nil, bad("ollama_required_tool_unsupported")
			}
			for _, t := range arr(c["tools"]) {
				allowed[str(obj(obj(t)["function"])["name"])] = true
			}
		}
		seen := map[string]bool{}
		nativeTools := []any{}
		for _, t := range tools {
			tool := obj(t)
			f := obj(tool["function"])
			name := str(f["name"])
			if obj(choice) != nil && !allowed[name] {
				continue
			}
			if tool["type"] != "function" || name == "" || seen[name] || obj(f["parameters"]) == nil {
				return nil, bad("ollama_invalid_tool")
			}
			seen[name] = true
			fn := object{"name": name, "parameters": f["parameters"]}
			if d := str(f["description"]); d != "" {
				fn["description"] = d
			}
			nativeTools = append(nativeTools, object{"type": "function", "function": fn})
		}
		if len(nativeTools) > 0 {
			out["tools"] = nativeTools
		}
	}
	return out, nil
}
