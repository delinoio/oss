// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// readSSE bounds each event independently; total stream length is not a retained buffer.
func readSSE(r io.Reader, emit func(string, []byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Split(splitSSELine)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var data []byte
	event := ""
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		payload := data[:len(data)-1]
		e := emit(event, payload)
		data = nil
		event = ""
		return e
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if e := flush(); e != nil {
				return e
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, val, _ := strings.Cut(line, ":")
		val = strings.TrimPrefix(val, " ")
		switch key {
		case "event":
			event = val
		case "data":
			if len(data)+len(val)+1 > 2<<20 {
				return &Error{502, "stream_event_too_large", 0}
			}
			data = append(data, val...)
			data = append(data, '\n')
		}
	}
	if e := scanner.Err(); e != nil {
		return e
	}
	return flush()
}
func emitSSE(w http.ResponseWriter, event string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if event != "" {
		if _, e = fmt.Fprintf(w, "event: %s\n", event); e != nil {
			return e
		}
	}
	if _, e = fmt.Fprintf(w, "data: %s\n\n", b); e != nil {
		return e
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
func mergeUsage(a *Usage, b Usage) {
	if b.InputTokens != nil {
		a.InputTokens = b.InputTokens
	}
	if b.OutputTokens != nil {
		a.OutputTokens = b.OutputTokens
	}
	if b.CacheReadTokens != nil {
		a.CacheReadTokens = b.CacheReadTokens
	}
	if b.CacheWrite1hTokens != nil {
		a.CacheWrite1hTokens = b.CacheWrite1hTokens
	}
	if b.CacheWriteTokens != nil {
		a.CacheWriteTokens = b.CacheWriteTokens
	}
}

type idleReader struct {
	io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (r idleReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	if n > 0 && r.timer != nil {
		r.timer.Reset(r.idle)
	}
	return n, e
}
func (h *Handler) deliverStream(ctx context.Context, w http.ResponseWriter, p Principal, in Wire, op Operation, b object, t Target, resp *http.Response, rec *UsageRecord, cancel context.CancelFunc, stall time.Duration) error {
	reasoningCapture := h.NewReasoningStreamCapture(ctx)
	captureFinished := false
	defer func() {
		if !captureFinished {
			_ = reasoningCapture.Finish(ctx, false)
		}
	}()
	finishReasoningCapture := func() error {
		captureFinished = true
		return replayError(reasoningCapture.Finish(ctx, true))
	}
	stopOnCancel := context.AfterFunc(ctx, func() { resp.Body.Close() })
	defer stopOnCancel()
	streaming := b["stream"] == true
	native := in == t.Wire && !forceMessagesTranslation(in, t)
	var timer *time.Timer
	if stall > 0 {
		timer = time.AfterFunc(stall, func() { cancel(); resp.Body.Close() })
		defer timer.Stop()
	}
	if !streaming {
		deadline := time.AfterFunc(15*time.Minute, func() { cancel(); resp.Body.Close() })
		defer deadline.Stop()
	}
	var terminal bool
	var final object
	nativeTerminalEvent := ""
	var nativeTerminalData []byte
	nativeDone := false
	var r = result{ID: newID(), Model: rec.Model, Stop: "stop"}
	acc := newTranslationStream(t.Wire)
	receipts := newStreamReceipts(t.Wire)
	size := 0
	emitter := &streamEmitter{w: w, wire: in, id: r.ID, model: r.Model}
	if streaming {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
	}
	err := readSSE(idleReader{resp.Body, timer, stall}, func(event string, data []byte) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if string(data) == "[DONE]" {
			if t.Wire == Chat {
				terminal = true
			}
			if native && streaming {
				nativeDone = true
			}

			return nil
		}
		m, e := decode(data)
		if e != nil {
			return &Error{502, "invalid_upstream_event", 0}
		}

		mergeUsage(&rec.Usage, readUsage(m, t.Wire))

		if response := obj(m["response"]); response != nil {
			mergeUsage(&rec.Usage, readUsage(response, t.Wire))
		}

		// Observe the unmodified provider event before projection or native forwarding.
		if t.Wire == Chat && m["choices"] != nil {
			if e := reasoningCapture.Observe(ctx, m); e != nil {
				return replayError(e)
			}
		}
		oldEventType := str(m["type"])
		m, e = h.RewriteProviderResponse(ctx, m)
		if e != nil {
			return e
		}
		if event == oldEventType && oldEventType != "" {
			event = str(m["type"])
		}
		data, e = json.Marshal(m)
		if e != nil {
			return e
		}
		if tier := str(m["service_tier"]); tier != "" {
			rec.ServiceTier = tier
		}
		if tier := str(obj(m["response"])["service_tier"]); tier != "" {
			rec.ServiceTier = tier
		}
		if id := str(m["id"]); id != "" {
			rec.ResponseID = id
		}
		if id := str(obj(m["response"])["id"]); id != "" {
			rec.ResponseID = id
		}
		if id := str(obj(m["message"])["id"]); id != "" {
			rec.ResponseID = id
		}
		if str(m["type"]) == "error" || m["error"] != nil {
			return &Error{502, "upstream_stream_error", 0}
		}
		if t.Wire == Responses && (str(m["type"]) == "response.completed" || str(m["type"]) == "response.incomplete") {
			final = obj(m["response"])
			terminal = true
			mergeUsage(&rec.Usage, readUsage(final, Responses))
		}
		if t.Wire == Responses && str(m["type"]) == "response.failed" {
			return &Error{502, "upstream_stream_error", 0}
		}
		if t.Wire == Anthropic {
			switch str(m["type"]) {
			case "message_start":
				mergeUsage(&rec.Usage, readUsage(obj(m["message"]), Anthropic))
			case "message_stop":
				terminal = true
			}
		}
		if native && streaming {
			if e := receipts.consume(m); e != nil {
				return e
			}
			typ := str(m["type"])
			if typ == "message_stop" || typ == "response.completed" || typ == "response.incomplete" {
				nativeTerminalEvent = event
				nativeTerminalData = append([]byte{}, data...)
				return nil
			}

			if _, e := io.WriteString(w, "event: "+safeEventName(event)+"\ndata: "+strings.ReplaceAll(string(data), "\n", "\ndata: ")+"\n\n"); e != nil {
				return e
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return nil
		}
		// Accumulation is bounded for translated output and buffered native responses.
		size += len(data)
		if size > 32<<20 {
			return &Error{502, "translation_buffer_limit", 0}
		}
		if e := acc.consume(m); e != nil {
			return e
		}
		if streaming {
			items, ready, e := acc.progressItems()
			if e != nil {
				return e
			}
			return emitter.progress(items, ready, b, in)
		}
		return nil

	})
	if err != nil {
		return err
	}
	if !terminal {
		return &Error{502, "upstream_stream_incomplete", 0}
	}
	if native && streaming {
		if e := finishReasoningCapture(); e != nil {
			return e
		}
		if final != nil {
			if e := h.CaptureReplayReceipts(ctx, final); e != nil {
				return e
			}
		} else {
			values, e := receipts.values()
			if e != nil {
				return e
			}
			if e = h.CaptureReplayReceipts(ctx, values); e != nil {
				return e
			}
		}
		if in == Responses && final != nil {
			if e := h.save(ctx, p, b, final, rec); e != nil {
				h.event(ctx, "continuation_write_failed")
			}
		}
		if nativeTerminalData != nil {
			if _, e := io.WriteString(w, "event: "+safeEventName(nativeTerminalEvent)+"\ndata: "+strings.ReplaceAll(string(nativeTerminalData), "\n", "\ndata: ")+"\n\n"); e != nil {
				return e
			}
		}
		if nativeDone {
			if _, e := io.WriteString(w, "data: [DONE]\n\n"); e != nil {
				return e
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		return nil
	}
	if final != nil {
		r, err = parseResult(final, t.Wire)
		if err != nil {
			return err
		}
	} else {
		r, err = acc.result()
		if err != nil {
			return err
		}
	}
	if r.ID == "" {
		r.ID = emitter.id
	}
	if r.Model == "" {
		r.Model = rec.Model
	}
	if streaming {
		r.ID = emitter.id
	}
	rec.ResponseID = r.ID
	r.Usage = rec.Usage
	if err = validateTools(r, b, in); err != nil {
		return err
	}
	if e := finishReasoningCapture(); e != nil {
		return e
	}
	out := r.response(in)
	if e := h.CaptureReplayReceipts(ctx, out); e != nil {
		return e
	}
	if in == Responses {
		if e := h.save(ctx, p, b, out, rec); e != nil {
			h.event(ctx, "continuation_write_failed")
		}
	}
	if streaming {
		return emitter.finish(r)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}
func safeEventName(s string) string {
	for _, c := range s {
		if !(c == '.' || c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "message"
		}
	}
	if s == "" {
		return "message"
	}
	return s
}
func index(v any) int { n, _ := strconv.Atoi(fmt.Sprint(v)); return n }

type streamEmitter struct {
	w                    http.ResponseWriter
	wire                 Wire
	id, model            string
	started, textStarted bool
	seq                  int
	progressState        map[int]*emittedItem
}

func (s *streamEmitter) event(name string, m object) error {
	if s.wire == Responses {
		m["type"] = name
		m["sequence_number"] = s.seq
		s.seq++
	}
	return emitSSE(s.w, name, m)
}
func (s *streamEmitter) start() error {
	if s.started {
		return nil
	}
	s.started = true
	switch s.wire {
	case Responses:
		return s.event("response.created", object{"response": object{"id": s.id, "object": "response", "model": s.model, "status": "in_progress", "output": []any{}}})
	case Anthropic:
		return s.event("message_start", object{"type": "message_start", "message": object{"id": s.id, "type": "message", "role": "assistant", "model": s.model, "content": []any{}, "stop_reason": nil, "usage": object{}}})
	default:
		return s.chat(object{"role": "assistant"}, nil)
	}
}
func (s *streamEmitter) chat(delta object, finish any) error {
	return emitSSE(s.w, "", object{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{object{"index": 0, "delta": delta, "finish_reason": finish}}})
}
func (s *streamEmitter) text(text string) error {
	if e := s.start(); e != nil {
		return e
	}
	if !s.textStarted {
		s.textStarted = true
		switch s.wire {
		case Responses:
			if e := s.event("response.output_item.added", object{"output_index": 0, "item": object{"id": "msg_" + s.id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}); e != nil {
				return e
			}
			if e := s.event("response.content_part.added", object{"output_index": 0, "item_id": "msg_" + s.id, "content_index": 0, "part": object{"type": "output_text", "text": "", "annotations": []any{}}}); e != nil {
				return e
			}
		case Anthropic:
			if e := s.event("content_block_start", object{"type": "content_block_start", "index": 0, "content_block": object{"type": "text", "text": ""}}); e != nil {
				return e
			}
		}
	}
	switch s.wire {
	case Responses:
		return s.event("response.output_text.delta", object{"output_index": 0, "item_id": "msg_" + s.id, "content_index": 0, "delta": text})
	case Anthropic:
		return s.event("content_block_delta", object{"type": "content_block_delta", "index": 0, "delta": object{"type": "text_delta", "text": text}})
	default:
		return s.chat(object{"content": text}, nil)
	}
}
func (s *streamEmitter) finish(r result) error {
	if e := s.start(); e != nil {
		return e
	}
	r.ID = s.id
	out := r.response(s.wire)
	if e := s.verifyProgress(out); e != nil {
		return e
	}
	switch s.wire {
	case Chat:
		msg := obj(obj(arr(out["choices"])[0])["message"])
		for i, v := range arr(msg["tool_calls"]) {
			obj(v)["index"] = i
		}
		if s.progressState != nil {
			for i, v := range r.items() {
				item := obj(v)
				if hostedItem(item) {
					continue
				}
				state := s.progressState[i]
				single := result{ID: r.ID, Model: r.Model, Items: []any{item}}.response(Chat)
				part := clone(obj(obj(arr(single["choices"])[0])["message"]))
				if state != nil && state.started {
					delete(part, "content")
					if item["type"] == "reasoning" {
						delete(part, "reasoning_content")
					}
					if calls := arr(part["tool_calls"]); len(calls) > 0 {
						for _, v := range calls {
							call := obj(v)
							call["index"] = i
							call["function"] = object{}
						}
					}
				} else if calls := arr(part["tool_calls"]); len(calls) > 0 {
					for _, v := range calls {
						obj(v)["index"] = i
					}
				}
				delete(part, "role")
				if len(part) > 0 {
					if e := s.chat(part, nil); e != nil {
						return e
					}
				}
			}
			msg = object{}
		}
		if e := s.chat(msg, nil); e != nil {
			return e
		}
		if outputs := arr(out["hosted_outputs"]); len(outputs) > 0 {
			if e := emitSSE(s.w, "", object{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{}, "hosted_outputs": outputs}); e != nil {
				return e
			}
		}
		if e := s.chat(object{}, obj(arr(out["choices"])[0])["finish_reason"]); e != nil {
			return e
		}
		if e := emitSSE(s.w, "", object{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{}, "usage": out["usage"]}); e != nil {
			return e
		}
		_, e := io.WriteString(s.w, "data: [DONE]\n\n")
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
		return e
	case Anthropic:
		for i, v := range arr(out["content"]) {
			block := obj(v)
			state := s.progressState[i]
			if state != nil && state.closed {
				continue
			}
			start := clone(block)
			var deltas []object
			switch str(block["type"]) {
			case "thinking":
				start["thinking"] = ""
				delete(start, "signature")
				if text := str(block["thinking"]); text != "" {
					deltas = append(deltas, object{"type": "thinking_delta", "thinking": text})
				}
				if sig := str(block["signature"]); sig != "" {
					deltas = append(deltas, object{"type": "signature_delta", "signature": sig})
				}
			case "text":
				start["text"] = ""
				deltas = append(deltas, object{"type": "text_delta", "text": block["text"]})
			case "tool_use", "server_tool_use":
				start["input"] = object{}
				b, _ := json.Marshal(block["input"])
				deltas = append(deltas, object{"type": "input_json_delta", "partial_json": string(b)})
			}
			if state == nil || !state.started {
				if e := s.event("content_block_start", object{"type": "content_block_start", "index": i, "content_block": start}); e != nil {
					return e
				}
			} else {
				for _, d := range deltas {
					key := "text"
					if d["type"] == "input_json_delta" {
						key = "partial_json"
						for _, v := range r.items() {
							item := obj(v)
							if item["call_id"] == block["id"] {
								if raw, ok := item["arguments"].(string); ok {
									d[key] = raw
								}
								break
							}
						}
					}
					if value, ok := d[key].(string); ok {
						if len(value) < state.bytes || !strings.HasPrefix(value, state.sent) {
							return &Error{502, "upstream_stream_rewritten", 0}
						}
						d[key] = value[state.bytes:]
					}
				}
			}
			for _, d := range deltas {
				if e := s.event("content_block_delta", object{"type": "content_block_delta", "index": i, "delta": d}); e != nil {
					return e
				}
			}
			if e := s.event("content_block_stop", object{"type": "content_block_stop", "index": i}); e != nil {
				return e
			}
		}
		if e := s.event("message_delta", object{"type": "message_delta", "delta": object{"stop_reason": out["stop_reason"], "stop_sequence": nil}, "usage": out["usage"]}); e != nil {
			return e
		}
		return s.event("message_stop", object{"type": "message_stop"})
	case Responses:
		for i, v := range arr(out["output"]) {
			item := obj(v)
			state := s.progressState[i]
			if state != nil && state.started {
				item = clone(item)
				item["id"] = state.id
				arr(out["output"])[i] = item
			}
			if state != nil && state.closed {
				continue
			}
			start := clone(item)
			start["status"] = "in_progress"
			switch item["type"] {
			case "message":
				start["content"] = []any{}
			case "reasoning":
				start["summary"] = []any{}
				delete(start, "encrypted_content")
			case "function_call":
				start["arguments"] = ""
			case "custom_tool_call":
				start["input"] = ""
			}
			if state == nil || !state.started {
				if e := s.event("response.output_item.added", object{"output_index": i, "item": start}); e != nil {
					return e
				}
			}
			switch item["type"] {
			case "message":
				for j, v := range arr(item["content"]) {
					part := obj(v)
					empty := clone(part)
					if part["type"] == "output_text" {
						empty["text"] = ""
					}
					base := object{"output_index": i, "item_id": item["id"], "content_index": j, "part": empty}
					if state == nil || !state.started || j > 0 {
						if e := s.event("response.content_part.added", base); e != nil {
							return e
						}
					}
					if part["type"] == "output_text" {
						if e := s.event("response.output_text.delta", object{"output_index": i, "item_id": item["id"], "content_index": j, "delta": remainingProgress(state, j, str(part["text"]))}); e != nil {
							return e
						}
						if e := s.event("response.output_text.done", object{"output_index": i, "item_id": item["id"], "content_index": j, "text": part["text"]}); e != nil {
							return e
						}
					}
					base = clone(base)
					base["part"] = part
					if e := s.event("response.content_part.done", base); e != nil {
						return e
					}
				}
			case "reasoning":
				for j, v := range arr(item["summary"]) {
					part := obj(v)
					base := object{"output_index": i, "item_id": item["id"], "summary_index": j, "part": object{"type": "summary_text", "text": ""}}
					if state == nil || !state.partStarted || j > 0 {
						if e := s.event("response.reasoning_summary_part.added", base); e != nil {
							return e
						}
					}
					if e := s.event("response.reasoning_summary_text.delta", object{"output_index": i, "item_id": item["id"], "summary_index": j, "delta": remainingProgress(state, j, str(part["text"]))}); e != nil {
						return e
					}
					if e := s.event("response.reasoning_summary_text.done", object{"output_index": i, "item_id": item["id"], "summary_index": j, "text": part["text"]}); e != nil {
						return e
					}
					base = clone(base)
					base["part"] = part
					if e := s.event("response.reasoning_summary_part.done", base); e != nil {
						return e
					}
				}
			case "function_call", "custom_tool_call":
				field, event := "arguments", "response.function_call_arguments"
				if item["type"] == "custom_tool_call" {
					field, event = "input", "response.custom_tool_call_input"
				}
				if e := s.event(event+".delta", object{"output_index": i, "item_id": item["id"], "delta": remainingProgress(state, 0, str(item[field]))}); e != nil {
					return e
				}
				if e := s.event(event+".done", object{"output_index": i, "item_id": item["id"], field: item[field]}); e != nil {
					return e
				}
			}
			if e := s.event("response.output_item.done", object{"output_index": i, "item": item}); e != nil {
				return e
			}
		}
		event := "response.completed"
		if out["status"] == "incomplete" {
			event = "response.incomplete"
		}
		return s.event(event, object{"response": out})
	}
	return ErrUnsupported
}

func splitSSELine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			n := i + 1
			if n < len(data) && data[n] == '\n' {
				n++
			}
			return n, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// translationStream retains at most the caller's 32 MiB budget. Thinking blocks
// remain mutable until termination because several providers deliver signatures
// after visible text. Emission then closes each block only after its signature.
type translationStream struct {
	wire                   Wire
	id, model, stop        string
	items                  []any
	indexed                map[int]object
	chatTools              map[int]object
	chatThinking, chatText object
	extra                  object
	closed                 map[int]bool
	order                  []int
}

func newTranslationStream(w Wire) *translationStream {
	return &translationStream{wire: w, indexed: map[int]object{}, chatTools: map[int]object{}, extra: object{}, closed: map[int]bool{}}
}
func (s *translationStream) chatMessage(reasoning bool) object {
	slot := s.chatText
	if reasoning {
		slot = s.chatThinking
	}
	if slot != nil {
		return slot
	}
	slot = object{"role": "assistant"}
	s.items = append(s.items, slot)
	if reasoning {
		s.chatThinking = slot
		s.chatText = nil
	} else {
		s.chatText = slot
	}
	return slot
}
func appendString(m object, key string, v any) {
	if text, ok := v.(string); ok {
		m[key] = str(m[key]) + text
	}
}
func (s *translationStream) consume(m object) error {
	for _, key := range []string{"index", "output_index", "content_index", "summary_index"} {
		if v, ok := m[key]; ok {
			n := index(v)
			if n < 0 || n > 4095 {
				return &Error{502, "stream_index_limit", 0}
			}
		}
	}

	if id := str(m["id"]); id != "" {
		s.id = id
	}
	if model := str(m["model"]); model != "" {
		s.model = model
	}
	if outputs := arr(m["hosted_outputs"]); len(outputs) > 0 {
		s.extra["hosted_outputs"] = mergeHostedOutputs(arr(s.extra["hosted_outputs"]), outputs)
	}
	if u := obj(m["usage"]); u != nil {
		usage := clone(obj(s.extra["usage"]))
		mergeExtra(usage, u)
		s.extra["usage"] = usage
	}
	switch s.wire {
	case Chat:
		for _, v := range arr(m["choices"]) {
			c := obj(v)
			if index(c["index"]) != 0 {
				return unsupportedField()
			}
			if stop := str(c["finish_reason"]); stop != "" {
				s.stop = stop
			}
			d := obj(c["delta"])
			if d["reasoning_content"] != nil || d["reasoning_signature"] != nil || d["kiro_reasoning"] != nil || d["reasoning_details"] != nil || obj(obj(d["extra_content"])["google"])["parts"] != nil {
				msg := s.chatMessage(true)
				appendString(msg, "reasoning_content", d["reasoning_content"])
				appendString(msg, "reasoning_signature", d["reasoning_signature"])
				if d["kiro_reasoning"] != nil {
					msg["kiro_reasoning"] = d["kiro_reasoning"]
				}
				for _, v := range arr(d["reasoning_details"]) {
					q := obj(v)
					details := arr(msg["reasoning_details"])
					idx := index(q["index"])
					if q["index"] == nil {
						idx = len(details)
					}
					if idx < 0 || idx > 4095 {
						return &Error{502, "stream_index_limit", 0}
					}
					for len(details) <= idx {
						details = append(details, object{})
					}
					part := obj(details[idx])
					copyFields(part, q, "type", "format", "index")
					appendString(part, "text", q["text"])
					appendString(part, "signature", q["signature"])
					appendString(part, "data", q["data"])
					msg["reasoning_details"] = details
				}
				extra := clone(obj(msg["extra_content"]))
				mergeExtra(extra, obj(d["extra_content"]))
				msg["extra_content"] = extra
			} else if d["extra_content"] != nil {
				msg := s.chatMessage(false)
				extra := clone(obj(msg["extra_content"]))
				mergeExtra(extra, obj(d["extra_content"]))
				msg["extra_content"] = extra
			}
			if d["content"] != nil {
				msg := s.chatMessage(false)
				if text, ok := d["content"].(string); ok {
					appendString(msg, "content", text)
				} else {
					msg["content"] = append(arr(msg["content"]), arr(d["content"])...)
				}
			}
			if d["images"] != nil {
				msg := s.chatMessage(false)
				extra := clone(obj(msg["extra_content"]))
				extra["images"] = append(arr(extra["images"]), arr(d["images"])...)
				msg["extra_content"] = extra
			}
			for _, v := range arr(d["tool_calls"]) {
				call := obj(v)
				i := index(call["index"])
				if i < 0 || i > 4095 {
					return &Error{502, "stream_index_limit", 0}
				}
				tc := s.chatTools[i]
				if tc == nil {
					tc = object{"type": "function", "function": object{}}
					s.chatTools[i] = tc
					s.items = append(s.items, object{"role": "assistant", "tool_calls": []any{tc}})
					s.chatText = nil
				}
				copyFields(tc, call, "id", "type", "provider_metadata")
				if call["extra_content"] != nil {
					extra := clone(obj(tc["extra_content"]))
					mergeExtra(extra, obj(call["extra_content"]))
					tc["extra_content"] = extra
				}
				f := obj(tc["function"])
				delta := obj(call["function"])
				appendString(f, "name", delta["name"])
				appendString(f, "arguments", delta["arguments"])
				copyFields(f, delta, "extra_content")
			}
		}
	case Anthropic:
		i := index(m["index"])
		switch str(m["type"]) {
		case "message_start":
			msg := obj(m["message"])
			s.id = str(msg["id"])
			s.model = str(msg["model"])
			if u := obj(msg["usage"]); u != nil {
				s.extra["usage"] = clone(u)
			}
			copyFields(s.extra, msg, "extra_content")
		case "content_block_start":
			if s.indexed[i] != nil {
				return &Error{502, "duplicate_content_block", 0}
			}
			block := clone(obj(m["content_block"]))
			s.indexed[i] = block
			s.items = append(s.items, block)
			s.order = append(s.order, i)
		case "content_block_delta":
			block := s.indexed[i]
			if block == nil {
				return &Error{502, "invalid_content_stream", 0}
			}
			d := obj(m["delta"])
			switch str(d["type"]) {
			case "text_delta":
				appendString(block, "text", d["text"])
			case "thinking_delta":
				appendString(block, "thinking", d["thinking"])
			case "signature_delta":
				appendString(block, "signature", d["signature"])
			case "input_json_delta":
				appendString(block, "_arguments", d["partial_json"])
			case "citations_delta":
				block["citations"] = append(arr(block["citations"]), d["citation"])
			default:
				return unsupportedField()
			}
		case "content_block_stop":
			s.closed[i] = true
		case "message_delta":
			s.stop = str(obj(m["delta"])["stop_reason"])
		}
	case Responses:
		i := index(m["output_index"])
		switch str(m["type"]) {
		case "response.created", "response.in_progress":
			r := obj(m["response"])
			s.id = str(r["id"])
			s.model = str(r["model"])
		case "response.output_item.added":
			if s.indexed[i] != nil {
				return &Error{502, "duplicate_output_item", 0}
			}
			item := clone(obj(m["item"]))
			s.indexed[i] = item
			s.items = append(s.items, item)
			s.order = append(s.order, i)
		case "response.output_item.done":
			s.closed[i] = true
			item := s.indexed[i]
			if item == nil {
				return &Error{502, "invalid_output_stream", 0}
			}
			for k := range item {
				delete(item, k)
			}
			for k, v := range obj(m["item"]) {
				item[k] = v
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			item := s.indexed[i]
			if item == nil {
				return &Error{502, "invalid_tool_stream", 0}
			}
			key := "arguments"
			if m["type"] == "response.custom_tool_call_input.delta" {
				key = "input"
			}
			appendString(item, key, m["delta"])
		case "response.content_part.added", "response.reasoning_summary_part.added":
			item := s.indexed[i]
			if item == nil {
				return &Error{502, "invalid_output_stream", 0}
			}
			key := "content"
			idx := index(m["content_index"])
			if m["type"] == "response.reasoning_summary_part.added" {
				key = "summary"
				idx = index(m["summary_index"])
			}
			parts := arr(item[key])
			for len(parts) <= idx {
				parts = append(parts, object{})
			}
			parts[idx] = clone(obj(m["part"]))
			item[key] = parts
		case "response.output_text.delta", "response.reasoning_summary_text.delta":
			item := s.indexed[i]
			if item == nil {
				item = object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": ""}}}
				if m["type"] == "response.reasoning_summary_text.delta" {
					item = object{"type": "reasoning", "summary": []any{object{"type": "summary_text", "text": ""}}}
				}
				s.indexed[i] = item
				s.items = append(s.items, item)
			}
			key := "content"
			idx := index(m["content_index"])
			if m["type"] == "response.reasoning_summary_text.delta" {
				key = "summary"
				idx = index(m["summary_index"])
			}
			parts := arr(item[key])
			if idx < 0 || idx >= len(parts) {
				return &Error{502, "invalid_output_stream", 0}
			}
			appendString(obj(parts[idx]), "text", m["delta"])
		}
	}
	return nil
}
func (s *translationStream) result() (result, error) {
	m := object{"id": s.id, "model": s.model, "usage": s.extra["usage"]}
	copyFields(m, s.extra, "extra_content")
	switch s.wire {
	case Chat:
		items, e := messagesToResponses(s.items)
		if e != nil {
			return result{}, e
		}
		m["output"] = append(items, arr(s.extra["hosted_outputs"])...)
		m["status"] = "completed"
		r, e := parseResult(m, Responses)
		r.Stop = s.stop
		return r, e
	case Anthropic:
		for _, v := range s.items {
			block := obj(v)
			if raw, ok := block["_arguments"].(string); ok {
				args, e := decode([]byte(raw))
				if e != nil {
					return result{}, &Error{502, "invalid_upstream_tool_arguments", 0}
				}
				block["input"] = args
				delete(block, "_arguments")
			}
		}
		m["content"] = s.items
		m["stop_reason"] = s.stop
	case Responses:
		m["output"] = s.items
		m["status"] = "completed"
	}
	return parseResult(m, s.wire)
}

// Native forwarding retains only signed reasoning and tool identity, bounded to
// 2 MiB. Ordinary visible text remains an unbounded forwarded stream. Exact signed
// text and canonical tool arguments are needed by the replay receipt verifier.
type streamReceipts struct {
	wire Wire
	acc  *translationStream
	size int
}

func newStreamReceipts(w Wire) *streamReceipts {
	return &streamReceipts{wire: w, acc: newTranslationStream(w)}
}
func (s *streamReceipts) consume(m object) error {
	filtered := object{}
	switch s.wire {
	case Chat:
		choices := []any{}
		for _, v := range arr(m["choices"]) {
			c := obj(v)
			d := obj(c["delta"])
			delta := object{}
			copyFields(delta, d, "reasoning_content", "reasoning_signature", "kiro_reasoning", "reasoning_details", "tool_calls")
			if google := obj(obj(d["extra_content"])["google"]); google["parts"] != nil {
				delta["extra_content"] = object{"google": object{"parts": google["parts"]}}
			}
			if len(delta) > 0 {
				choices = append(choices, object{"index": c["index"], "delta": delta})
			}
		}
		if len(choices) == 0 {
			return nil
		}
		filtered["choices"] = choices
	case Anthropic:
		typ := str(m["type"])
		i := index(m["index"])
		if typ == "content_block_start" {
			block := obj(m["content_block"])
			switch block["type"] {
			case "thinking", "redacted_thinking", "tool_use", "server_tool_use":
				filtered = m
			default:
				return nil
			}
		} else if typ == "content_block_delta" && s.acc.indexed[i] != nil {
			filtered = m
		} else {
			return nil
		}
	default:
		return nil
	}
	b, e := json.Marshal(filtered)
	if e != nil {
		return e
	}
	s.size += len(b)
	if s.size > 2<<20 {
		return &Error{502, "reasoning_receipt_limit", 0}
	}
	return s.acc.consume(filtered)
}
func (s *streamReceipts) values() ([]any, error) {
	if s.wire == Anthropic {
		for _, v := range s.acc.items {
			block := obj(v)
			if raw, ok := block["_arguments"].(string); ok {
				args, e := decode([]byte(raw))
				if e != nil {
					return nil, &Error{502, "invalid_upstream_tool_arguments", 0}
				}
				block["input"] = args
				delete(block, "_arguments")
			}
		}
	}
	return s.acc.items, nil
}

// AggregateResponse owns and closes the response body. It returns Chat JSON for a
// completed standard-wire response. On failure it returns only confirmed usage
// alongside the error; callers must not treat partial content as a completed turn.
// It performs no sends, replay authorization, persistence or tool execution.
func AggregateResponse(ctx context.Context, resp *http.Response, wire Wire) (json.RawMessage, error) {
	return aggregateResponse(ctx, resp, wire, 32<<20)
}
func aggregateResponse(ctx context.Context, resp *http.Response, wire Wire, maxBytes int64) (json.RawMessage, error) {
	if resp == nil || resp.Body == nil {
		return nil, &Error{502, "missing_upstream_body", 0}
	}
	defer resp.Body.Close()
	stop := context.AfterFunc(ctx, func() { resp.Body.Close() })
	defer stop()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{resp.StatusCode, "upstream_refused", 0}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, e := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if e != nil {
			return nil, e
		}
		if int64(len(b)) > maxBytes {
			return nil, &Error{502, "translation_buffer_limit", 0}
		}
		return TranslateResponseJSON(b, wire, Chat)
	}
	acc := newTranslationStream(wire)
	usage := Usage{}
	var final object
	terminal := false
	size := 0
	e := readSSE(resp.Body, func(_ string, b []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		size += len(b)
		if int64(size) > maxBytes {
			return &Error{502, "translation_buffer_limit", 0}
		}
		if string(b) == "[DONE]" {
			if wire == Chat {
				terminal = true
			}
			return nil
		}
		m, e := decode(b)
		if e != nil {
			return &Error{502, "invalid_upstream_event", 0}
		}
		mergeUsage(&usage, readUsage(m, wire))
		if q := obj(m["response"]); q != nil {
			mergeUsage(&usage, readUsage(q, wire))
		}
		if q := obj(m["message"]); q != nil {
			mergeUsage(&usage, readUsage(q, wire))
		}
		typ := str(m["type"])
		if typ == "error" || typ == "response.failed" || m["error"] != nil {
			return &Error{502, "upstream_stream_error", 0}
		}
		if wire == Responses && (typ == "response.completed" || typ == "response.incomplete") {
			final = obj(m["response"])
			terminal = true
		}
		if wire == Anthropic && typ == "message_stop" {
			terminal = true
		}
		return acc.consume(m)
	})
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	if e == nil && !terminal {
		e = &Error{502, "upstream_stream_incomplete", 0}
	}
	if e != nil {
		b, _ := json.Marshal(object{"usage": usageObject(usage, Chat)})
		return b, e
	}
	var r result
	if final != nil {
		r, e = parseResult(final, wire)
	} else {
		r, e = acc.result()
	}
	if e != nil {
		return nil, e
	}
	r.Usage = usage
	return json.Marshal(r.response(Chat))
}

// HostedCodec satisfies hosted wrappers through structural typing, without a
// dependency from the core inference package to child provider adapters.
type HostedCodec struct{}

func (HostedCodec) Request(body json.RawMessage, from, to Wire) (json.RawMessage, error) {
	return TranslateRequestJSON(body, from, to)
}
func (HostedCodec) Collect(ctx context.Context, resp *http.Response, wire Wire, maxBytes int64) (json.RawMessage, error) {
	if maxBytes <= 0 || maxBytes > 32<<20 {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, &Error{400, "invalid_translation_limit", 0}
	}
	return aggregateResponse(ctx, resp, wire, maxBytes)
}

// emittedItem tracks only the prefix already published for a block. It never
// marks a turn successful; terminal output remains behind validation and receipts.
type emittedItem struct {
	started, closed bool
	partStarted     bool
	id, sent        string
	status          string
	kind            string
	source          int
	bytes           int
}

func remainingProgress(state *emittedItem, part int, value string) string {
	if state == nil || part != 0 {
		return value
	}
	if len(value) >= state.bytes && strings.HasPrefix(value, state.sent) {
		return value[state.bytes:]
	}
	return ""
}
func (s *translationStream) progressItems() ([]any, map[int]bool, error) {
	out := []any{}
	ready := map[int]bool{}
	for i, v := range s.items {
		q := obj(v)
		var items []any
		switch s.wire {
		case Chat:
			var err error
			items, err = messagesToResponses([]any{q})
			if err != nil {
				// Freeform custom input is a JSON string inside an argument object. Do not
				// publish its escaped partial spelling as decoded custom input.
				if len(arr(q["tool_calls"])) > 0 {
					if e := pendingBlockBudget(s.items[i:]); e != nil {
						return nil, nil, e
					}
					return out, ready, nil
				}
				return nil, nil, err
			}
		case Anthropic:
			switch q["type"] {
			case "text":
				items = []any{object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": q["text"]}}}}
			case "thinking", "redacted_thinking":
				items = []any{anthropicReasoning(q)}
			case "tool_use", "server_tool_use":
				args := str(q["_arguments"])
				if args == "" {
					b, _ := json.Marshal(q["input"])
					if string(b) != "{}" {
						args = string(b)
					}
				}
				item := object{"type": "function_call", "call_id": q["id"], "name": q["name"], "arguments": args}
				copyFields(item, q, "extra_content", "provider_metadata")
				items = []any{item}
			default:
				msgs, err := toMessages(object{"messages": []any{object{"role": "assistant", "content": []any{q}}}}, Anthropic)
				if err != nil {
					return nil, nil, err
				}
				items, err = messagesToResponses(msgs)
				if err != nil {
					return nil, nil, err
				}
			}
		case Responses:
			items = []any{q}
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			idx := len(out)
			out = append(out, item)
			ready[idx] = obj(item)["type"] == "message" || s.wire == Chat && s.stop != ""
			if s.wire != Chat {
				source := i
				if i < len(s.order) {
					source = s.order[i]
				}
				if s.closed[source] {
					ready[idx] = true
				}
			}
		}
	}
	for _, v := range arr(s.extra["hosted_outputs"]) {
		ready[len(out)] = true
		out = append(out, v)
	}
	return out, ready, nil
}
func (s *streamEmitter) progress(items []any, ready map[int]bool, request object, in Wire) error {
	if s.progressState == nil {
		s.progressState = map[int]*emittedItem{}
	}
	// Catalog identity is available before argument JSON completes. Custom/hosted
	// calls are withheld until their projection is complete, not guessed as functions.
	declarations, err := translateTools(arr(request["tools"]), in, Responses)
	if err != nil {
		return err
	}
	special := map[string]bool{}
	for _, v := range declarations {
		d := obj(v)
		name := str(d["name"])
		if ns := str(d["namespace"]); ns != "" {
			name = ns + "__" + name
		}
		if d["type"] != "function" || obj(obj(d["extra_content"])["aiprouter"])["custom_tool"] == true {
			special[name] = true
		}
	}
	blockIndex := 0
	for i, v := range items {
		item := obj(v)
		kind := str(item["type"])
		if hostedItem(item) && s.wire != Anthropic {
			state := s.progressState[i]
			if state == nil {
				state = &emittedItem{id: str(item["id"])}
				s.progressState[i] = state
			}
			status := str(item["status"])
			if status == state.status && state.started {
				continue
			}
			if err := s.start(); err != nil {
				return err
			}
			if s.wire == Chat {
				if err := emitSSE(s.w, "", object{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{}, "hosted_outputs": []any{item}}); err != nil {
					return err
				}
			} else {
				if !state.started {
					if err := s.event("response.output_item.added", object{"output_index": i, "item": item}); err != nil {
						return err
					}
				}
				if err := s.event("response."+kind+"."+status, object{"output_index": i, "item_id": item["id"]}); err != nil {
					return err
				}
				if status == "completed" || status == "failed" {
					if err := s.event("response.output_item.done", object{"output_index": i, "item": item}); err != nil {
						return err
					}
					state.closed = true
				}
			}
			state.started = true
			state.status = status
			continue
		}
		if kind == "reasoning" && s.wire != Anthropic {
			state := s.progressState[i]
			if state == nil {
				state = &emittedItem{id: fmt.Sprintf("item_%s_%d", s.id, i)}
				s.progressState[i] = state
			}
			text := reasoningText(item)
			if !strings.HasPrefix(text, state.sent) {
				return &Error{502, "upstream_stream_rewritten", 0}
			}
			if !state.started {
				if e := s.start(); e != nil {
					return e
				}
				if s.wire == Responses {
					start := clone(item)
					start["id"] = state.id
					start["summary"] = []any{}
					delete(start, "encrypted_content")
					if e := s.event("response.output_item.added", object{"output_index": i, "item": start}); e != nil {
						return e
					}
				}
				state.started = true
			}
			if text != "" && !state.partStarted && s.wire == Responses {
				if e := s.event("response.reasoning_summary_part.added", object{"output_index": i, "item_id": state.id, "summary_index": 0, "part": object{"type": "summary_text", "text": ""}}); e != nil {
					return e
				}
				state.partStarted = true
			}
			if delta := text[state.bytes:]; delta != "" {
				if s.wire == Chat {
					if e := s.chat(object{"reasoning_content": delta}, nil); e != nil {
						return e
					}
				} else {
					if e := s.event("response.reasoning_summary_text.delta", object{"output_index": i, "item_id": state.id, "summary_index": 0, "delta": delta}); e != nil {
						return e
					}
				}
			}
			state.sent = text
			state.bytes = len(text)
			continue
		}
		if s.wire == Anthropic && kind == "reasoning" {
			if !ready[i] {
				return pendingBlockBudget(items[i:])
			}
			blocks := reasoningToAnthropic(item, true)
			for _, v := range blocks {
				block := obj(v)
				state := s.progressState[blockIndex]
				if state == nil {
					state = &emittedItem{kind: "reasoning", source: i}
					s.progressState[blockIndex] = state
				}
				if !state.started {
					ok, e := s.closeBefore(blockIndex, ready)
					if e != nil {
						return e
					}
					if !ok {
						return pendingBlockBudget(items[i:])
					}
					if err := s.start(); err != nil {
						return err
					}
					start := clone(block)
					if block["type"] == "thinking" {
						start["thinking"] = ""
						delete(start, "signature")
					}
					if err := s.event("content_block_start", object{"type": "content_block_start", "index": blockIndex, "content_block": start}); err != nil {
						return err
					}
					if block["type"] == "thinking" {
						if text := str(block["thinking"]); text != "" {
							if err := s.event("content_block_delta", object{"type": "content_block_delta", "index": blockIndex, "delta": object{"type": "thinking_delta", "thinking": text}}); err != nil {
								return err
							}
						}
						if sig := str(block["signature"]); sig != "" {
							if err := s.event("content_block_delta", object{"type": "content_block_delta", "index": blockIndex, "delta": object{"type": "signature_delta", "signature": sig}}); err != nil {
								return err
							}
						}
					}
					if err := s.event("content_block_stop", object{"type": "content_block_stop", "index": blockIndex}); err != nil {
						return err
					}
					state.started = true
					state.closed = true
				}
				blockIndex++
			}
			continue
		}
		idx := i
		if s.wire == Anthropic {
			idx = blockIndex
			blockIndex++
		}
		if kind != "message" && kind != "function_call" {
			if s.wire == Anthropic || kind != "reasoning" {
				return pendingBlockBudget(items[i:])
			}
			continue
		}
		text := ""
		if kind == "message" {
			parts := arr(item["content"])
			if len(parts) != 1 || obj(parts[0])["type"] != "output_text" {
				if s.wire == Anthropic {
					return pendingBlockBudget(items[i:])
				}
				continue
			}
			text = str(obj(parts[0])["text"])
		} else {
			if special[str(item["name"])] || str(obj(item["provider_metadata"])["tool_kind"]) != "" && obj(item["provider_metadata"])["tool_kind"] != "function" {
				if s.wire == Anthropic {
					return pendingBlockBudget(items[i:])
				}
				continue
			}
			if str(item["name"]) == "" || str(item["call_id"]) == "" {
				return nil
			}
			text = str(item["arguments"])
			// A fragmented function name is not stable until argument bytes or an
			// explicit item boundary arrive. Never publish a partial tool identity.
			if text == "" && !ready[i] {
				return pendingBlockBudget(items[i:])
			}
		}
		state := s.progressState[idx]
		if state == nil {
			state = &emittedItem{id: fmt.Sprintf("item_%s_%d", s.id, i), kind: kind, source: i}
			s.progressState[idx] = state
		}
		if len(text) < state.bytes || !strings.HasPrefix(text, state.sent) {
			return &Error{502, "upstream_stream_rewritten", 0}
		}
		delta := text[state.bytes:]
		if !state.started {
			if s.wire == Anthropic {
				ok, e := s.closeBefore(idx, ready)
				if e != nil {
					return e
				}
				if !ok {
					return pendingBlockBudget(items[i:])
				}
			}
			if err := s.start(); err != nil {
				return err
			}
			switch s.wire {
			case Responses:
				start := clone(item)
				start["id"] = state.id
				start["status"] = "in_progress"
				if kind == "message" {
					start["content"] = []any{}
				} else {
					start["arguments"] = ""
				}
				if err := s.event("response.output_item.added", object{"output_index": i, "item": start}); err != nil {
					return err
				}
				if kind == "message" {
					if err := s.event("response.content_part.added", object{"output_index": i, "item_id": state.id, "content_index": 0, "part": object{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
						return err
					}
				}
			case Anthropic:
				block := object{"type": "text", "text": ""}
				if kind == "function_call" {
					block = object{"type": "tool_use", "id": item["call_id"], "name": item["name"], "input": object{}}
					copyFields(block, item, "extra_content", "provider_metadata")
				}
				if err := s.event("content_block_start", object{"type": "content_block_start", "index": idx, "content_block": block}); err != nil {
					return err
				}
			case Chat:
				if kind == "function_call" {
					call := responseCallToChat(item)
					obj(call["function"])["arguments"] = ""
					call["index"] = i
					if err := s.chat(object{"tool_calls": []any{call}}, nil); err != nil {
						return err
					}
				}
			}
			state.started = true
		}
		if delta != "" {
			switch s.wire {
			case Responses:
				name := "response.output_text.delta"
				m := object{"output_index": i, "item_id": state.id, "delta": delta}
				if kind == "message" {
					m["content_index"] = 0
				} else {
					name = "response.function_call_arguments.delta"
				}
				if err := s.event(name, m); err != nil {
					return err
				}
			case Anthropic:
				d := object{"type": "text_delta", "text": delta}
				if kind == "function_call" {
					d = object{"type": "input_json_delta", "partial_json": delta}
				}
				if err := s.event("content_block_delta", object{"type": "content_block_delta", "index": idx, "delta": d}); err != nil {
					return err
				}
			case Chat:
				d := object{"content": delta}
				if kind == "function_call" {
					d = object{"tool_calls": []any{object{"index": i, "function": object{"arguments": delta}}}}
				}
				if err := s.chat(d, nil); err != nil {
					return err
				}
			}
		}
		state.sent = text
		state.bytes = len(text)
	}
	return nil
}

// Stream publishes canonical Chat chunks without DONE framing. The finalized
// return value is suitable for the next hosted iteration only when err is nil.
func (HostedCodec) Stream(ctx context.Context, resp *http.Response, wire Wire, maxBytes int64, emit func(json.RawMessage) error) (json.RawMessage, error) {
	if resp == nil || resp.Body == nil {
		return nil, &Error{502, "missing_upstream_body", 0}
	}
	if maxBytes <= 0 || maxBytes > 32<<20 {
		resp.Body.Close()
		return nil, &Error{400, "invalid_translation_limit", 0}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, e := (HostedCodec{}).Collect(ctx, resp, wire, maxBytes)
		if e != nil {
			return b, e
		}
		m, e := decode(b)
		if e != nil {
			return nil, e
		}
		for _, v := range arr(m["choices"]) {
			c := obj(v)
			chunk := object{"id": m["id"], "model": m["model"], "object": "chat.completion.chunk", "choices": []any{object{"index": c["index"], "delta": c["message"], "finish_reason": c["finish_reason"]}}}
			copyFields(chunk, m, "usage", "hosted_outputs")
			raw, _ := json.Marshal(chunk)
			if e = emit(raw); e != nil {
				return nil, e
			}
		}
		return b, nil
	}
	defer resp.Body.Close()
	stop := context.AfterFunc(ctx, func() { resp.Body.Close() })
	defer stop()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{resp.StatusCode, "upstream_refused", 0}
	}
	acc := newTranslationStream(wire)
	usage := Usage{}
	terminal := false
	var final object
	size := 0
	sink := &chunkWriter{emit: emit}
	emitter := &streamEmitter{w: sink, wire: Chat, id: newID()}
	err := readSSE(resp.Body, func(_ string, b []byte) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		size += len(b)
		if int64(size) > maxBytes {
			return &Error{502, "translation_buffer_limit", 0}
		}
		if string(b) == "[DONE]" {
			if wire == Chat {
				terminal = true
			}
			return nil
		}
		m, e := decode(b)
		if e != nil {
			return &Error{502, "invalid_upstream_event", 0}
		}
		mergeUsage(&usage, readUsage(m, wire))
		mergeUsage(&usage, readUsage(obj(m["response"]), wire))
		mergeUsage(&usage, readUsage(obj(m["message"]), wire))
		typ := str(m["type"])
		if typ == "error" || typ == "response.failed" || m["error"] != nil {
			return &Error{502, "upstream_stream_error", 0}
		}
		if wire == Responses && (typ == "response.completed" || typ == "response.incomplete") {
			final = obj(m["response"])
			terminal = true
		}
		if wire == Anthropic && typ == "message_stop" {
			terminal = true
		}
		if e = acc.consume(m); e != nil {
			return e
		}
		if wire == Chat {
			return emit(append(json.RawMessage(nil), b...))
		}
		items, ready, e := acc.progressItems()
		if e != nil {
			return e
		}
		return emitter.progress(items, ready, object{}, Chat)
	})
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && !terminal {
		err = &Error{502, "upstream_stream_incomplete", 0}
	}
	if err != nil {
		b, _ := json.Marshal(object{"usage": usageObject(usage, Chat)})
		return b, err
	}
	var r result
	if final != nil {
		r, err = parseResult(final, wire)
	} else {
		r, err = acc.result()
	}
	if err != nil {
		return nil, err
	}
	r.Usage = usage
	if wire != Chat {
		if err = emitter.finish(r); err != nil {
			return nil, err
		}
	}
	return json.Marshal(r.response(Chat))
}

type chunkWriter struct {
	emit    func(json.RawMessage) error
	pending string
}

func (*chunkWriter) Header() http.Header { return http.Header{} }
func (*chunkWriter) WriteHeader(int)     {}
func (*chunkWriter) Flush()              {}
func (w *chunkWriter) Write(p []byte) (int, error) {
	w.pending += string(p)
	for {
		frame, rest, ok := strings.Cut(w.pending, "\n\n")
		if !ok {
			break
		}
		w.pending = rest
		if e := readSSE(strings.NewReader(frame+"\n\n"), func(_ string, b []byte) error {
			if string(b) == "[DONE]" {
				return nil
			}
			return w.emit(append(json.RawMessage(nil), b...))
		}); e != nil {
			return 0, e
		}
	}
	return len(p), nil
}

func mergeHostedOutputs(existing, updates []any) []any {
	for _, v := range updates {
		q := obj(v)
		matched := false
		for i, old := range existing {
			p := obj(old)
			if q["id"] != nil && p["id"] == q["id"] && p["type"] == q["type"] {
				merged := clone(p)
				for k, v := range q {
					merged[k] = v
				}
				existing[i] = merged
				matched = true
				break
			}
		}
		if !matched {
			existing = append(existing, clone(q))
		}
	}
	return existing
}
func (s *streamEmitter) verifyProgress(out object) error {
	if s.progressState == nil {
		return nil
	}
	if s.wire == Chat {
		return nil
	} // Chat has no final text snapshot on its terminal chunk.
	items := arr(out["output"])
	if s.wire == Anthropic {
		items = arr(out["content"])
	}
	for i, state := range s.progressState {
		if !state.started || state.bytes == 0 {
			continue
		}
		if i >= len(items) {
			return &Error{502, "upstream_stream_rewritten", 0}
		}
		item := obj(items[i])
		value := ""
		if s.wire == Responses {
			switch item["type"] {
			case "message":
				parts := arr(item["content"])
				if len(parts) > 0 {
					value = str(obj(parts[0])["text"])
				}
			case "function_call":
				value = str(item["arguments"])
			default:
				continue
			}
		} else {
			switch item["type"] {
			case "text":
				value = str(item["text"])
			case "tool_use":
				b, _ := json.Marshal(item["input"])
				value = string(b)
				if parsed, e := decode([]byte(state.sent)); e == nil {
					normalized, _ := json.Marshal(parsed)
					if string(normalized) == value {
						continue
					}
				}
			default:
				continue
			}
		}
		if !strings.HasPrefix(value, state.sent) {
			return &Error{502, "upstream_stream_rewritten", 0}
		}
	}
	return nil
}

// A missing reasoning boundary may delay later Messages blocks, but cannot
// accumulate an unbounded answer behind a withheld signature.
func pendingBlockBudget(items []any) error {
	b, e := json.Marshal(items)
	if e != nil {
		return e
	}
	if len(b) > 2<<20 {
		return &Error{502, "translation_pending_block_limit", 0}
	}
	return nil
}

func (s *streamEmitter) closeBefore(index int, ready map[int]bool) (bool, error) {
	if index == 0 {
		return true, nil
	}
	previous := s.progressState[index-1]
	if previous == nil || !previous.started || previous.closed {
		return true, nil
	}
	if previous.kind == "function_call" && !ready[previous.source] {
		return false, nil
	}
	if e := s.event("content_block_stop", object{"type": "content_block_stop", "index": index - 1}); e != nil {
		return false, e
	}
	previous.closed = true
	return true, nil
}
