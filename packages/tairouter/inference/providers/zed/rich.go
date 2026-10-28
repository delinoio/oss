// SPDX-License-Identifier: Apache-2.0
// Content and reasoning-envelope behavior adapted from the pinned delegates.
package zed

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// Canonical content uses Chat text/image_url/file parts. Native Anthropic blocks
// travel separately so a signed or server-produced block is never reconstructed.
func richContent(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	parts, ok := v.([]any)
	if !ok {
		return nil, requestError("invalid_zed_content")
	}
	out := []any{}
	for _, v := range parts {
		p := objectOf(v)
		switch p["type"] {
		case "text", "input_text", "output_text":
			q := object{"type": "text", "text": p["text"]}
			for _, k := range []string{"cache_control", "citations", "annotations"} {
				if p[k] != nil {
					q[k] = p[k]
				}
			}
			out = append(out, q)
		case "image_url":
			out = append(out, p)
		case "input_image":
			out = append(out, object{"type": "image_url", "image_url": object{"url": p["image_url"], "detail": p["detail"]}})
		case "image":
			s := objectOf(p["source"])
			u := stringOf(s["url"])
			if s["type"] == "base64" {
				u = "data:" + stringOf(s["media_type"]) + ";base64," + stringOf(s["data"])
			}
			out = append(out, object{"type": "image_url", "image_url": object{"url": u}})
		case "document":
			out = append(out, object{"type": "file", "file": object{"anthropic": p}})
		case "file", "input_file":
			out = append(out, p)
		default:
			return nil, inference.ErrUnsupported
		}
	}
	return out, nil
}

func contentFor(v any, wire inference.Wire, role string) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		if wire != inference.Anthropic {
			return s, nil
		}
		return []any{object{"type": "text", "text": s}}, nil
	}
	out := []any{}
	for _, v := range arrayOf(v) {
		p := objectOf(v)
		switch p["type"] {
		case "text":
			q := object{}
			for k, v := range p {
				q[k] = v
			}
			if wire == inference.Responses {
				q["type"] = "input_text"
				if role == "assistant" {
					q["type"] = "output_text"
				}
				delete(q, "cache_control")
				delete(q, "citations")
			}
			out = append(out, q)
		case "image_url":
			im := objectOf(p["image_url"])
			u := stringOf(im["url"])
			if wire == inference.Responses {
				out = append(out, object{"type": "input_image", "image_url": u, "detail": im["detail"]})
				continue
			}
			if wire == inference.Chat {
				out = append(out, p)
				continue
			}
			source := object{"type": "url", "url": u}
			if strings.HasPrefix(u, "data:") {
				head, data, ok := strings.Cut(u, ",")
				if !ok || !strings.HasSuffix(head, ";base64") {
					return nil, requestError("invalid_image_data")
				}
				source = object{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": data}
			}
			out = append(out, object{"type": "image", "source": source})
		case "file", "input_file":
			f := objectOf(p["file"])
			if f == nil {
				f = p
			}
			if native := objectOf(f["anthropic"]); native != nil {
				if wire == inference.Anthropic {
					out = append(out, native)
					continue
				}
				s := objectOf(native["source"])
				if s["type"] == "base64" {
					f = object{"file_data": "data:" + stringOf(s["media_type"]) + ";base64," + stringOf(s["data"]), "filename": native["title"]}
				} else if s["type"] == "url" {
					f = object{"file_url": s["url"]}
				} else {
					return nil, inference.ErrUnsupported
				}
			}
			if wire == inference.Chat {
				out = append(out, object{"type": "file", "file": f})
				continue
			}
			if wire == inference.Responses {
				q := object{"type": "input_file"}
				for k, v := range f {
					q[k] = v
				}
				out = append(out, q)
				continue
			}
			source := object{}
			if u := stringOf(f["file_url"]); u != "" {
				source = object{"type": "url", "url": u}
			} else {
				head, data, ok := strings.Cut(stringOf(f["file_data"]), ",")
				if !ok || !strings.HasSuffix(head, ";base64") {
					return nil, requestError("invalid_document_data")
				}
				source = object{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": data}
			}
			out = append(out, object{"type": "document", "source": source})
		default:
			return nil, inference.ErrUnsupported
		}
	}
	return out, nil
}

var signatureShape = regexp.MustCompile(`^[A-Za-z0-9+/_=-]+$`)
var foreignSignature = regexp.MustCompile(`(?i)^(fc|ctc|call|msg|rs|resp|reasoning|item|ws|tool|func|function)[-_]`)

func realSignature(s string) bool {
	return len(s) >= 16 && len(s) <= 64<<10 && s != "skip_thought_signature_validator" && !foreignSignature.MatchString(s) && signatureShape.MatchString(s)
}
func thoughtSignature(p object) string {
	for _, v := range []any{p["thoughtSignature"], p["thought_signature"], objectOf(objectOf(p["extra_content"])["google"])["thought_signature"], objectOf(objectOf(p["providerMetadata"])["google"])["thoughtSignature"]} {
		if s := stringOf(v); s != "" {
			return s
		}
	}
	return ""
}
func reasoningBlocks(m object) []any {
	out := []any{}
	for _, v := range arrayOf(objectOf(objectOf(m["extra_content"])["anthropic"])["content"]) {
		out = append(out, v)
	}
	for _, v := range arrayOf(m["reasoning_details"]) {
		p := objectOf(v)
		if p["type"] == "reasoning.encrypted" && p["data"] != nil {
			out = append(out, object{"type": "redacted_thinking", "data": p["data"]})
			continue
		}
		sig := stringOf(p["signature"])
		if realSignature(sig) {
			out = append(out, object{"type": "thinking", "thinking": p["text"], "signature": sig})
		}
	}
	return out
}
func envelopeMessage(item object) (object, error) {
	text := ""
	for _, v := range arrayOf(item["summary"]) {
		text += stringOf(objectOf(v)["text"])
	}
	m := object{"role": "assistant", "reasoning_content": text}
	enc := stringOf(item["encrypted_content"])
	if strings.HasPrefix(enc, "ocxr1:") {
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, "ocxr1:"))
		if err != nil {
			return nil, requestError("invalid_reasoning_envelope")
		}
		e, err := decodeObject(data)
		if err != nil {
			return nil, requestError("invalid_reasoning_envelope")
		}
		blocks := []any{}
		for _, v := range arrayOf(e["red"]) {
			blocks = append(blocks, object{"type": "redacted_thinking", "data": v})
		}
		if e["txt"] != nil {
			text = stringOf(e["txt"])
		}
		if sig := stringOf(e["sig"]); realSignature(sig) {
			blocks = append(blocks, object{"type": "thinking", "thinking": text, "signature": sig})
		}
		m["extra_content"] = object{"anthropic": object{"content": blocks}}
	} else {
		m["extra_content"] = object{"openai": object{"reasoning": item}}
	}
	return m, nil
}
func responseReasoning(m object) []any {
	if item := objectOf(objectOf(objectOf(m["extra_content"])["openai"])["reasoning"]); item != nil {
		return []any{item}
	}
	blocks := reasoningBlocks(m)
	summary := stringOf(m["reasoning_content"])
	if len(blocks) == 0 && summary == "" {
		return nil
	}
	e := object{}
	red := []any{}
	for _, v := range blocks {
		p := objectOf(v)
		if p["type"] == "redacted_thinking" {
			red = append(red, p["data"])
		}
		if p["type"] == "thinking" {
			e["sig"] = p["signature"]
			e["txt"] = p["thinking"]
		}
	}
	if len(red) > 0 {
		e["red"] = red
	}
	item := object{"type": "reasoning", "summary": []any{object{"type": "summary_text", "text": summary}}}
	if len(e) > 0 {
		b, _ := json.Marshal(e)
		item["encrypted_content"] = "ocxr1:" + base64.StdEncoding.EncodeToString(b)
	}
	return []any{item}
}

func customFunction(t object) object {
	return object{"name": t["name"], "description": t["description"], "parameters": object{"type": "object", "properties": object{"input": object{"type": "string", "description": "Raw freeform input for this tool."}}, "required": []any{"input"}}}
}

func mergeAssistantMessages(input []any) []any {
	out := []any{}
	for _, v := range input {
		m := objectOf(v)
		if m["role"] != "assistant" || len(out) == 0 || objectOf(out[len(out)-1])["role"] != "assistant" {
			out = append(out, m)
			continue
		}
		prev := objectOf(out[len(out)-1])
		parts := func(v any) []any {
			if s, ok := v.(string); ok {
				return []any{object{"type": "text", "text": s}}
			}
			return arrayOf(v)
		}
		if m["content"] != nil {
			prev["content"] = append(parts(prev["content"]), parts(m["content"])...)
		}
		if m["tool_calls"] != nil {
			prev["tool_calls"] = append(arrayOf(prev["tool_calls"]), arrayOf(m["tool_calls"])...)
		}
		if m["reasoning_content"] != nil {
			prev["reasoning_content"] = stringOf(prev["reasoning_content"]) + stringOf(m["reasoning_content"])
		}
		if m["reasoning_details"] != nil {
			prev["reasoning_details"] = append(arrayOf(prev["reasoning_details"]), arrayOf(m["reasoning_details"])...)
		}
		if extra := objectOf(m["extra_content"]); extra != nil {
			old := objectOf(prev["extra_content"])
			if old == nil {
				old = object{}
				prev["extra_content"] = old
			}
			for k, v := range extra {
				if k == "anthropic" && old[k] != nil {
					p := objectOf(old[k])
					p["content"] = append(arrayOf(p["content"]), arrayOf(objectOf(v)["content"])...)
				} else {
					old[k] = v
				}
			}
		}
	}
	return out
}
