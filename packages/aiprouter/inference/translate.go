// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

type object = map[string]any

func obj(v any) object { m, _ := v.(map[string]any); return m }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func clone(m object) object {
	n := object{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func decode(b []byte) (object, error) {
	var m object
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if e := d.Decode(&m); e != nil || m == nil {
		return nil, &Error{400, "invalid_json", 0}
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, &Error{400, "invalid_json", 0}
	}
	return m, nil
}
func unsupportedField() error { return &Error{400, "unsupported_translation_field", 0} }

// translateRequest uses Chat's message structure as a private intermediary. Native paths
// retain unknown fields; translated paths reject fields whose semantics cannot be preserved.
func translateRequest(body object, from, to Wire) (object, error) {
	if from == to {
		return clone(body), nil
	}
	allowed := map[string]bool{"model": true, "stream": true, "messages": true, "input": true, "instructions": true, "system": true, "tools": true, "tool_choice": true, "temperature": true, "top_p": true, "max_tokens": true, "max_completion_tokens": true, "max_output_tokens": true, "parallel_tool_calls": true, "store": true, "previous_response_id": true, "stream_options": true, "metadata": true, "user": true, "reasoning": true, "reasoning_effort": true, "thinking": true, "include": true, "text": true, "response_format": true, "stop": true, "stop_sequences": true, "additional_tools": true, "output_config": true}
	for k := range body {
		if !allowed[k] {
			return nil, unsupportedField()
		}
	}
	catalog := append([]any{}, arr(body["tools"])...)
	catalog = append(catalog, arr(body["additional_tools"])...)
	for _, v := range arr(body["input"]) {
		q := obj(v)
		if q["type"] == "additional_tools" || q["type"] == "tool_search_output" {
			catalog = append(catalog, arr(q["tools"])...)
		}
	}
	messages, err := toMessages(body, from)
	if err != nil {
		return nil, err
	}
	out := object{"model": body["model"], "stream": body["stream"]}
	if err := translateOptions(body, out, from, to); err != nil {
		return nil, err
	}
	for _, k := range []string{"temperature", "top_p", "metadata"} {
		if v, ok := body[k]; ok {
			out[k] = v
		}
	}
	max := body["max_output_tokens"]
	if max == nil {
		max = body["max_completion_tokens"]
	}
	if max == nil {
		max = body["max_tokens"]
	}
	switch to {
	case Chat:
		out["messages"] = messages
		if max != nil {
			out["max_completion_tokens"] = max
		}
		if body["stream"] == true {
			out["stream_options"] = object{"include_usage": true}
		}
	case Responses:
		input, err := messagesToResponses(messages)
		if err != nil {
			return nil, err
		}
		out["input"] = input
		out["store"] = false
		if max != nil {
			out["max_output_tokens"] = max
		}
	case Anthropic:
		msgs, system, err := messagesToAnthropic(messages)
		if err != nil {
			return nil, err
		}
		out["messages"] = msgs
		if len(system) > 0 {
			out["system"] = system
		}
		if max == nil {
			max = 8192
		}
		out["max_tokens"] = max
	default:
		return nil, ErrUnsupported
	}
	if tools := catalog; len(tools) > 0 {
		converted, e := translateTools(tools, from, to)
		if e != nil {
			return nil, e
		}
		out["tools"] = converted
	}
	if choice, ok := body["tool_choice"]; ok {
		c, e := translateChoice(choice, from, to)
		if e != nil {
			return nil, e
		}
		out["tool_choice"] = c
		if choice := obj(body["tool_choice"]); choice["type"] == "allowed_tools" && to == Anthropic {
			permitted := map[string]bool{}
			for _, v := range arr(choice["tools"]) {
				selected, e := translateChoice(v, from, Anthropic)
				if e != nil {
					return nil, e
				}
				permitted[str(obj(selected)["name"])] = true
			}
			selected := []any{}
			for _, v := range arr(out["tools"]) {
				if permitted[str(obj(v)["name"])] {
					selected = append(selected, v)
				}
			}
			if len(selected) == 0 {
				return nil, &Error{400, "empty_allowed_tools", 0}
			}
			out["tools"] = selected
		}
	}
	if v, ok := body["parallel_tool_calls"]; ok {
		if to == Anthropic {
			choice := obj(out["tool_choice"])
			if choice == nil {
				choice = object{"type": "auto"}
			}
			choice["disable_parallel_tool_use"] = v == false
			out["tool_choice"] = choice
		} else {
			out["parallel_tool_calls"] = v
		}
	}
	return out, nil
}
func translateChoice(v any, from, to Wire) (any, error) {
	mode := str(v)
	name, namespace, kind := "", "", "function"
	if m := obj(v); m != nil {
		kind = str(m["type"])
		namespace = str(m["namespace"])
		if kind == "allowed_tools" {
			mode = str(m["mode"])
			if mode == "" {
				mode = "auto"
			}
			if mode != "auto" && mode != "required" {
				return nil, unsupportedField()
			}
			if to == Anthropic {
				if mode == "required" {
					mode = "any"
				}
				return object{"type": mode}, nil
			}
			selected := []any{}
			for _, v := range arr(m["tools"]) {
				c, e := translateChoice(v, from, to)
				if e != nil {
					return nil, e
				}
				selected = append(selected, c)
			}
			return object{"type": "allowed_tools", "mode": mode, "tools": selected}, nil
		}
		name = str(m["name"])
		if from == Chat {
			if f := obj(m["function"]); f != nil {
				name = str(f["name"])
			}
			if f := obj(m["custom"]); f != nil {
				name = str(f["name"])
			}
		}
		if from == Anthropic {
			mode = kind
			if kind == "any" {
				mode = "required"
			}
			kind = "function"
		}
		if name == "" && (kind == "tool_search" || kind == "web_search" || kind == "image_generation" || kind == "video_generation") {
			name = kind
		}
	}
	if name != "" {
		wireName := name
		if namespace != "" && namespace != "functions" {
			wireName = namespace + "__" + name
		}
		switch to {
		case Chat:
			return object{"type": "function", "function": object{"name": wireName}}, nil
		case Anthropic:
			return object{"type": "tool", "name": wireName}, nil
		case Responses:
			q := object{"type": kind, "name": name}
			if namespace != "" {
				q["namespace"] = namespace
			}
			return q, nil
		}
	}
	if mode != "auto" && mode != "none" && mode != "required" {
		return nil, unsupportedField()
	}
	if to == Anthropic {
		if mode == "required" {
			mode = "any"
		}
		return object{"type": mode}, nil
	}
	return mode, nil
}

func toMessages(b object, w Wire) ([]any, error) {
	var out []any
	switch w {
	case Chat:
		return arr(b["messages"]), nil
	case Responses:
		if s := str(b["instructions"]); s != "" {
			out = append(out, object{"role": "system", "content": s})
		}
		if s, ok := b["input"].(string); ok {
			return append(out, object{"role": "user", "content": s}), nil
		}
		for _, v := range arr(b["input"]) {
			m := obj(v)
			switch str(m["type"]) {
			case "additional_tools":
				continue
			case "reasoning":
				msg, e := reasoningMessage(m)
				if e != nil {
					return nil, e
				}
				out = append(out, msg)
			case "function_call", "custom_tool_call", "tool_search_call", "web_search_call", "image_generation_call", "video_generation_call":
				out = append(out, object{"role": "assistant", "tool_calls": []any{responseCallToChat(m)}})
			case "function_call_output", "custom_tool_call_output", "tool_search_output":
				content, e := contentToChat(m["output"], Responses)
				if e != nil {
					return nil, e
				}
				msg := object{"role": "tool", "tool_call_id": m["call_id"], "content": content}
				copyFields(msg, m, "is_error", "extra_content", "provider_metadata", "additional_tools")
				if m["type"] == "custom_tool_call_output" {
					msg["custom_result"] = true
				}
				if m["type"] == "tool_search_output" {
					raw, _ := json.Marshal(object{"tools": m["tools"], "status": m["status"]})
					msg["content"] = string(raw)
					msg["provider_metadata"] = object{"output_item": clone(m)}
				}
				out = append(out, msg)
			case "", "message":
				c, e := contentToChat(m["content"], w)
				if e != nil {
					return nil, e
				}
				mm := object{"role": m["role"], "content": c}
				copyFields(mm, m, "extra_content", "provider_metadata")
				out = append(out, mm)
			default:
				return nil, unsupportedField()
			}
		}
	case Anthropic:
		if b["system"] != nil {
			c, e := contentToChat(b["system"], w)
			if e != nil {
				return nil, e
			}
			out = append(out, object{"role": "system", "content": c})
		}
		for _, v := range arr(b["messages"]) {
			m := obj(v)
			if s, ok := m["content"].(string); ok {
				out = append(out, object{"role": m["role"], "content": s})
				continue
			}
			var parts, calls []any
			flush := func() {
				if len(parts) > 0 || len(calls) > 0 {
					mm := object{"role": m["role"], "content": parts}
					copyFields(mm, m, "extra_content", "provider_metadata")
					if len(calls) > 0 {
						mm["tool_calls"] = calls
					}
					out = append(out, mm)
					parts = nil
					calls = nil
				}
			}
			for _, p := range arr(m["content"]) {
				q := obj(p)
				if item := obj(obj(obj(q["extra_content"])["aiprouter"])["hosted_output"]); item != nil {
					flush()
					out = append(out, object{"role": "assistant", "_hosted_output": item})
					continue
				}
				switch str(q["type"]) {
				case "thinking", "redacted_thinking":
					flush()
					item := anthropicReasoning(q)
					mm, e := reasoningMessage(item)
					if e != nil {
						return nil, e
					}
					out = append(out, mm)
				case "server_tool_use", "tool_use":
					flush()
					args, _ := json.Marshal(q["input"])
					call := object{"id": q["id"], "type": "function", "function": object{"name": q["name"], "arguments": string(args)}}
					copyFields(call, q, "extra_content", "provider_metadata")
					if q["type"] == "server_tool_use" && call["provider_metadata"] == nil {
						call["provider_metadata"] = object{"tool_kind": "web_search", "tool_name": q["name"]}
					}
					calls = append(calls, call)
					flush()
				case "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
					flush()
					out = append(out, object{"role": "assistant", "content": []any{clone(q)}})
				case "tool_result":
					flush()

					c, e := contentToChat(q["content"], w)
					if e != nil {
						return nil, e
					}
					mm := object{"role": "tool", "tool_call_id": q["tool_use_id"], "content": c}
					copyFields(mm, q, "is_error", "extra_content", "provider_metadata")
					out = append(out, mm)
				default:
					c, e := contentToChat([]any{p}, w)
					if e != nil {
						return nil, e
					}
					parts = append(parts, arr(c)...)
					flush()
				}
			}
			flush()
		}
	default:
		return nil, ErrUnsupported
	}
	return out, nil
}
func contentToChat(v any, w Wire) (any, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	parts := []any{}
	if v != nil {
		if _, ok := v.([]any); !ok {
			return nil, unsupportedField()
		}
	}
	for _, p := range arr(v) {
		m := obj(p)
		switch str(m["type"]) {
		case "text", "input_text", "output_text":
			q := clone(m)
			q["type"] = "text"
			parts = append(parts, q)
		case "image_url", "document", "file", "input_file", "input_audio", "audio", "refusal", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
			parts = append(parts, clone(m))
		case "input_image":
			parts = append(parts, object{"type": "image_url", "image_url": object{"url": m["image_url"], "detail": m["detail"]}})
		case "image":
			s := obj(m["source"])
			u := str(s["url"])
			if str(s["type"]) == "base64" {
				u = "data:" + str(s["media_type"]) + ";base64," + str(s["data"])
			}
			parts = append(parts, object{"type": "image_url", "image_url": object{"url": u}})
		default:
			return nil, unsupportedField()
		}
	}
	return parts, nil
}
func messagesToResponses(msgs []any) ([]any, error) {
	out := []any{}
	for _, v := range msgs {
		m := obj(v)
		if item := obj(m["_hosted_output"]); item != nil {
			out = append(out, item)
			continue
		}
		reasoning, e := chatReasoning(m)
		if e != nil {
			return nil, e
		}
		out = append(out, reasoning...)
		if refusal, ok := m["refusal"].(string); ok && refusal != "" {
			out = append(out, object{"type": "message", "role": "assistant", "content": []any{object{"type": "refusal", "refusal": refusal}}})
		}
		if str(m["role"]) == "tool" {
			if item := obj(obj(m["provider_metadata"])["output_item"]); item != nil {
				out = append(out, clone(item))
				continue
			}
			output := m["content"]
			if parts := arr(output); parts != nil {
				converted, e := messagesToResponses([]any{object{"role": "user", "content": parts}})
				if e != nil {
					return nil, e
				}
				if len(converted) > 0 {
					output = obj(converted[0])["content"]
				}
			}
			item := object{"type": "function_call_output", "call_id": m["tool_call_id"], "output": output}
			if m["custom_result"] == true {
				item["type"] = "custom_tool_call_output"
			}
			copyFields(item, m, "is_error", "extra_content", "provider_metadata", "additional_tools")
			out = append(out, item)
			continue
		}
		hasContent := m["content"] != nil
		if (len(reasoning) > 0 || len(arr(m["tool_calls"])) > 0) && str(m["content"]) == "" && len(arr(m["content"])) == 0 {
			hasContent = false
		}
		if hasContent || m["extra_content"] != nil && len(reasoning) == 0 && len(arr(m["tool_calls"])) == 0 {
			parts := []any{}
			textType := "input_text"
			if str(m["role"]) == "assistant" {
				textType = "output_text"
			}
			if s, ok := m["content"].(string); ok {
				parts = append(parts, object{"type": textType, "text": s})
			} else {
				for _, p := range arr(m["content"]) {
					q := obj(p)
					switch str(q["type"]) {
					case "text":
						part := clone(q)
						part["type"] = textType
						parts = append(parts, part)
					case "file", "input_file", "document", "input_audio", "audio", "refusal", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
						parts = append(parts, clone(q))
					case "image_url":
						im := obj(q["image_url"])
						parts = append(parts, object{"type": "input_image", "image_url": im["url"], "detail": im["detail"]})
					default:
						return nil, unsupportedField()
					}
				}
			}
			item := object{"type": "message", "role": m["role"], "content": parts}
			copyFields(item, m, "extra_content", "provider_metadata")
			out = append(out, item)
		}
		for _, t := range arr(m["tool_calls"]) {
			c := obj(t)
			if c["type"] == "custom" {
				q := obj(c["custom"])
				args, _ := json.Marshal(object{"input": q["input"]})
				c = clone(c)
				c["function"] = object{"name": q["name"], "arguments": string(args)}
				meta := clone(obj(c["provider_metadata"]))
				meta["tool_kind"] = "custom"
				c["provider_metadata"] = meta
			}
			item, e := chatCallToResponse(c)
			if e != nil {
				return nil, e
			}
			out = append(out, item)
		}
	}
	return out, nil
}
func messagesToAnthropic(msgs []any, output ...bool) ([]any, []any, error) {
	out, system := []any{}, []any{}
	for _, v := range msgs {
		m := obj(v)
		role := str(m["role"])
		parts := []any{}
		reasoning, e := chatReasoning(m)
		if e != nil {
			return nil, nil, e
		}
		for _, v := range reasoning {
			parts = append(parts, reasoningToAnthropic(obj(v), output...)...)
		}
		if s, ok := m["content"].(string); ok {
			parts = append(parts, object{"type": "text", "text": s})
		} else {
			for _, p := range arr(m["content"]) {
				q := obj(p)
				switch str(q["type"]) {
				case "text", "document", "input_audio", "audio", "refusal", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
					parts = append(parts, q)
				case "input_file", "file":
					f := q
					if obj(q["file"]) != nil {
						f = obj(q["file"])
					}
					source := object{"type": "url", "url": f["file_url"]}
					if data := str(f["file_data"]); data != "" {
						head, body, ok := strings.Cut(data, ",")
						if !ok {
							return nil, nil, unsupportedField()
						}
						source = object{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": body}
					}
					parts = append(parts, object{"type": "document", "source": source, "title": f["filename"]})
				case "image_url":
					u := str(obj(q["image_url"])["url"])
					source := object{"type": "url", "url": u}
					if strings.HasPrefix(u, "data:") {
						head, data, ok := strings.Cut(u, ",")
						if !ok || !strings.HasSuffix(head, ";base64") {
							return nil, nil, unsupportedField()
						}
						source = object{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": data}
					}
					parts = append(parts, object{"type": "image", "source": source})
				default:
					return nil, nil, unsupportedField()
				}
			}
		}
		if role == "system" || role == "developer" {
			system = append(system, parts...)
			continue
		}
		if role == "tool" {
			role = "user"
			block := object{"type": "tool_result", "tool_use_id": m["tool_call_id"], "content": parts}
			copyFields(block, m, "is_error", "extra_content", "provider_metadata")
			parts = []any{block}
		}
		for _, v := range arr(m["tool_calls"]) {
			c := obj(v)
			f := obj(c["function"])
			args, err := decode([]byte(str(f["arguments"])))
			if err != nil {
				return nil, nil, &Error{400, "invalid_tool_arguments", 0}
			}
			block := object{"type": "tool_use", "id": c["id"], "name": f["name"], "input": args}
			copyFields(block, c, "extra_content", "provider_metadata")
			if kind := str(obj(c["provider_metadata"])["tool_kind"]); kind == "web_search" || kind == "image_generation" || kind == "video_generation" {
				block["type"] = "server_tool_use"
			}
			parts = append(parts, block)
		}
		mm := object{"role": role, "content": parts}
		copyFields(mm, m, "extra_content", "provider_metadata")
		out = append(out, mm)
	}
	return out, system, nil
}

type toolCall struct{ ID, Name, Arguments string }
type result struct {
	ID, Model, Text, Stop string
	Tools                 []toolCall
	Usage                 Usage
	Items                 []any
	Extra                 object
}

func token(v any) *int64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	i, e := n.Int64()
	if e != nil || i < 0 {
		return nil
	}
	return &i
}
func readUsage(m object, w Wire) Usage {
	u := obj(m["usage"])
	if u == nil {
		return Usage{}
	}
	if w == Chat {
		return Usage{InputTokens: token(u["prompt_tokens"]), OutputTokens: token(u["completion_tokens"]), CacheReadTokens: token(obj(u["prompt_tokens_details"])["cached_tokens"]), CacheWriteTokens: firstToken(u["cache_creation_input_tokens"], obj(u["prompt_tokens_details"])["cache_write_tokens"], obj(u["prompt_tokens_details"])["cache_creation_tokens"])}
	}
	v := Usage{InputTokens: token(u["input_tokens"]), OutputTokens: token(u["output_tokens"]), CacheReadTokens: token(obj(u["input_tokens_details"])["cached_tokens"]), CacheWriteTokens: firstToken(u["cache_creation_input_tokens"], obj(u["input_tokens_details"])["cache_write_tokens"])}
	if w == Anthropic {
		v.CacheReadTokens = token(u["cache_read_input_tokens"])
		v.CacheWriteTokens = token(u["cache_creation_input_tokens"])
		if v.InputTokens != nil {
			x := *v.InputTokens
			if v.CacheReadTokens != nil {
				if x > math.MaxInt64-*v.CacheReadTokens {
					v.InputTokens = nil
					return v
				}
				x += *v.CacheReadTokens
			}
			if v.CacheWriteTokens != nil {
				if x > math.MaxInt64-*v.CacheWriteTokens {
					v.InputTokens = nil
					return v
				}
				x += *v.CacheWriteTokens
			}
			v.InputTokens = &x
		}
	}
	return v
}
func wirePath(w Wire) string {
	switch w {
	case Chat:
		return "/chat/completions"
	case Anthropic:
		return "/messages"
	case Responses:
		return "/responses"
	}
	return fmt.Sprint("")
}

// Reasoning envelopes carry provider bytes, never authorization. Replay admission is
// performed by the caller with the original principal, route, model and account.
type reasoningEnvelope struct {
	Signature string   `json:"sig,omitempty"`
	Text      *string  `json:"txt,omitempty"`
	Redacted  []string `json:"red,omitempty"`
	Kiro      string   `json:"krc,omitempty"`
	Native    object   `json:"nat,omitempty"`
}

const reasoningPrefix = "ocxr1:"

func encodeReasoning(e reasoningEnvelope) string {
	b, _ := json.Marshal(e)
	return reasoningPrefix + base64.StdEncoding.EncodeToString(b)
}
func decodeReasoning(s string) (reasoningEnvelope, error) {
	var e reasoningEnvelope
	if len(s) > 256<<10 {
		return e, &Error{400, "reasoning_envelope_too_large", 0}
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, reasoningPrefix))
	if err != nil {
		return e, &Error{400, "invalid_reasoning_envelope", 0}
	}
	if _, err = decode(b); err != nil {
		return e, &Error{400, "invalid_reasoning_envelope", 0}
	}
	if json.Unmarshal(b, &e) != nil {
		return e, &Error{400, "invalid_reasoning_envelope", 0}
	}
	return e, nil
}
func copyFields(dst, src object, keys ...string) {
	for _, k := range keys {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
}
func firstToken(values ...any) *int64 {
	for _, v := range values {
		if n := token(v); n != nil {
			return n
		}
	}
	return nil
}
func reasoningText(item object) string {
	text := ""
	for _, v := range arr(item["summary"]) {
		text += str(obj(v)["text"])
	}
	if text == "" {
		for _, v := range arr(item["content"]) {
			text += str(obj(v)["text"])
		}
	}
	return text
}
func reasoningItem(text string, e reasoningEnvelope) object {
	item := object{"type": "reasoning", "summary": []any{}}
	if text != "" {
		item["summary"] = []any{object{"type": "summary_text", "text": text}}
	}
	if e.Signature != "" || e.Kiro != "" || len(e.Redacted) > 0 || e.Text != nil || e.Native != nil {
		item["encrypted_content"] = encodeReasoning(e)
	}
	return item
}
func anthropicReasoning(block object) object {
	if item := obj(obj(obj(block["extra_content"])["openai"])["reasoning"]); item != nil {
		return clone(item)
	}
	e := reasoningEnvelope{Signature: str(block["signature"])}
	text := str(block["thinking"])
	if block["type"] == "redacted_thinking" {
		e.Redacted = []string{str(block["data"])}
	}
	if strings.HasPrefix(e.Signature, reasoningPrefix) {
		if parsed, err := decodeReasoning(e.Signature); err == nil {
			e = parsed
		}
	}
	item := reasoningItem(text, e)
	copyFields(item, block, "extra_content", "provider_metadata")
	return item
}
func reasoningMessage(item object) (object, error) {
	m := object{"role": "assistant", "reasoning_content": reasoningText(item)}
	copyFields(m, item, "extra_content", "provider_metadata")
	// Preserve the exact item for opaque native ciphertext and lossless round trips.
	extra := clone(obj(m["extra_content"]))
	extra["openai"] = object{"reasoning": clone(item)}
	m["extra_content"] = extra
	enc := str(item["encrypted_content"])
	if strings.HasPrefix(enc, reasoningPrefix) {
		e, err := decodeReasoning(enc)
		if err != nil {
			return nil, err
		}
		if e.Kiro != "" {
			m["kiro_reasoning"] = e.Kiro
		}
		if e.Signature != "" {
			m["reasoning_signature"] = e.Signature
		}
		text := reasoningText(item)
		if e.Text != nil {
			text = *e.Text
		}
		details := []any{}
		for _, red := range e.Redacted {
			details = append(details, object{"type": "reasoning.encrypted", "data": red, "format": "anthropic-claude-v1"})
		}
		if e.Signature != "" && e.Text == nil {
			details = append(details, object{"type": "reasoning.text", "text": text, "signature": e.Signature, "format": "anthropic-claude-v1"})
		}
		if len(details) > 0 {
			m["reasoning_details"] = details
		}
	}
	return m, nil
}
func chatReasoning(m object) ([]any, error) {
	if items := arr(obj(obj(m["extra_content"])["openai"])["reasoning_items"]); len(items) > 0 {
		return items, nil
	}
	if item := obj(obj(obj(m["extra_content"])["openai"])["reasoning"]); item != nil {
		return []any{clone(item)}, nil
	}
	out := []any{}
	for _, v := range arr(m["reasoning_details"]) {
		d := obj(v)
		e := reasoningEnvelope{}
		switch str(d["type"]) {
		case "reasoning.encrypted":
			e.Redacted = []string{str(d["data"])}
		case "reasoning.text":
			e.Signature = str(d["signature"])
		default:
			return nil, unsupportedField()
		}
		out = append(out, reasoningItem(str(d["text"]), e))
	}
	if len(out) == 0 && (m["reasoning_content"] != nil || m["reasoning_signature"] != nil || m["kiro_reasoning"] != nil) {
		out = append(out, reasoningItem(str(m["reasoning_content"]), reasoningEnvelope{Signature: str(m["reasoning_signature"]), Kiro: str(m["kiro_reasoning"])}))
	}
	// Raw Google thoughts are independent of the optional visible summary.
	if parts := arr(obj(obj(m["extra_content"])["google"])["parts"]); len(parts) > 0 {
		if len(out) == 0 {
			out = append(out, reasoningItem("", reasoningEnvelope{}))
		}
		item := obj(out[0])
		extra := clone(obj(item["extra_content"]))
		extra["google"] = object{"parts": parts}
		item["extra_content"] = extra
	}
	return out, nil
}
func reasoningToAnthropic(item object, output ...bool) []any {
	text := reasoningText(item)
	enc := str(item["encrypted_content"])
	e := reasoningEnvelope{}
	if strings.HasPrefix(enc, reasoningPrefix) {
		e, _ = decodeReasoning(enc)
	}
	out := []any{}
	for _, red := range e.Redacted {
		out = append(out, object{"type": "redacted_thinking", "data": red})
	}
	hiddenOutput := len(output) > 0 && output[0] && e.Text != nil
	if e.Text != nil && !hiddenOutput {
		text = *e.Text
	}
	if text != "" || e.Signature != "" || e.Kiro != "" || enc != "" || item["extra_content"] != nil {
		b := object{"type": "thinking", "thinking": text}
		copyFields(b, item, "extra_content", "provider_metadata")
		switch {
		case e.Kiro != "" || hiddenOutput:
			b["signature"] = enc
		case e.Signature != "":
			b["signature"] = e.Signature
		case enc != "":
			// Native ciphertext stays in an explicit carrier, not a fabricated provider signature.
			extra := clone(obj(b["extra_content"]))
			extra["openai"] = object{"reasoning": item}
			b["extra_content"] = extra
		}
		out = append(out, b)
	}
	return out
}
func responseCallToChat(item object) object {
	kind := strings.TrimSuffix(str(item["type"]), "_call")
	if kind == "custom_tool" {
		kind = "custom"
	}
	name := str(item["name"])
	if name == "" {
		name = kind
	}
	namespace := str(item["namespace"])
	wireName := name
	if namespace != "" {
		wireName = namespace + "__" + name
	}
	args := item["arguments"]
	if args != nil {
		if _, ok := args.(string); !ok {
			b, _ := json.Marshal(args)
			args = string(b)
		}
	}
	if kind == "custom" {
		b, _ := json.Marshal(object{"input": item["input"]})
		args = string(b)
	}
	if args == nil {
		action := item["action"]
		if action == nil {
			action = object{}
		}
		b, _ := json.Marshal(action)
		args = string(b)
	}
	c := object{"id": item["call_id"], "type": "function", "function": object{"name": wireName, "arguments": args}}
	if c["id"] == nil {
		c["id"] = item["id"]
	}
	copyFields(c, item, "extra_content", "provider_metadata")
	meta := clone(obj(c["provider_metadata"]))
	meta["tool_kind"] = kind
	meta["tool_name"] = name
	if namespace != "" {
		meta["tool_namespace"] = namespace
	}
	c["provider_metadata"] = meta
	if kind != "function" && kind != "custom" {
		meta["output_item"] = clone(item)
	}
	return c
}
func chatCallToResponse(c object) (object, error) {
	f := obj(c["function"])
	meta := obj(c["provider_metadata"])
	kind := str(meta["tool_kind"])
	if item := obj(meta["output_item"]); item != nil {
		return clone(item), nil
	}
	if kind == "" {
		kind = "function"
	}
	if obj(obj(f["extra_content"])["aiprouter"])["custom_tool"] == true || obj(obj(c["extra_content"])["aiprouter"])["custom_tool"] == true {
		kind = "custom"
	}
	name := str(meta["tool_name"])
	if name == "" {
		name = str(f["name"])
	}
	item := object{"type": "function_call", "call_id": c["id"], "name": name, "arguments": f["arguments"]}
	copyFields(item, c, "extra_content", "provider_metadata")
	if ns := str(meta["tool_namespace"]); ns != "" {
		item["namespace"] = ns
	}
	switch kind {
	case "custom":
		args, e := decode([]byte(str(f["arguments"])))
		if e != nil {
			return nil, &Error{502, "invalid_custom_tool_arguments", 0}
		}
		input, ok := args["input"].(string)
		if !ok {
			return nil, &Error{502, "invalid_custom_tool_arguments", 0}
		}
		item["type"] = "custom_tool_call"
		item["input"] = input
		delete(item, "arguments")
	case "tool_search", "web_search", "image_generation", "video_generation":
		item["type"] = kind + "_call"
		args, e := decode([]byte(str(f["arguments"])))
		if e != nil {
			return nil, &Error{502, "invalid_upstream_tool_arguments", 0}
		}
		if kind == "tool_search" {
			item["arguments"] = args
			item["execution"] = "client"
		} else if kind == "web_search" {
			item["action"] = args
			delete(item, "arguments")
		}
	case "function":
	default:
		return nil, unsupportedField()
	}
	return item, nil
}

func translateTools(tools []any, from, to Wire) ([]any, error) {
	out := []any{}
	var visit func([]any, string, int) error
	seen := map[string]string{}
	visit = func(tools []any, namespace string, depth int) error {
		if depth > 8 || len(out) > 4096 {
			return &Error{400, "tool_catalog_limit", 0}
		}
		for _, v := range tools {
			t := obj(v)
			kind := str(t["type"])
			if kind == "namespace" {
				if err := visit(arr(t["tools"]), str(t["name"]), depth+1); err != nil {
					return err
				}
				continue
			}
			f := t
			if from == Chat && kind == "function" {
				f = obj(t["function"])
			}
			if from == Chat && kind == "custom" {
				f = obj(t["custom"])
			}
			if kind == "" {
				kind = "function"
			}
			if declared := str(obj(f["provider_metadata"])["tool_kind"]); declared != "" {
				kind = declared
			}
			name := str(f["name"])
			if name == "" && kind != "function" && kind != "custom" {
				name = kind
			}
			ns := namespace
			if ns == "" {
				ns = str(f["namespace"])
			}
			wireName := name
			if ns != "" && ns != "functions" {
				wireName = ns + "__" + name
			}
			fingerprint, _ := json.Marshal(object{"kind": kind, "definition": f})
			if name == "" {
				return &Error{400, "ambiguous_tool_catalog", 0}
			}
			if previous, ok := seen[wireName]; ok {
				if previous == string(fingerprint) {
					continue
				}
				return &Error{400, "ambiguous_tool_catalog", 0}
			}
			seen[wireName] = string(fingerprint)
			if kind == "tool_search" && to != Responses {
				schema := f["parameters"]
				if schema == nil {
					schema = object{"type": "object", "properties": object{"query": object{"type": "string"}, "limit": object{"type": "number"}}, "required": []any{"query"}}
				}
				fn := object{"name": wireName, "description": f["description"], "parameters": schema, "provider_metadata": object{"tool_kind": "tool_search"}}
				if to == Chat {
					out = append(out, object{"type": "function", "function": fn})
				} else {
					fn["input_schema"] = schema
					delete(fn, "parameters")
					out = append(out, fn)
				}
				continue
			}
			if kind != "function" && kind != "custom" {
				// Hosted declarations are not client functions. Keep their discriminator and options.
				switch kind {
				case "web_search", "web_search_preview", "tool_search", "image_generation", "video_generation", "file_search", "code_interpreter", "computer", "computer_use_preview", "mcp", "web_search_20250305", "web_search_20260209", "web_fetch_20250910", "bash_20250124", "text_editor_20250728":
					q := clone(t)
					if to == Anthropic && kind == "web_search" {
						q["type"] = "web_search_20250305"
						q["name"] = "web_search"
					}
					if from == Anthropic && strings.HasPrefix(kind, "web_search_") && to != Anthropic {
						q["type"] = "web_search"
					}
					out = append(out, q)
					continue
				default:
					return unsupportedField()
				}
			}
			schema := f["parameters"]
			if from == Anthropic {
				schema = f["input_schema"]
			}
			if kind == "custom" {
				schema = object{"type": "object", "properties": object{"input": object{"type": "string"}}, "required": []any{"input"}, "additionalProperties": false}
			}
			fn := clone(f)
			delete(fn, "type")
			delete(fn, "input_schema")
			delete(fn, "namespace")
			fn["name"] = wireName
			fn["parameters"] = schema
			if kind == "custom" {
				extra := clone(obj(fn["extra_content"]))
				extra["aiprouter"] = object{"custom_tool": true}
				fn["extra_content"] = extra
			}
			switch to {
			case Chat:
				out = append(out, object{"type": "function", "function": fn})
			case Responses:
				fn["type"] = kind
				fn["name"] = name
				if ns != "" && ns != "functions" {
					fn["namespace"] = ns
				}
				if kind == "custom" {
					delete(fn, "parameters")
				}
				out = append(out, fn)
			case Anthropic:
				fn["input_schema"] = schema
				delete(fn, "parameters")
				out = append(out, fn)
			}
		}
		return nil
	}
	err := visit(tools, "", 0)
	return out, err
}
func translateOptions(b, out object, from, to Wire) error {
	effort := b["reasoning_effort"]
	if effort == nil {
		effort = obj(b["output_config"])["effort"]
	}
	if effort == nil {
		effort = obj(b["reasoning"])["effort"]
	}
	if effort != nil {
		if to == Responses {
			out["reasoning"] = object{"effort": effort}
		} else if to == Chat {
			out["reasoning_effort"] = effort
		} else {
			out["thinking"] = object{"type": "adaptive"}
			out["output_config"] = object{"effort": effort}
		}
	}
	if b["thinking"] != nil {
		if to == Anthropic {
			out["thinking"] = b["thinking"]
		} else {
			out["thinking"] = b["thinking"]
		}
	}
	format := b["response_format"]
	if format == nil {
		format = obj(b["output_config"])["format"]
	}
	if format == nil {
		format = obj(b["text"])["format"]
	}
	if format != nil {
		f := clone(obj(format))
		if f["type"] == "json_schema" {
			if nested := obj(f["json_schema"]); nested != nil {
				f = clone(nested)
				f["type"] = "json_schema"
			}
		}
		switch to {
		case Responses:
			out["text"] = object{"format": f}
		case Chat:
			if f["type"] == "json_schema" {
				nested := clone(f)
				delete(nested, "type")
				out["response_format"] = object{"type": "json_schema", "json_schema": nested}
			} else {
				out["response_format"] = f
			}
		case Anthropic:
			config := clone(obj(out["output_config"]))
			config["format"] = f
			out["output_config"] = config
		}
	}
	stop := b["stop"]
	if stop == nil {
		stop = b["stop_sequences"]
	}
	if stop != nil {
		if to == Anthropic {
			if v, ok := stop.(string); ok {
				stop = []any{v}
			}
			out["stop_sequences"] = stop
		} else {
			out["stop"] = stop
		}
	}
	if to == Responses && b["include"] != nil {
		out["include"] = b["include"]
	}
	return nil
}
func parseResult(m object, w Wire) (result, error) {
	r := result{ID: str(m["id"]), Model: str(m["model"]), Stop: "stop", Usage: readUsage(m, w), Extra: object{}}
	copyFields(r.Extra, m, "extra_content", "provider_metadata")
	if u := obj(m["usage"]); u != nil {
		r.Extra["usage"] = clone(u)
	}
	switch w {
	case Responses:
		r.Items = arr(m["output"])
		if m["status"] == "incomplete" {
			r.Stop = "length"
		}
	case Chat:
		choices := arr(m["choices"])
		if len(choices) != 1 {
			return r, unsupportedField()
		}
		ch := obj(choices[0])
		r.Stop = str(ch["finish_reason"])
		msg := clone(obj(ch["message"]))
		if msg["images"] != nil {
			extra := clone(obj(msg["extra_content"]))
			extra["images"] = msg["images"]
			msg["extra_content"] = extra
		}
		items, err := messagesToResponses([]any{msg})
		if err != nil {
			return r, err
		}
		r.Items = append(items, arr(m["hosted_outputs"])...)
	case Anthropic:
		r.Stop = str(m["stop_reason"])
		msgs, err := toMessages(object{"messages": []any{object{"role": "assistant", "content": m["content"], "extra_content": m["extra_content"]}}}, Anthropic)
		if err != nil {
			return r, err
		}
		items, err := messagesToResponses(msgs)
		if err != nil {
			return r, err
		}
		r.Items = items
	default:
		return r, ErrUnsupported
	}
	for _, v := range r.Items {
		q := obj(v)
		switch str(q["type"]) {
		case "message":
			if _, err := contentToChat(q["content"], Responses); err != nil {
				return r, err
			}
			for _, v := range arr(q["content"]) {
				p := obj(v)
				if p["type"] == "output_text" {
					r.Text += str(p["text"])
				}
			}
		case "web_search_call", "image_generation_call", "video_generation_call":
		// Completed hosted output is not a client-executed function call.
		case "function_call", "custom_tool_call", "tool_search_call":
			c := responseCallToChat(q)
			f := obj(c["function"])
			r.Tools = append(r.Tools, toolCall{str(c["id"]), str(f["name"]), str(f["arguments"])})
		case "reasoning":
			if strings.HasPrefix(str(q["encrypted_content"]), reasoningPrefix) {
				if _, err := decodeReasoning(str(q["encrypted_content"])); err != nil {
					return r, err
				}
			}
		default:
			return r, unsupportedField()
		}
	}
	return r, nil
}
func (r result) items() []any {
	if r.Items != nil {
		return r.Items
	}
	items := []any{}
	if r.Text != "" {
		items = append(items, object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": r.Text, "annotations": []any{}}}})
	}
	for _, t := range r.Tools {
		items = append(items, object{"type": "function_call", "call_id": t.ID, "name": t.Name, "arguments": t.Arguments})
	}
	return items
}
func usageObject(u Usage, w Wire) object {
	out := object{}
	input, output := "input_tokens", "output_tokens"
	details := "input_tokens_details"
	if w == Chat {
		input, output, details = "prompt_tokens", "completion_tokens", "prompt_tokens_details"
	}
	if u.InputTokens != nil {
		n := *u.InputTokens
		if w == Anthropic {
			if u.CacheReadTokens != nil {
				n -= *u.CacheReadTokens
			}
			if u.CacheWriteTokens != nil {
				n -= *u.CacheWriteTokens
			}
			if n < 0 {
				n = 0
			}
		}
		out[input] = n
	}
	if u.OutputTokens != nil {
		out[output] = *u.OutputTokens
	}
	if w == Anthropic {
		if u.CacheReadTokens != nil {
			out["cache_read_input_tokens"] = *u.CacheReadTokens
		}
	} else if u.CacheReadTokens != nil {
		out[details] = object{"cached_tokens": *u.CacheReadTokens}
	}
	if u.CacheWriteTokens != nil {
		out["cache_creation_input_tokens"] = *u.CacheWriteTokens
	}
	if w == Chat && u.InputTokens != nil && u.OutputTokens != nil && *u.InputTokens <= math.MaxInt64-*u.OutputTokens {
		out["total_tokens"] = *u.InputTokens + *u.OutputTokens
	}
	return out
}
func (r result) response(w Wire) object {
	if r.ID == "" {
		r.ID = newID()
	}
	items := r.items()
	usage := usageObject(r.Usage, w)
	if source := obj(r.Extra["usage"]); source != nil {
		copyFields(usage, source, "extra_content")
		inputDetails := obj(source["input_tokens_details"])
		if inputDetails == nil {
			inputDetails = obj(source["prompt_tokens_details"])
		}
		outputDetails := obj(source["output_tokens_details"])
		if outputDetails == nil {
			outputDetails = obj(source["completion_tokens_details"])
		}
		if w != Anthropic {
			inKey, outKey := "input_tokens_details", "output_tokens_details"
			if w == Chat {
				inKey, outKey = "prompt_tokens_details", "completion_tokens_details"
			}
			if inputDetails != nil {
				usage[inKey] = clone(inputDetails)
			}
			if outputDetails != nil {
				usage[outKey] = clone(outputDetails)
			}
		} else if outputDetails != nil {
			extra := clone(obj(usage["extra_content"]))
			extra["openai"] = object{"output_tokens_details": outputDetails}
			usage["extra_content"] = extra
		}
	}
	incomplete := r.Stop == "length" || r.Stop == "max_tokens"
	switch w {
	case Responses:
		output := []any{}
		for i, v := range items {
			item := clone(obj(v))
			if item["id"] == nil {
				item["id"] = fmt.Sprintf("item_%s_%d", r.ID, i)
			}
			if item["type"] != "reasoning" {
				item["status"] = "completed"
			}
			output = append(output, item)
		}
		status := "completed"
		if incomplete {
			status = "incomplete"
		}
		out := object{"id": r.ID, "object": "response", "model": r.Model, "status": status, "output": output, "usage": usage}
		copyFields(out, r.Extra, "extra_content", "provider_metadata")
		return out
	case Anthropic:
		content := []any{}
		for _, v := range items {
			item := obj(v)
			if hostedItem(item) {
				content = append(content, hostedAnthropic(item)...)
				continue
			}
			msgs, _ := toMessages(object{"input": []any{item}}, Responses)
			messages, _, _ := messagesToAnthropic(msgs, true)
			for _, v := range messages {
				content = append(content, arr(obj(v)["content"])...)
			}
		}

		stop := "end_turn"
		if len(r.Tools) > 0 {
			stop = "tool_use"
		}
		if incomplete {
			stop = "max_tokens"
		}
		out := object{"id": r.ID, "type": "message", "role": "assistant", "model": r.Model, "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": usage}
		copyFields(out, r.Extra, "extra_content", "provider_metadata")
		return out
	default:
		ordinary, hosted := []any{}, []any{}
		for _, v := range items {
			if hostedItem(obj(v)) {
				hosted = append(hosted, v)
			} else {
				ordinary = append(ordinary, v)
			}
		}
		msgs, _ := toMessages(object{"input": ordinary}, Responses)
		msg := object{"role": "assistant", "content": ""}
		var parts, calls, details []any
		reasoning := ""
		extra := object{}
		nativeReasoning := []any{}
		for _, v := range msgs {
			m := obj(v)
			if s, ok := m["content"].(string); ok {
				parts = append(parts, object{"type": "text", "text": s})
			} else {
				parts = append(parts, arr(m["content"])...)
			}
			calls = append(calls, arr(m["tool_calls"])...)
			details = append(details, arr(m["reasoning_details"])...)
			reasoning += str(m["reasoning_content"])
			copyFields(msg, m, "reasoning_signature", "kiro_reasoning")
			mergeExtra(extra, obj(m["extra_content"]))
			if item := obj(obj(obj(m["extra_content"])["openai"])["reasoning"]); item != nil {
				nativeReasoning = append(nativeReasoning, item)
			}
		}
		if len(nativeReasoning) > 1 {
			delete(msg, "reasoning_signature")
			delete(msg, "kiro_reasoning")
			openai := clone(obj(extra["openai"]))
			delete(openai, "reasoning")
			openai["reasoning_items"] = nativeReasoning
			extra["openai"] = openai
		}
		plain := true
		text := ""
		for _, v := range parts {
			p := obj(v)
			if p["type"] == "refusal" {
				msg["refusal"] = str(msg["refusal"]) + str(p["refusal"])
				continue
			}
			if p["type"] != "text" || len(arr(p["annotations"])) > 0 || len(arr(p["citations"])) > 0 {
				plain = false
			}
			text += str(p["text"])
		}
		if plain {
			msg["content"] = text
			if text == "" && msg["refusal"] != nil {
				msg["content"] = nil
			}
		} else {
			msg["content"] = parts
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		if len(details) > 0 {
			for i, v := range details {
				d := clone(obj(v))
				d["index"] = i
				details[i] = d
			}
			msg["reasoning_details"] = details
		}
		if reasoning != "" {
			msg["reasoning_content"] = reasoning
		}
		mergeExtra(extra, obj(r.Extra["extra_content"]))
		if len(extra) > 0 {
			msg["extra_content"] = extra
		}
		if images := arr(extra["images"]); len(images) > 0 {
			msg["images"] = images
		}
		stop := "stop"
		if len(calls) > 0 {
			stop = "tool_calls"
		}
		if incomplete {
			stop = "length"
		}
		out := object{"id": r.ID, "object": "chat.completion", "model": r.Model, "choices": []any{object{"index": 0, "message": msg, "finish_reason": stop}}, "usage": usage}
		if len(hosted) > 0 {
			out["hosted_outputs"] = hosted
		}
		return out
	}
}

// mergeExtra appends ordered provider parts while retaining independently arriving metadata.
func mergeExtra(dst, src object) {
	for k, v := range src {
		if q := obj(v); q != nil {
			child := clone(obj(dst[k]))
			mergeExtra(child, q)
			dst[k] = child
		} else if a := arr(v); a != nil {
			dst[k] = append(arr(dst[k]), a...)
		} else {
			dst[k] = v
		}
	}
}
func validateTools(r result, body object, w Wire) error {
	catalog := append([]any{}, arr(body["tools"])...)
	catalog = append(catalog, arr(body["additional_tools"])...)
	for _, v := range arr(body["input"]) {
		q := obj(v)
		if q["type"] == "tool_search_output" {
			catalog = append(catalog, arr(q["tools"])...)
		}
	}
	declarations, e := translateTools(catalog, w, Responses)
	if e != nil {
		return e
	}
	names := map[string]object{}
	for _, v := range declarations {
		d := obj(v)
		name := str(d["name"])
		if name == "" {
			name = str(d["type"])
		}
		if ns := str(d["namespace"]); ns != "" {
			name = ns + "__" + name
		}
		names[name] = d
	}
	seen := map[string]bool{}
	for _, t := range r.Tools {
		if t.ID == "" || t.Name == "" || names[t.Name] == nil || seen[t.ID] {
			return &Error{502, "invalid_upstream_tool_call", 0}
		}
		seen[t.ID] = true
		args, e := decode([]byte(t.Arguments))
		if e != nil || args == nil {
			return &Error{502, "invalid_upstream_tool_arguments", 0}
		}
	}
	// Project lowered custom calls only after complete arguments have been validated.
	for _, v := range r.Items {
		item := obj(v)
		if hostedItem(item) {
			kind := strings.TrimSuffix(str(item["type"]), "_call")
			allowed := false
			for _, d := range names {
				typ := str(d["type"])
				if typ == kind || kind == "web_search" && strings.HasPrefix(typ, "web_search") {
					allowed = true
				}
			}
			if !allowed {
				return &Error{502, "undeclared_hosted_tool", 0}
			}
			continue
		}
		if item["type"] == "custom_tool_call" || item["type"] == "tool_search_call" {
			name := str(item["name"])
			if name == "" && item["type"] == "tool_search_call" {
				name = "tool_search"
			}
			if ns := str(item["namespace"]); ns != "" {
				name = ns + "__" + name
			}
			d := names[name]
			expected := "custom"
			if item["type"] == "tool_search_call" {
				expected = "tool_search"
			}
			if d["type"] != expected && !(expected == "custom" && obj(obj(d["extra_content"])["aiprouter"])["custom_tool"] == true) {
				return &Error{502, "upstream_tool_kind_mismatch", 0}
			}
			continue
		}
		if item["type"] != "function_call" {
			continue
		}
		name := str(item["name"])
		if ns := str(item["namespace"]); ns != "" {
			name = ns + "__" + name
		}
		d := names[name]
		if d["type"] == "tool_search" {
			args, e := decode([]byte(str(item["arguments"])))
			if e != nil {
				return &Error{502, "invalid_upstream_tool_arguments", 0}
			}
			item["type"] = "tool_search_call"
			item["arguments"] = args
			item["execution"] = "client"
			continue
		}
		if d["type"] == "custom" || obj(obj(d["extra_content"])["aiprouter"])["custom_tool"] == true {
			args, e := decode([]byte(str(item["arguments"])))
			if e != nil {
				return &Error{502, "invalid_custom_tool_arguments", 0}
			}
			input, ok := args["input"].(string)
			if !ok {
				return &Error{502, "invalid_custom_tool_arguments", 0}
			}
			item["type"] = "custom_tool_call"
			item["input"] = input
			delete(item, "arguments")
			if ns := str(d["namespace"]); ns != "" {
				item["namespace"] = ns
				item["name"] = d["name"]
			}
		}
	}
	return nil
}

// TranslateRequestJSON translates one bounded generation request. This codec does
// not authorize signed replay, execute tools or send requests. The central handler
// must admit the translated history using its original owner and account.
func TranslateRequestJSON(body json.RawMessage, from, to Wire) (json.RawMessage, error) {
	if len(body) > 32<<20 {
		return nil, &Error{400, "translation_buffer_limit", 0}
	}
	m, e := decode(body)
	if e != nil {
		return nil, e
	}
	out, e := translateRequest(m, from, to)
	if e != nil {
		return nil, e
	}
	return json.Marshal(out)
}

// TranslateResponseJSON preserves typed reasoning and tools in one completed JSON
// response. Provider tool kinds must come from the admitted request catalog. The
// caller must validate client tools and materialize hosted artifacts separately.
func TranslateResponseJSON(body json.RawMessage, from, to Wire) (json.RawMessage, error) {
	if len(body) > 32<<20 {
		return nil, &Error{502, "translation_buffer_limit", 0}
	}
	m, e := decode(body)
	if e != nil {
		return nil, e
	}
	if from == to {
		return json.Marshal(m)
	}
	r, e := parseResult(m, from)
	if e != nil {
		return nil, e
	}
	return json.Marshal(r.response(to))
}

func hostedItem(item object) bool {
	switch str(item["type"]) {
	case "web_search_call", "image_generation_call", "video_generation_call":
		return true
	}
	return false
}
func hostedAnthropic(item object) []any {
	// Messages has no video-generation result cell. Keep the canonical hosted item
	// in metadata and expose artifact links as text; never invent a client tool call.
	extra := object{"aiprouter": object{"hosted_output": clone(item)}}
	if item["type"] == "web_search_call" {
		return []any{object{"type": "web_search_tool_result", "tool_use_id": item["id"], "content": item["sources"], "extra_content": extra}}
	}
	links := []string{}
	for _, v := range arr(item["artifacts"]) {
		if url := str(obj(v)["url"]); url != "" {
			links = append(links, url)
		}
	}
	if result := obj(item["result"]); result["url"] != nil {
		links = append(links, str(result["url"]))
	}
	return []any{object{"type": "text", "text": strings.Join(links, "\n"), "extra_content": extra}}
}

// TranslateToolsJSON normalizes a tool catalog even when both wires are Chat.
// It leaves unrelated same-wire request extensions under the adapter's ownership.
func TranslateToolsJSON(raw json.RawMessage, from, to Wire) (json.RawMessage, error) {
	if len(raw) > 2<<20 {
		return nil, &Error{400, "tool_catalog_limit", 0}
	}
	var value any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e := d.Decode(&value); e != nil {
		return nil, &Error{400, "invalid_tool_catalog", 0}
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, &Error{400, "invalid_tool_catalog", 0}
	}
	list, ok := value.([]any)
	if !ok {
		return nil, &Error{400, "invalid_tool_catalog", 0}
	}
	out, e := translateTools(list, from, to)
	if e != nil {
		return nil, e
	}
	return json.Marshal(out)
}
