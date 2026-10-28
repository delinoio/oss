// SPDX-License-Identifier: Apache-2.0
package google

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type message struct {
	phase    string
	role     string
	parts    []any
	resultID string
	result   any
}

var callIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var sentinelModelPattern = regexp.MustCompile(`(?i)^gemini[-.\d]`)

func (a *Adapter) request(root object, wire inference.Wire, model string) (object, string, error) {
	// Unrepresented options fail before dispatch instead of silently weakening a
	// tool constraint, output schema or reasoning request.
	allowed := strings.Fields("model stream messages input instructions system tools tool_choice temperature top_p max_tokens max_completion_tokens max_output_tokens stop stop_sequences reasoning reasoning_effort thinking output_config response_format text metadata user store stream_options parallel_tool_calls")
	for key := range root {
		found := false
		for _, k := range allowed {
			if key == k {
				found = true
				break
			}
		}
		if !found {
			return nil, "", unsupported()
		}
	}
	if v := root["stream"]; v != nil {
		if _, ok := v.(bool); !ok {
			return nil, "", invalid()
		}
	}
	if root["parallel_tool_calls"] == false {
		return nil, "", unsupported()
	}
	msgs, err := parseMessages(root, wire)
	if err != nil {
		return nil, "", err
	}
	contents, system, anchor, err := compileMessages(msgs, model)
	if err != nil {
		return nil, "", err
	}
	body := object{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = object{"parts": system}
	}
	toolInput, choice, err := filterToolSubset(root["tools"], root["tool_choice"], wire)
	if err != nil {
		return nil, "", err
	}
	tools, strict, err := parseTools(toolInput, wire)
	if err != nil {
		return nil, "", err
	}
	if len(tools) > 0 {
		body["tools"] = []any{object{"functionDeclarations": tools}}
		config, e := toolConfig(choice, wire, tools, strict)
		if e != nil {
			return nil, "", e
		}
		if config != nil {
			body["toolConfig"] = object{"functionCallingConfig": config}
		}
	} else if root["tool_choice"] != nil && root["tool_choice"] != "auto" && root["tool_choice"] != "none" {
		return nil, "", invalid()
	}
	if a.cca && strings.HasPrefix(model, "claude-") {
		if root["tool_choice"] == "none" || obj(root["tool_choice"])["type"] == "none" {
			delete(body, "tools")
		}
		body["toolConfig"] = object{"functionCallingConfig": object{"mode": "VALIDATED"}}
	}
	gc := object{}
	for from, to := range map[string]string{"temperature": "temperature", "top_p": "topP"} {
		if v := root[from]; v != nil {
			if _, ok := v.(json.Number); !ok {
				return nil, "", invalid()
			}
			gc[to] = v
		}
	}
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if v := root[key]; v != nil {
			n, ok := v.(json.Number)
			if !ok {
				return nil, "", invalid()
			}
			x, e := n.Int64()
			if e != nil || x <= 0 {
				return nil, "", invalid()
			}
			gc["maxOutputTokens"] = x
			break
		}
	}
	stop := root["stop_sequences"]
	if stop == nil {
		stop = root["stop"]
	}
	if stop != nil {
		if s, ok := stop.(string); ok {
			stop = []any{s}
		}
		ss, ok := stop.([]any)
		if !ok {
			return nil, "", invalid()
		}
		for _, v := range ss {
			if _, ok := v.(string); !ok {
				return nil, "", invalid()
			}
		}
		gc["stopSequences"] = ss
	}
	thinking := object{}
	effort := text(root["reasoning_effort"])
	if root["reasoning_effort"] != nil && effort == "" {
		return nil, "", invalid()
	}
	if root["reasoning"] != nil && obj(root["reasoning"]) == nil {
		return nil, "", invalid()
	}
	if root["thinking"] != nil && obj(root["thinking"]) == nil {
		return nil, "", invalid()
	}
	if r := obj(root["reasoning"]); r != nil {
		for k := range r {
			if k != "effort" && k != "summary" {
				return nil, "", unsupported()
			}
		}
		if r["summary"] != nil && r["summary"] != "none" && !a.options.ShowThinkingSummaries {
			return nil, "", unsupported()
		}
		if r["effort"] != nil {
			effort = text(r["effort"])
			if effort == "" {
				return nil, "", invalid()
			}
		}
	}
	if effort != "" {
		switch effort {
		case "minimal", "low", "medium", "high", "max", "xhigh", "ultra", "none":
			thinking["thinkingLevel"] = effort
		default:
			return nil, "", unsupported()
		}
	}
	if config := obj(root["output_config"]); config != nil {
		if level := normalizedEffort(text(config["effort"])); level != "" {
			thinking["thinkingLevel"] = level
		}
	}
	if t := obj(root["thinking"]); t != nil {
		switch text(t["type"]) {
		case "adaptive":
		case "enabled":
			n, ok := t["budget_tokens"].(json.Number)
			if !ok {
				return nil, "", invalid()
			}
			x, e := n.Int64()
			if e != nil || x <= 0 {
				return nil, "", invalid()
			}
			level := "high"
			if x <= 4096 {
				level = "low"
			} else if x <= 16384 {
				level = "medium"
			}
			if thinking["thinkingLevel"] == nil {
				thinking["thinkingLevel"] = level
			}
		case "disabled":
			thinking["thinkingBudget"] = 0
		default:
			return nil, "", unsupported()
		}
	}
	if thinking["thinkingLevel"] != nil && thinking["thinkingBudget"] != nil {
		return nil, "", invalid()
	}
	if a.options.ShowThinkingSummaries && strings.HasPrefix(model, "gemini-") {
		thinking["includeThoughts"] = true
	}
	if len(thinking) > 0 {
		gc["thinkingConfig"] = thinking
	}
	format := obj(root["response_format"])
	if root["response_format"] != nil && format == nil {
		return nil, "", invalid()
	}
	if wire == inference.Responses {
		if root["text"] != nil && obj(root["text"]) == nil {
			return nil, "", invalid()
		}
		for k := range obj(root["text"]) {
			if k != "format" {
				return nil, "", unsupported()
			}
		}
		format = obj(obj(root["text"])["format"])
		if obj(root["text"])["format"] != nil && format == nil {
			return nil, "", invalid()
		}
	}
	if format != nil && format["type"] != "text" {
		if imageModel(model) {
			return nil, "", failure(400, "google_image_structured_output_unsupported")
		}
		if a.cca && !strings.HasPrefix(model, "gemini-") {
			return nil, "", unsupported()
		}
		gc["responseMimeType"] = "application/json"
		switch text(format["type"]) {
		case "json_object":
		case "json_schema":
			schema := format["schema"]
			if wire == inference.Chat {
				schema = obj(format["json_schema"])["schema"]
			}
			if obj(schema) == nil {
				return nil, "", invalid()
			}
			gc["responseJsonSchema"] = schema
		default:
			return nil, "", unsupported()
		}
	}
	if len(gc) > 0 {
		body["generationConfig"] = gc
	}
	return body, anchor, nil
}

func parseMessages(root object, wire inference.Wire) ([]message, error) {
	var out []message
	add := func(role string, content any) error {
		if role != "user" && role != "assistant" && role != "system" && role != "developer" {
			return invalid()
		}
		parts, err := contentParts(content)
		if err != nil {
			return err
		}
		out = append(out, message{role: role, parts: parts})
		return nil
	}
	switch wire {
	case inference.Chat, inference.Anthropic:
		if wire == inference.Anthropic && root["system"] != nil {
			if err := add("system", root["system"]); err != nil {
				return nil, err
			}
		}
		messages, ok := root["messages"].([]any)
		if !ok {
			return nil, invalid()
		}
		for _, raw := range messages {
			m := obj(raw)
			if m == nil {
				return nil, invalid()
			}
			role := text(m["role"])
			if m["reasoning_content"] != nil && obj(obj(m["extra_content"])["google"])["parts"] == nil {
				return nil, unsupported()
			}
			if role != "user" && role != "assistant" && role != "system" && role != "developer" && !(role == "tool" && wire == inference.Chat) {
				return nil, invalid()
			}
			if role == "tool" && wire == inference.Chat {
				out = append(out, message{role: "tool", resultID: text(m["tool_call_id"]), result: m["content"]})
				continue
			}
			if wire == inference.Anthropic {
				if blocks, ok := m["content"].([]any); ok {
					var parts []any
					signedThinking := false
					for _, b := range blocks {
						if obj(b)["type"] == "thinking" && realSignature(text(obj(b)["signature"])) {
							signedThinking = true
							break
						}
					}
					if raw := obj(obj(m["extra_content"])["google"])["parts"]; raw != nil && !signedThinking {
						opaque, err := replayParts(raw)
						if err != nil {
							return nil, err
						}
						// Visible thinking blocks already carry their exact signature.
						for _, p := range opaque {
							found := false
							for _, b := range blocks {
								if obj(b)["type"] == "thinking" && text(obj(b)["signature"]) == signature(obj(p)) {
									found = true
									break
								}
							}
							if !found {
								parts = append(parts, p)
							}
						}
					}
					flush := func() {
						if len(parts) > 0 {
							out = append(out, message{role: role, parts: parts, phase: text(m["phase"])})
							parts = nil
						}
					}
					for _, rawPart := range blocks {
						p := obj(rawPart)
						switch text(p["type"]) {
						case "tool_result":
							if role != "user" {
								return nil, invalid()
							}
							flush()
							result := object{"result": p["content"]}
							if p["is_error"] == true {
								result = object{"error": p["content"]}
							}
							out = append(out, message{role: "tool", resultID: text(p["tool_use_id"]), result: result})
						case "tool_use":
							if role != "assistant" {
								return nil, invalid()
							}
							part, err := callPart(p["id"], p["name"], p["input"], p)
							if err != nil {
								return nil, err
							}
							parts = append(parts, part)
						case "thinking":
							if role != "assistant" {
								return nil, invalid()
							}
							sig := text(p["signature"])
							if !realSignature(sig) {
								return nil, unsupported()
							}
							parts = append(parts, object{"thought": true, "text": p["thinking"], "thoughtSignature": sig})
						default:
							pp, err := contentParts([]any{p})
							if err != nil {
								return nil, err
							}
							parts = append(parts, pp...)
						}
					}
					flush()
					continue
				}
			}
			if err := add(role, m["content"]); err != nil {
				return nil, err
			}
			last := &out[len(out)-1]
			last.phase = text(m["phase"])
			if opaque := obj(obj(m["extra_content"])["google"]); opaque["parts"] != nil {
				if role != "assistant" {
					return nil, invalid()
				}
				parts, err := replayParts(opaque["parts"])
				if err != nil {
					return nil, err
				}
				last.parts = append(parts, last.parts...)
			}
			for _, rawCall := range array(m["tool_calls"]) {
				if role != "assistant" {
					return nil, invalid()
				}
				call := obj(rawCall)
				f := obj(call["function"])
				if call["type"] != "function" {
					return nil, unsupported()
				}
				args, err := arguments(f["arguments"])
				if err != nil {
					return nil, err
				}
				part, err := callPart(call["id"], f["name"], args, call)
				if err != nil {
					return nil, err
				}
				last.parts = append(last.parts, part)
			}
		}
	case inference.Responses:
		if root["instructions"] != nil {
			if err := add("system", root["instructions"]); err != nil {
				return nil, err
			}
		}
		if s, ok := root["input"].(string); ok {
			if err := add("user", s); err != nil {
				return nil, err
			}
			return out, nil
		}
		items, ok := root["input"].([]any)
		if !ok {
			return nil, invalid()
		}
		for _, raw := range items {
			m := obj(raw)
			if m == nil {
				return nil, invalid()
			}
			switch text(m["type"]) {
			case "", "message":
				if err := add(text(m["role"]), m["content"]); err != nil {
					return nil, err
				}
				out[len(out)-1].phase = text(m["phase"])
			case "function_call":
				args, err := arguments(m["arguments"])
				if err != nil {
					return nil, err
				}
				part, err := callPart(m["call_id"], m["name"], args, m)
				if err != nil {
					return nil, err
				}
				// Responses puts parallel calls in adjacent items. Keep a single
				// model turn so their results can be paired as one adjacent batch.
				if len(out) > 0 && out[len(out)-1].role == "assistant" {
					out[len(out)-1].parts = append(out[len(out)-1].parts, part)
				} else {
					out = append(out, message{role: "assistant", parts: []any{part}})
				}
			case "function_call_output":
				out = append(out, message{role: "tool", resultID: text(m["call_id"]), result: m["output"]})
			case "reasoning":
				parts, err := replayParts(obj(obj(m["extra_content"])["google"])["parts"])
				if err != nil || len(parts) == 0 {
					return nil, unsupported()
				}
				out = append(out, message{role: "assistant", parts: parts})
			default:
				return nil, unsupported()
			}
		}
	default:
		return nil, inference.ErrUnsupported
	}
	return out, nil
}

func arguments(v any) (object, error) {
	s, ok := v.(string)
	if !ok {
		return nil, invalid()
	}
	m, err := decodeJSON([]byte(s))
	if err != nil {
		return nil, invalid()
	}
	return m, nil
}
func callPart(id, name, args any, source object) (object, error) {
	if !validSourceToolName(text(name)) || obj(args) == nil {
		return nil, invalid()
	}
	if text(id) == "" {
		encoded, _ := json.Marshal(args)
		return object{"text": "[tool_use without a usable id: " + text(name) + "]\n" + string(encoded)}, nil
	}
	p := object{"functionCall": object{"id": id, "name": name, "args": args}}
	if sig := signature(source); realSignature(sig) {
		p["thoughtSignature"] = sig
	}
	return p, nil
}
func replayParts(v any) ([]any, error) {
	parts, ok := v.([]any)
	if !ok {
		return nil, invalid()
	}
	for _, raw := range parts {
		p := obj(raw)
		if p == nil || p["thought"] != true || !realSignature(signature(p)) {
			return nil, unsupported()
		}
		for k := range p {
			if k != "thought" && k != "text" && k != "thoughtSignature" && k != "thought_signature" && k != "extra_content" {
				return nil, unsupported()
			}
		}
	}
	return parts, nil
}

func contentParts(v any) ([]any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return nil, nil
		}
		return []any{object{"text": s}}, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, invalid()
	}
	var parts []any
	for _, raw := range list {
		p := obj(raw)
		if p == nil {
			return nil, invalid()
		}
		switch text(p["type"]) {
		case "text", "input_text", "output_text":
			s, ok := p["text"].(string)
			if !ok {
				return nil, invalid()
			}
			if s != "" {
				parts = append(parts, object{"text": s})
			}
		case "image_url", "input_image":
			u := text(p["image_url"])
			if obj(p["image_url"]) != nil {
				u = text(obj(p["image_url"])["url"])
			}
			head, data, ok := strings.Cut(u, ",")
			if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
				return nil, unsupported()
			}
			mime := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
			if _, err := base64.StdEncoding.DecodeString(data); err != nil {
				return nil, invalid()
			}
			parts = append(parts, object{"inlineData": object{"mimeType": mime, "data": data}})
		case "image", "document":
			s := obj(p["source"])
			if s["type"] != "base64" {
				return nil, unsupported()
			}
			if _, err := base64.StdEncoding.DecodeString(text(s["data"])); err != nil {
				return nil, invalid()
			}
			parts = append(parts, object{"inlineData": object{"mimeType": s["media_type"], "data": s["data"]}})
		default:
			return nil, unsupported()
		}
	}
	return parts, nil
}

func compileMessages(msgs []message, model string) ([]any, []any, string, error) {
	normalizeCallIDs(msgs)
	var contents, system []any
	anchor := ""
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.role == "system" || m.role == "developer" {
			system = append(system, m.parts...)
			continue
		}
		if m.role == "tool" {
			parts, err := orphanResult(m)
			if err != nil {
				return nil, nil, "", err
			}
			contents = append(contents, object{"role": "user", "parts": parts})
			continue
		}
		role := "user"
		if m.role == "assistant" {
			role = "model"
		}
		parts := m.parts
		if strings.Contains(strings.ToLower(model), "gemini") && m.phase != "final_answer" {
			parts = pruneProgress(parts)
		}
		if len(parts) == 0 {
			if role == "model" {
				continue
			}
			parts = []any{object{"text": "(empty)"}}
		}
		if role == "user" && anchor == "" {
			for _, p := range parts {
				if s := text(obj(p)["text"]); s != "" {
					anchor = s
					break
				}
			}
		}
		var calls []object
		for _, raw := range parts {
			p := obj(raw)
			if call := obj(p["functionCall"]); call != nil {
				calls = append(calls, call)
				if len(calls) == 1 && sentinelModelPattern.MatchString(model) && !realSignature(signature(p)) {
					p["thoughtSignature"] = "skip_thought_signature_validator"
				}
			}
		}
		if len(contents) == 0 && len(calls) > 0 {
			contents = append(contents, object{"role": "user", "parts": []any{object{"text": "(continue)"}}})
		}
		contents = append(contents, object{"role": role, "parts": parts})
		if len(calls) == 0 {
			continue
		}
		results := map[string]any{}
		var orphans []message
		j := i + 1
		for j < len(msgs) && msgs[j].role == "tool" {
			id := msgs[j].resultID
			if _, exists := results[id]; exists {
				orphans = append(orphans, msgs[j])
				j++
				continue
			}
			results[id] = msgs[j].result
			j++
		}
		seen := map[string]bool{}
		var responses []any
		var images []any
		for _, call := range calls {
			id := text(call["id"])
			if seen[id] {
				return nil, nil, "", invalid()
			}
			seen[id] = true
			value, exists := results[id]
			if !exists {
				value = "[missing tool_result for this tool_use in history]"
			} else {
				delete(results, id)
			}
			response, media, err := toolResult(value)
			if err != nil {
				return nil, nil, "", err
			}
			responses = append(responses, object{"functionResponse": object{"id": id, "name": call["name"], "response": response}})
			images = append(images, media...)
		}
		responses = append(responses, images...)
		// Preserve source order for unmatched results rather than map iteration.
		for k := i + 1; k < j; k++ {
			if _, exists := results[msgs[k].resultID]; exists {
				orphans = append(orphans, msgs[k])
				delete(results, msgs[k].resultID)
			}
		}
		for _, orphan := range orphans {
			parts, err := orphanResult(orphan)
			if err != nil {
				return nil, nil, "", err
			}
			responses = append(responses, parts...)
		}
		contents = append(contents, object{"role": "user", "parts": responses})
		i = j - 1
	}
	if len(contents) == 0 || obj(contents[len(contents)-1])["role"] == "model" {
		contents = append(contents, object{"role": "user", "parts": []any{object{"text": "(continue)"}}})
	}
	return contents, system, anchor, nil
}

// Reserve conforming IDs before allocating transformed IDs, so a raw ID that
// already equals another call's hash-suffixed alias cannot steal its result.
func normalizeCallIDs(msgs []message) {
	used := map[string]bool{}
	aliases := map[string]string{}
	for _, m := range msgs {
		for _, p := range m.parts {
			id := text(obj(obj(p)["functionCall"])["id"])
			if callIDPattern.MatchString(id) {
				used[id] = true
				aliases[id] = id
			}
		}
	}
	for _, m := range msgs {
		for _, p := range m.parts {
			call := obj(obj(p)["functionCall"])
			if call == nil {
				continue
			}
			id := text(call["id"])
			alias, exists := aliases[id]
			if !exists {
				var b strings.Builder
				for _, c := range id {
					if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
						b.WriteRune(c)
					} else {
						b.WriteByte('_')
					}
				}
				h := sha256.Sum256([]byte(id))
				base := b.String() + "_" + hex.EncodeToString(h[:4])
				alias = base
				for suffix := 1; used[alias]; suffix++ {
					alias = fmt.Sprintf("%s_%d", base, suffix)
				}
				used[alias] = true
				aliases[id] = alias
			}
			call["id"] = alias
		}
	}
	for i := range msgs {
		if alias, exists := aliases[msgs[i].resultID]; exists {
			msgs[i].resultID = alias
		}
	}
}

func toolResult(value any) (object, []any, error) {
	if response := obj(value); response != nil {
		return response, nil, nil
	}
	if value == nil || value == "" {
		return object{"result": "(empty tool output)"}, nil, nil
	}
	if s, ok := value.(string); ok {
		return object{"result": s}, nil, nil
	}
	parts, err := contentParts(value)
	if err != nil {
		return nil, nil, err
	}
	var lines []string
	var media []any
	for _, raw := range parts {
		p := obj(raw)
		if s := text(p["text"]); s != "" {
			lines = append(lines, s)
		}
		if p["inlineData"] != nil {
			media = append(media, p)
			lines = append(lines, "[image]")
		}
	}
	result := strings.Join(lines, "\n")
	if result == "" {
		result = "(empty tool output)"
	}
	return object{"result": result}, media, nil
}

func orphanResult(m message) ([]any, error) {
	response, media, err := toolResult(m.result)
	if err != nil {
		return nil, err
	}
	result := text(response["result"])
	if result == "" {
		encoded, _ := json.Marshal(response)
		result = string(encoded)
	}
	parts := []any{object{"text": "[tool_result without adjacent tool_use: " + m.resultID + "]\n" + result}}
	return append(parts, media...), nil
}

func parseTools(v any, wire inference.Wire) ([]any, bool, error) {
	if v == nil {
		return nil, false, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false, invalid()
	}
	var tools []any
	strict := false
	seen := map[string]bool{}
	for _, raw := range list {
		t := obj(raw)
		if t == nil {
			return nil, false, invalid()
		}
		if wire == inference.Chat {
			if t["type"] != "function" {
				return nil, false, unsupported()
			}
			t = obj(t["function"])
		} else if wire == inference.Responses && t["type"] != "function" {
			return nil, false, unsupported()
		}
		name := text(t["name"])
		if !validSourceToolName(name) || seen[name] {
			return nil, false, invalid()
		}
		seen[name] = true
		schema := t["parameters"]
		if wire == inference.Anthropic {
			schema = t["input_schema"]
		}
		if schema == nil {
			schema = object{"type": "object", "properties": object{}}
		}
		tool := object{"name": name, "parameters": schema}
		if t["description"] != nil {
			tool["description"] = t["description"]
		}
		tools = append(tools, tool)
		strict = strict || t["strict"] == true
	}
	return tools, strict, nil
}

func toolConfig(v any, wire inference.Wire, tools []any, strict bool) (object, error) {
	mode := "auto"
	name := ""
	if v != nil {
		if s, ok := v.(string); ok {
			mode = s
		} else {
			m := obj(v)
			if m == nil {
				return nil, invalid()
			}
			if wire == inference.Chat {
				name = text(obj(m["function"])["name"])
			} else {
				name = text(m["name"])
			}
			mode = text(m["type"])
		}
	}
	if name != "" {
		found := false
		for _, t := range tools {
			if obj(t)["name"] == name {
				found = true
			}
		}
		if !found {
			return nil, invalid()
		}
		return object{"mode": "ANY", "allowedFunctionNames": []any{name}}, nil
	}
	switch mode {
	case "auto":
		if strict {
			return object{"mode": "VALIDATED"}, nil
		}
		return nil, nil
	case "none":
		return object{"mode": "NONE"}, nil
	case "required", "any":
		return object{"mode": "ANY"}, nil
	default:
		return nil, unsupported()
	}
}
