// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

func googleRequest(b object, wire inference.Wire) (object, error) {
	if wire != inference.Chat {
		var err error
		b, err = translate(b, wire, inference.Chat)
		if err != nil {
			return nil, err
		}
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "tools": true, "tool_choice": true, "temperature": true, "top_p": true, "max_tokens": true, "max_completion_tokens": true, "stream_options": true}
	for _, k := range strings.Fields("reasoning reasoning_effort thinking response_format text stop stop_sequences metadata user parallel_tool_calls") {
		allowed[k] = true
	}
	for k := range b {
		if !allowed[k] {
			return nil, requestError("unsupported_zed_google_field")
		}
	}
	messages, err := canonicalMessages(b, inference.Chat)
	if err != nil {
		return nil, err
	}
	contents, system := []any{}, []any{}
	names := map[string]string{}
	for _, v := range messages {
		m := objectOf(v)
		role := stringOf(m["role"])
		parts, err := googleContent(m["content"])
		if err != nil {
			return nil, err
		}
		if role == "system" || role == "developer" {
			system = append(system, parts...)
			continue
		}
		if role == "assistant" {
			role = "model"
		}
		fallbackSignature := ""
		for _, v := range arrayOf(objectOf(objectOf(m["extra_content"])["google"])["parts"]) {
			p := objectOf(v)
			if p["thought"] == true {
				if sig := thoughtSignature(p); realSignature(sig) {
					fallbackSignature = sig
				}
			}
		}
		if role == "tool" {
			name := names[stringOf(m["tool_call_id"])]
			if name == "" {
				return nil, requestError("unknown_zed_tool_result")
			}
			var response any
			decoded, decodeErr := decodeObject([]byte(stringOf(m["content"])))
			if decodeErr != nil {
				response = object{"result": m["content"]}
			} else {
				response = decoded
			}
			if objectOf(response) == nil {
				response = object{"result": response}
			}
			role = "user"
			parts = []any{object{"functionResponse": object{"id": m["tool_call_id"], "name": name, "response": response}}}
		}
		for _, v := range arrayOf(m["tool_calls"]) {
			c := objectOf(v)
			f := objectOf(c["function"])
			if c["type"] == "custom" {
				custom := objectOf(c["custom"])
				f = object{"name": custom["name"], "arguments": jsonString(object{"input": custom["input"]})}
			}
			args, err := decodeObject([]byte(stringOf(f["arguments"])))
			if err != nil {
				return nil, requestError("invalid_tool_arguments")
			}
			names[stringOf(c["id"])] = stringOf(f["name"])
			part := object{"functionCall": object{"id": c["id"], "name": f["name"], "args": args}}
			sig := thoughtSignature(c)
			if sig == "" {
				sig = fallbackSignature
			}
			if realSignature(sig) {
				part["thoughtSignature"] = sig
			}
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			contents = append(contents, object{"role": role, "parts": parts})
		}
	}
	out := object{"contents": contents}
	if len(system) > 0 {
		out["systemInstruction"] = object{"parts": system}
	}
	config := googleGeneration(b)
	for from, to := range map[string]string{"temperature": "temperature", "top_p": "topP", "max_tokens": "maxOutputTokens", "max_completion_tokens": "maxOutputTokens"} {
		if v, ok := b[from]; ok {
			config[to] = v
		}
	}
	if max, ok := b["max_completion_tokens"]; ok {
		config["maxOutputTokens"] = max
	}
	if len(config) > 0 {
		out["generationConfig"] = config
	}
	if tools, ok := b["tools"].([]any); ok {
		decls := []any{}
		strict := false
		for _, v := range tools {
			t := objectOf(v)
			f := objectOf(t["function"])
			if t["type"] == "custom" {
				f = customFunction(t)
			}
			if (t["type"] != "function" && t["type"] != "custom") || f == nil {
				return nil, inference.ErrUnsupported
			}
			strict = strict || f["strict"] == true
			decls = append(decls, object{"name": f["name"], "description": f["description"], "parameters": f["parameters"]})
		}
		out["tools"] = []any{object{"functionDeclarations": decls}}
		if strict {
			out["toolConfig"] = object{"functionCallingConfig": object{"mode": "VALIDATED"}}
		}
	}
	if choice, ok := b["tool_choice"]; ok {
		mode := "AUTO"
		c := object{"mode": mode}
		switch choice {
		case "auto":
			if out["toolConfig"] != nil {
				c = objectOf(objectOf(out["toolConfig"])["functionCallingConfig"])
			}
		case "none":
			c["mode"] = "NONE"
		case "required":
			c["mode"] = "ANY"
		default:
			name := stringOf(objectOf(objectOf(choice)["function"])["name"])
			if name == "" {
				return nil, inference.ErrUnsupported
			}
			c["mode"] = "ANY"
			c["allowedFunctionNames"] = []any{name}
		}
		out["toolConfig"] = object{"functionCallingConfig": c}
	}
	// Zed's Google parser does not accept safetySettings.
	return out, nil
}

type googleStream struct {
	calls        int
	signature    string
	showThinking bool
}

func (g *googleStream) convert(m object) (object, error) {
	if objectOf(m["promptFeedback"])["blockReason"] != nil {
		return nil, inference.ErrUnsupported
	}
	cs := arrayOf(m["candidates"])
	if len(cs) > 1 {
		return nil, inference.ErrUnsupported
	}
	delta := object{}
	choices := []any{}
	if len(cs) == 1 {
		c := objectOf(cs[0])
		calls := []any{}
		opaque := []any{}
		text, thinking := "", ""
		for _, v := range arrayOf(objectOf(c["content"])["parts"]) {
			p := objectOf(v)
			if p == nil {
				return nil, inference.ErrUnsupported
			}
			for key := range p {
				switch key {
				case "text", "thought", "functionCall", "thoughtSignature", "thought_signature", "extra_content", "inlineData", "inline_data":
				default:
					return nil, inference.ErrUnsupported
				}
			}
			sig := thoughtSignature(p)
			if len(sig) > 64<<10 {
				return nil, inference.ErrUnsupported
			}
			if p["thought"] == true {
				if realSignature(sig) {
					g.signature = sig
				}
				opaque = append(opaque, p)
				if g.showThinking {
					thinking += stringOf(p["text"])
				}
			} else {
				text += stringOf(p["text"])
				if sig != "" {
					opaque = append(opaque, p)
				}
			}
			im := objectOf(p["inlineData"])
			if im == nil {
				im = objectOf(p["inline_data"])
			}
			if im != nil {
				mime := stringOf(im["mimeType"])
				if mime == "" {
					mime = stringOf(im["mime_type"])
				}
				delta["images"] = append(arrayOf(delta["images"]), object{"type": "image_url", "image_url": object{"url": "data:" + mime + ";base64," + stringOf(im["data"])}})
			}
			if f := objectOf(p["functionCall"]); f != nil {
				if f["args"] == nil {
					f["args"] = object{}
				}
				if stringOf(f["name"]) == "" || objectOf(f["args"]) == nil {
					return nil, inference.ErrUnsupported
				}
				if g.calls >= 256 {
					return nil, inference.ErrUnsupported
				}
				args, err := json.Marshal(f["args"])
				if err != nil {
					return nil, err
				}
				id := stringOf(f["id"])
				if id == "" {
					generated, err := randomID()
					if err != nil {
						return nil, err
					}
					id = "call_" + generated
				}
				call := object{"index": g.calls, "id": id, "type": "function", "function": object{"name": f["name"], "arguments": string(args)}}
				if sig == "" {
					sig = g.signature
				}
				if realSignature(sig) {
					call["extra_content"] = object{"google": object{"thought_signature": sig}}
				}
				calls = append(calls, call)
				g.calls++
			}
		}
		if text != "" {
			delta["content"] = text
		}
		if thinking != "" {
			delta["reasoning_content"] = thinking
		}
		if len(calls) > 0 {
			delta["tool_calls"] = calls
		}
		if len(opaque) > 0 {
			delta["extra_content"] = object{"google": object{"parts": opaque}}
		}
		var finish any
		if reason := stringOf(c["finishReason"]); reason != "" {
			switch reason {
			case "STOP":
				finish = "stop"
				if g.calls > 0 {
					finish = "tool_calls"
				}
			case "MAX_TOKENS":
				finish = "length"
			case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
				finish = "content_filter"
			default:
				return nil, inference.ErrUnsupported
			}
		}
		choices = append(choices, object{"index": 0, "delta": delta, "finish_reason": finish})
	}
	out := object{"object": "chat.completion.chunk", "choices": choices}
	if m["responseId"] != nil {
		out["id"] = m["responseId"]
	}
	if m["modelVersion"] != nil {
		out["model"] = m["modelVersion"]
	}
	if u := objectOf(m["usageMetadata"]); u != nil {
		usage := object{}
		for from, to := range map[string]string{"promptTokenCount": "prompt_tokens", "candidatesTokenCount": "completion_tokens", "totalTokenCount": "total_tokens"} {
			if n, ok := validCount(u[from]); ok {
				usage[to] = n
			}
		}
		if n, ok := validCount(u["cachedContentTokenCount"]); ok {
			usage["prompt_tokens_details"] = object{"cached_tokens": n}
		}
		if n, ok := validCount(u["thoughtsTokenCount"]); ok {
			usage["completion_tokens_details"] = object{"reasoning_tokens": n}
			if output, ok := validCount(u["candidatesTokenCount"]); ok && output <= (1<<63-1)-n {
				usage["completion_tokens"] = output + n
			}
		}
		out["usage"] = usage
	}
	return out, nil
}
func validCount(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil && i >= 0
}
