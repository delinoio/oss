// SPDX-License-Identifier: Apache-2.0
// NDJSON event semantics adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// streamBody joins the producer on Close. Closing the source unblocks a pending
// upstream read; closing the pipe unblocks a producer waiting for client demand.
type streamBody struct {
	*io.PipeReader
	source io.ReadCloser
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (b *streamBody) Close() error {
	b.once.Do(func() { b.cancel(); b.PipeReader.Close(); b.source.Close(); <-b.done })
	return nil
}
func (a *Adapter) Decode(ctx context.Context, r *http.Response) (*http.Response, error) {
	return a.DecodeWithInput(ctx, inference.AdapterInput{}, r)
}
func (*Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, r *http.Response) (*http.Response, error) {
	if r == nil || r.Body == nil {
		return nil, fail(502, "command_code_missing_body")
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return r, nil
	}
	child, cancel := context.WithCancel(ctx)
	rd, wr := io.Pipe()
	b := &streamBody{PipeReader: rd, source: r.Body, cancel: cancel, done: make(chan struct{})}
	stop := context.AfterFunc(child, func() { r.Body.Close(); wr.CloseWithError(child.Err()) })
	go func() {
		defer close(b.done)
		defer stop()
		defer cancel()
		defer r.Body.Close()
		err := decodeStream(child, in, r.Body, wr)
		wr.CloseWithError(err)
	}()
	out := *r
	out.Header = r.Header.Clone()
	out.Header.Set("Content-Type", "text/event-stream")
	out.Header.Del("Content-Length")
	out.Header.Del("Content-Encoding")
	out.ContentLength = -1
	out.Body = b
	return &out, nil
}
func usage(v any) object {
	m := obj(v)
	if m == nil {
		return nil
	}
	in, out := m["inputTokens"], m["outputTokens"]
	if in == nil {
		in = float64(0)
	}
	if out == nil {
		out = float64(0)
	}
	u := object{"prompt_tokens": in, "completion_tokens": out}
	details := obj(m["inputTokenDetails"])
	d := object{}
	if v, ok := details["cacheReadTokens"]; ok {
		d["cached_tokens"] = v
	}
	if v, ok := details["cacheWriteTokens"]; ok {
		d["cache_creation_tokens"] = v
	}
	if len(d) > 0 {
		u["prompt_tokens_details"] = d
	}
	return u
}
func decodeStream(ctx context.Context, in inference.AdapterInput, src io.Reader, dst io.Writer) error {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	id := "chatcmpl-" + uuid()
	index := 0
	finished := false
	emit := func(delta object, finish any, u object) error {
		v := object{"id": id, "object": "chat.completion.chunk", "model": CanonicalModel(in.Target.Model), "choices": []any{object{"index": 0, "delta": delta, "finish_reason": finish}}}
		if u != nil {
			v["usage"] = u
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintf(dst, "data: %s\n\n", b)
		return e
	}
	emitError := func(code string, u object) error {
		v := object{"error": object{"type": "upstream_error", "code": code, "message": "Command Code generation failed"}}
		if u != nil {
			v["usage"] = u
		}
		b, _ := json.Marshal(v)
		_, e := fmt.Fprintf(dst, "data: %s\n\n", b)
		return e
	}
	filter := newToolFilter(in)
	tool := func(e object) error {
		callID := str(e["toolCallId"])
		if callID == "" {
			callID = uuid()
		}
		name := str(e["toolName"])
		if name == "" {
			name = "tool"
		}
		input, ok := e["input"]
		if !ok {
			input = e["args"]
		}
		if input == nil {
			input = object{}
		}
		args, ok := input.(string)
		if !ok {
			b, err := json.Marshal(input)
			if err != nil {
				return err
			}
			args = string(b)
		}
		err := emit(object{"tool_calls": []any{object{"index": index, "id": callID, "type": "function", "function": object{"name": name, "arguments": args}}}}, nil, nil)
		index++
		return err
	}
	flush := func(events []object) error {
		for _, e := range events {
			var err error
			switch e["type"] {
			case "tool-call":
				err = tool(e)
			case "reasoning-delta":
				err = emit(object{"reasoning_content": e["text"]}, nil, nil)
			case "text-delta":
				err = emit(object{"content": e["text"]}, nil, nil)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var e object
		if json.Unmarshal([]byte(line), &e) != nil || e == nil {
			continue
		}
		if finished {
			continue
		}
		switch str(e["type"]) {
		case "text-start", "text-end", "tool-input-start":
			if err := flush(filter.event(e)); err != nil {
				return err
			}
		case "text-delta", "reasoning-delta", "tool-call":
			if err := flush(filter.event(e)); err != nil {
				return err
			}
		case "finish-step", "finish":
			finished = true
			v := e["totalUsage"]
			if v == nil {
				v = e["usage"]
			}
			u := usage(v)
			reason := str(e["rawFinishReason"])
			if reason == "" {
				reason = str(e["finishReason"])
			}
			clean := reason == "stop" || reason == "tool_calls" || reason == "tool-calls" || reason == "tool_use"
			events, salvaged := filter.finish(clean)
			if err := flush(events); err != nil {
				return err
			}
			if reason == "error" {
				if err := emitError("upstream_error", u); err != nil {
					return err
				}
				break
			}
			if salvaged || reason == "tool-calls" || reason == "tool_use" {
				reason = "tool_calls"
			}
			if reason == "length" || reason == "max_tokens" {
				reason = "length"
			}
			if reason == "" {
				reason = "stop"
			}
			if err := emit(object{}, reason, u); err != nil {
				return err
			}
		case "error":
			events, _ := filter.finish(false)
			if err := flush(events); err != nil {
				return err
			}
			message := str(e["error"])
			if message == "" {
				message = str(obj(e["error"])["message"])
			}
			code := "upstream_error"
			s := strings.ToLower(message)
			if strings.Contains(s, "tool result is missing") || strings.Contains(s, "tool_result is missing") {
				code = "missing_tool_result"
			}
			if err := emitError(code, usage(e["usage"])); err != nil {
				return err
			}
			finished = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !finished {
		events, _ := filter.finish(false)
		if err := flush(events); err != nil {
			return err
		}
		if err := emit(object{}, "stop", nil); err != nil {
			return err
		}
	}
	_, err := io.WriteString(dst, "data: [DONE]\n\n")
	return err
}
