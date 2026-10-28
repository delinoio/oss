// SPDX-License-Identifier: Apache-2.0
// Kiro wire behavior adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func inputMessages(body object, wire inference.Wire) ([]object, string, error) {
	var messages []object
	system := ""
	switch wire {
	case inference.Chat:
		for _, raw := range arr(body["messages"]) {
			if obj(raw) == nil {
				return nil, "", failure(400, "kiro_invalid_message")
			}
			messages = append(messages, obj(raw))
		}
	case inference.Responses:
		system = str(body["instructions"])
		if text, ok := body["input"].(string); ok {
			messages = append(messages, object{"role": "user", "content": text})
		} else {
			for _, raw := range arr(body["input"]) {
				m := obj(raw)
				switch str(m["type"]) {
				case "", "message":
					messages = append(messages, m)
				case "function_call":
					messages = append(messages, object{"role": "assistant", "tool_calls": []any{object{"id": m["call_id"], "function": object{"name": wireName(str(m["namespace"]), str(m["name"])), "arguments": m["arguments"]}}}})
				case "custom_tool_call":
					name := str(m["name"])
					requestedName := name
					namespace := str(m["namespace"])
					if namespace == "functions" {
						namespace = ""
					}
					declared, err := normalizeTools(body, wire)
					if err != nil {
						return nil, "", err
					}
					matches := 0
					for _, tool := range declared {
						if tool.definition.Kind == CustomTool && tool.definition.Name == requestedName && (namespace == "" || tool.definition.Namespace == namespace) {
							name = tool.definition.WireName
							matches++
						}
					}
					if matches != 1 {
						return nil, "", failure(400, "kiro_ambiguous_custom_history")
					}
					value, ok := m["input"].(string)
					if !ok {
						return nil, "", failure(400, "kiro_invalid_custom_input")
					}
					args, _ := json.Marshal(object{"input": value})
					messages = append(messages, object{"role": "assistant", "tool_calls": []any{object{"id": m["call_id"], "function": object{"name": name, "arguments": string(args)}}}})
				case "custom_tool_call_output":
					messages = append(messages, object{"role": "tool", "tool_call_id": m["call_id"], "content": m["output"]})
				case "additional_tools":
					// Tool declarations are incorporated by normalizeTools.
				case "tool_search_call":
					declared, err := normalizeTools(body, wire)
					if err != nil {
						return nil, "", err
					}
					name := "tool_search"
					for _, tool := range declared {
						if tool.definition.Kind == SearchTool {
							name = tool.definition.WireName
							break
						}
					}
					args := m["arguments"]
					if _, ok := args.(string); !ok {
						b, _ := json.Marshal(args)
						args = string(b)
					}
					messages = append(messages, object{"role": "assistant", "tool_calls": []any{object{"id": m["call_id"], "function": object{"name": name, "arguments": args}}}})
				case "tool_search_output":
					if m["call_id"] != nil {
						b, _ := json.Marshal(m["tools"])
						messages = append(messages, object{"role": "tool", "tool_call_id": m["call_id"], "content": string(b)})
					}
				case "function_call_output":
					messages = append(messages, object{"role": "tool", "tool_call_id": m["call_id"], "content": m["output"]})
				default:
					return nil, "", failure(400, "kiro_input_item_unsupported")
				}
			}
		}
	case inference.Anthropic:
		var err error
		system, _, err = content(body["system"])
		if err != nil {
			return nil, "", err
		}
		for _, raw := range arr(body["messages"]) {
			m := obj(raw)
			role := str(m["role"])
			if _, ok := m["content"].(string); ok {
				messages = append(messages, m)
				continue
			}
			current := object{"role": role}
			parts := []any{}
			calls := []any{}
			flush := func() {
				if len(parts) > 0 || len(calls) > 0 {
					current["content"], current["tool_calls"] = parts, calls
					messages = append(messages, current)
					current = object{"role": role}
					parts, calls = nil, nil
				}
			}
			for _, rawPart := range arr(m["content"]) {
				p := obj(rawPart)
				switch str(p["type"]) {
				case "tool_use":
					if role != "assistant" {
						return nil, "", failure(400, "kiro_invalid_tool_role")
					}
					encoded, err := json.Marshal(p["input"])
					if err != nil {
						return nil, "", err
					}
					calls = append(calls, object{"id": p["id"], "function": object{"name": p["name"], "arguments": string(encoded)}})
				case "tool_result":
					if role != "user" {
						return nil, "", failure(400, "kiro_invalid_tool_role")
					}
					flush()
					messages = append(messages, object{"role": "tool", "tool_call_id": p["tool_use_id"], "content": p["content"], "is_error": p["is_error"]})
				case "thinking", "redacted_thinking":
					return nil, "", failure(400, "kiro_foreign_reasoning_replay_unsupported")
				default:
					parts = append(parts, p)
				}
			}
			flush()
		}
	default:
		return nil, "", inference.ErrUnsupported
	}
	if len(messages) == 0 {
		return nil, "", failure(400, "kiro_messages_required")
	}
	return messages, system, nil
}

func content(v any) (string, []any, error) {
	if v == nil {
		return "", nil, nil
	}
	if s, ok := v.(string); ok {
		return s, nil, nil
	}
	parts, ok := v.([]any)
	if !ok {
		return "", nil, failure(400, "kiro_invalid_content")
	}
	var text string
	var images []any
	remoteImages, malformedImages := 0, 0
	for _, raw := range parts {
		p := obj(raw)
		switch str(p["type"]) {
		case "text", "input_text", "output_text":
			s, ok := p["text"].(string)
			if !ok {
				return "", nil, failure(400, "kiro_invalid_text")
			}
			text = joinText(text, s)
		case "image_url", "input_image", "image":
			img, marker := inlineImage(p)
			if img != nil {
				images = append(images, img)
			}
			if marker != "" {
				if strings.Contains(marker, "remote image references") {
					remoteImages++
				} else {
					malformedImages++
				}
			}

		default:
			return "", nil, failure(400, "kiro_content_unsupported")
		}
	}
	if remoteImages == 1 {
		text = joinText(text, "[image omitted: remote image references are not supported by this provider]")
	} else if remoteImages > 1 {
		text = joinText(text, fmt.Sprintf("[%d images omitted: remote image references are not supported by this provider]", remoteImages))
	}
	if malformedImages == 1 {
		text = joinText(text, "[image omitted: malformed inline image data URL]")
	} else if malformedImages > 1 {
		text = joinText(text, fmt.Sprintf("[%d images omitted: malformed inline image data URLs]", malformedImages))
	}
	return text, images, nil
}

var rejectedSchemaKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields("additionalProperties pattern format minLength maxLength minimum maximum exclusiveMinimum exclusiveMaximum multipleOf minItems maxItems uniqueItems minProperties maxProperties contentEncoding contentMediaType $schema patternProperties propertyNames dependentSchemas dependentRequired if then else contains unevaluatedProperties unevaluatedItems encrypted") {
		m[k] = true
	}
	return m
}()

func sanitizeSchema(in object, depth int) object {
	if depth > 64 {
		return nil
	}
	out := object{}
	for key, value := range in {
		if rejectedSchemaKeys[key] {
			continue
		}
		if key == "required" && len(arr(value)) == 0 {
			continue
		}
		if key == "properties" || key == "$defs" || key == "definitions" {
			children := object{}
			for name, child := range obj(value) {
				c := sanitizeSchema(obj(child), depth+1)
				if c == nil {
					return nil
				}
				children[name] = c
			}
			out[key] = children
			continue
		}
		if m, ok := value.(map[string]any); ok {
			child := sanitizeSchema(m, depth+1)
			if child == nil {
				return nil
			}
			value = child
		}
		if a, ok := value.([]any); ok {
			copy := make([]any, len(a))
			for i, v := range a {
				if m, ok := v.(map[string]any); ok {
					child := sanitizeSchema(m, depth+1)
					if child == nil {
						return nil
					}
					copy[i] = child
				} else {
					copy[i] = v
				}
			}
			value = copy
		}
		out[key] = value
	}
	return out
}

func flattenSchema(schema object) {
	schema["type"] = "object"
	properties := obj(schema["properties"])
	if properties == nil {
		properties = object{}
	}
	required := arr(schema["required"])
	seen := map[string]bool{}
	for _, r := range required {
		seen[str(r)] = true
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		for _, v := range arr(schema[key]) {
			m := obj(v)
			for k, p := range obj(m["properties"]) {
				properties[k] = p
			}
			if key == "allOf" {
				for _, r := range arr(m["required"]) {
					if s := str(r); s != "" && !seen[s] {
						required = append(required, s)
						seen[s] = true
					}
				}
			}
		}
		delete(schema, key)
	}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
}
