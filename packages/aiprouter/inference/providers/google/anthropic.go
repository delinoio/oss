// SPDX-License-Identifier: Apache-2.0
package google

import "encoding/json"

// Antigravity's direct Messages lane still sends GenerateContent upstream. This
// projection avoids a lossy intermediate protocol at the client-facing boundary.
func anthropicUsage(u object) object {
	out := object{}
	for from, to := range map[string]string{"prompt_tokens": "input_tokens", "completion_tokens": "output_tokens"} {
		if v, ok := u[from]; ok {
			out[to] = v
		}
	}
	if v, ok := obj(u["prompt_tokens_details"])["cached_tokens"]; ok {
		out["cache_read_input_tokens"] = v
		if total, ok := u["prompt_tokens"].(int64); ok {
			if cached, ok := v.(int64); ok && cached <= total {
				out["input_tokens"] = total - cached
			}
		}
	}
	google := object{}
	if v, ok := obj(u["completion_tokens_details"])["reasoning_tokens"]; ok {
		google["thoughts_token_count"] = v
	}
	if v, ok := u["total_tokens"]; ok {
		google["total_token_count"] = v
	}
	if len(google) > 0 {
		out["extra_content"] = object{"google": google}
	}
	return out
}
func anthropicStop(s *responseState) string {
	switch s.finish() {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}
func anthropicBlocks(message object) []any {
	var blocks []any
	if reasoning := text(message["reasoning_content"]); reasoning != "" {
		block := object{"type": "thinking", "thinking": reasoning}
		parts := array(obj(obj(message["extra_content"])["google"])["parts"])
		if len(parts) == 1 && realSignature(signature(obj(parts[0]))) {
			block["signature"] = signature(obj(parts[0]))
		}
		blocks = append(blocks, block)
	}
	if s := text(message["content"]); s != "" {
		blocks = append(blocks, object{"type": "text", "text": s})
	}
	for _, raw := range array(message["tool_calls"]) {
		call := obj(raw)
		fn := obj(call["function"])
		args, _ := decodeJSON([]byte(text(fn["arguments"])))
		b := object{"type": "tool_use", "id": call["id"], "name": fn["name"], "input": args}
		if extra := call["extra_content"]; extra != nil {
			b["extra_content"] = extra
		}
		blocks = append(blocks, b)
	}
	return blocks
}
func anthropicResponse(message object, s *responseState) object {
	var blocks []any
	for _, raw := range s.blocks {
		b := obj(raw)
		if b["type"] == "thinking_signature" {
			if len(blocks) > 0 && obj(blocks[len(blocks)-1])["type"] == "thinking" {
				obj(blocks[len(blocks)-1])["signature"] = b["signature"]
			}
			continue
		}
		blocks = append(blocks, raw)
	}
	if blocks == nil {
		blocks = []any{}
	}
	out := object{"id": s.id, "type": "message", "role": "assistant", "model": s.model, "content": blocks, "stop_reason": anthropicStop(s), "stop_sequence": nil, "usage": anthropicUsage(s.usage)}
	if extra := message["extra_content"]; extra != nil {
		out["extra_content"] = extra
	}
	return out
}
func namedEvent(event string, v object) []byte {
	v["type"] = event
	b, _ := json.Marshal(v)
	return []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
}
func (r *streamReader) anthropicStart() []byte {
	if r.anthropicStarted {
		return nil
	}
	r.anthropicStarted = true
	return namedEvent("message_start", object{"message": object{"id": r.state.id, "type": "message", "role": "assistant", "model": r.state.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": anthropicUsage(r.state.usage)}})
}
func (r *streamReader) anthropicDelta(delta object) []byte {
	out := r.anthropicStart()
	for _, raw := range r.state.blocks {
		block := obj(raw)
		if block["type"] == "thinking_signature" {
			if r.thinkingOpen {
				out = append(out, namedEvent("content_block_delta", object{"index": r.thinkingIndex, "delta": object{"type": "signature_delta", "signature": block["signature"]}})...)
			}
			continue
		}
		if block["type"] == "thinking" {
			if !r.thinkingOpen {
				r.thinkingIndex = r.blockIndex
				r.blockIndex++
				r.thinkingOpen = true
				out = append(out, namedEvent("content_block_start", object{"index": r.thinkingIndex, "content_block": object{"type": "thinking", "thinking": ""}})...)
			}
			out = append(out, namedEvent("content_block_delta", object{"index": r.thinkingIndex, "delta": object{"type": "thinking_delta", "thinking": block["thinking"]}})...)
			if block["signature"] != nil {
				out = append(out, namedEvent("content_block_delta", object{"index": r.thinkingIndex, "delta": object{"type": "signature_delta", "signature": block["signature"]}})...)
			}
			continue
		}
		out = append(out, r.closeThinking()...)
		index := r.blockIndex
		r.blockIndex++
		switch block["type"] {
		case "tool_use":
			args, _ := json.Marshal(block["input"])
			block["input"] = object{}
			out = append(out, namedEvent("content_block_start", object{"index": index, "content_block": block})...)
			out = append(out, namedEvent("content_block_delta", object{"index": index, "delta": object{"type": "input_json_delta", "partial_json": string(args)}})...)
		default:
			out = append(out, namedEvent("content_block_start", object{"index": index, "content_block": object{"type": "text", "text": ""}})...)
			out = append(out, namedEvent("content_block_delta", object{"index": index, "delta": object{"type": "text_delta", "text": block["text"]}})...)
		}
		out = append(out, namedEvent("content_block_stop", object{"index": index})...)
	}
	if extra := delta["extra_content"]; extra != nil {
		out = append(out, namedEvent("message_delta", object{"delta": object{}, "extra_content": extra})...)
	}
	return out
}

func (r *streamReader) closeThinking() []byte {
	if !r.thinkingOpen {
		return nil
	}
	r.thinkingOpen = false
	return namedEvent("content_block_stop", object{"index": r.thinkingIndex})
}
