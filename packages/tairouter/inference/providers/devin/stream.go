// SPDX-License-Identifier: Apache-2.0
// Protocol mappings adapted from the sources identified in LICENSE-MIT.
package devin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type streamBody struct {
	*io.PipeReader
	source io.ReadCloser
	once   sync.Once
	stop   func() bool
}

func (b *streamBody) Close() error {
	b.once.Do(func() { b.stop(); _ = b.PipeReader.Close(); _ = b.source.Close() })
	return nil
}

func newStream(ctx context.Context, source io.ReadCloser) io.ReadCloser {
	return newStreamIdle(ctx, source, 120*time.Second)
}

// Time only a pending raw read. Downstream backpressure is not evidence that
// Cognition has stopped sending, so no raw-idle timer runs during SSE writes.
type rawIdleBody struct {
	io.ReadCloser
	idle     time.Duration
	timedOut atomic.Bool
}

func (r *rawIdleBody) Read(b []byte) (int, error) {
	timer := time.AfterFunc(r.idle, func() { r.timedOut.Store(true); _ = r.ReadCloser.Close() })
	n, err := r.ReadCloser.Read(b)
	timer.Stop()
	return n, err
}

func newStreamIdle(ctx context.Context, source io.ReadCloser, idle time.Duration) io.ReadCloser {
	raw := &rawIdleBody{ReadCloser: source, idle: idle}
	source = raw
	r, w := io.Pipe()
	b := &streamBody{PipeReader: r, source: source}
	b.stop = context.AfterFunc(ctx, func() { _ = w.CloseWithError(ctx.Err()); _ = source.Close() })
	go func() {
		defer source.Close()
		defer b.stop()
		err := transcode(ctx, source, w)
		if raw.timedOut.Load() {
			err = &inference.Error{Status: 504, Code: "devin_stream_idle_timeout"}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		_ = w.CloseWithError(err)
	}()
	return b
}

type chatStream struct {
	id             string
	created        int64
	writer         io.Writer
	tools          map[string]int
	names          map[string]string
	last           string
	finish         string
	usage          object
	state          *ResponseContext
	held           [][]byte
	heldBytes      int
	projectedArgs  map[string]*strings.Builder
	projectedBytes int
}

func (s *chatStream) emit(delta object, finish any, usage object) error {
	choices := []any{}
	if delta != nil || finish != nil {
		choices = append(choices, object{"index": 0, "delta": delta, "finish_reason": finish})
	}
	chunk := object{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "choices": choices}
	if s.state != nil {
		chunk["model"] = s.state.Model
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, err := json.Marshal(chunk)
	if err != nil {
		return errWire
	}
	if len(b) > 2<<20 {
		return errLimit
	}
	encoded := append(append([]byte("data: "), b...), []byte("\n\n")...)
	if s.state != nil && s.state.Inbound == inference.Anthropic && delta["reasoning_content"] == nil && delta["reasoning_signature"] == nil {
		if s.heldBytes+len(encoded) > 32<<20 {
			return &inference.Error{Status: 413, Code: "translation_buffer_limit"}
		}
		s.held = append(s.held, encoded)
		s.heldBytes += len(encoded)
		return nil
	}
	_, err = s.writer.Write(encoded)
	return err
}

func (s *chatStream) text(key, value string) error {
	if !utf8.ValidString(value) {
		return errWire
	}
	// The parent SSE parser has a smaller event cap than a Connect frame. Split
	// large deltas on UTF-8 boundaries without retaining the complete conversation.
	for len(value) > 0 {
		n := len(value)
		if n > 32<<10 {
			n = 32 << 10
			for n > 0 && !utf8.RuneStart(value[n]) {
				n--
			}
		}
		if err := s.emit(object{key: value[:n]}, nil, nil); err != nil {
			return err
		}
		value = value[n:]
	}
	return nil
}

func transcode(ctx context.Context, r io.Reader, w io.Writer) error {
	id, err := uuid()
	if err != nil {
		return err
	}
	s := chatStream{id: "chatcmpl-" + id, created: time.Now().Unix(), writer: w, tools: map[string]int{}, names: map[string]string{}, finish: "stop", usage: object{}}
	s.state, _ = ctx.Value(responseContextKey{}).(*ResponseContext)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, b, err := readFrame(r)
		if err != nil {
			if errors.Is(err, errLimit) {
				return err
			}
			return &inference.Error{Status: 502, Code: "devin_truncated_stream"}
		}
		if flags&2 != 0 {
			if err := trailer(b); err != nil {
				return err
			}
			if s.state != nil && s.state.RequiredTool && len(s.tools) == 0 {
				// Cognition has no native required selector. Never report a text-only
				// answer as satisfying it, or replay after visible stream output.
				return &inference.Error{Status: 502, Code: "devin_required_tool_missing"}
			}
			for id, args := range s.projectedArgs {
				if _, err := s.state.ProjectToolCall(s.names[id], args.String()); err != nil {
					return err
				}
			}
			if len(s.tools) > 0 && s.finish == "stop" {
				s.finish = "tool_calls"
			}
			if err := s.emit(object{}, s.finish, nil); err != nil {
				return err
			}
			if len(s.usage) > 0 {
				if err := s.emit(nil, nil, s.usage); err != nil {
					return err
				}
			}
			for _, event := range s.held {
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, err := w.Write(event); err != nil {
					return err
				}
			}
			s.held = nil
			_, err = io.WriteString(w, "data: [DONE]\n\n")
			if err == nil {
				markComplete(ctx)
			}
			return err
		}
		if err := s.decodeFrame(b); err != nil {
			return err
		}
	}
}

func trailer(b []byte) error {
	var m object
	if json.Unmarshal(b, &m) != nil || m == nil {
		return errWire
	}
	v, exists := m["error"]
	if !exists || v == nil {
		return nil
	}
	code := str(obj(v)["code"])
	status := 502
	switch code {
	case "unauthenticated":
		status = 401
	case "permission_denied":
		status = 403
	case "resource_exhausted":
		status = 429
	case "invalid_argument", "failed_precondition", "out_of_range":
		status = 400
	case "deadline_exceeded":
		status = 504
	case "unavailable":
		status = 503
	case "not_found":
		status = 404
	case "unimplemented":
		status = 501
	case "canceled":
		status = 499
	default:
		code = "upstream_error"
	}
	// Upstream messages can quote the credential-bearing request. Only a closed
	// code and status escape the adapter. Replay policy belongs to the executor.
	return &inference.Error{Status: status, Code: "devin_" + code}
}

func (s *chatStream) decodeFrame(b []byte) error {
	var authoritative object
	signatureType := ""
	if err := walk(b, func(f field) error {
		if f.num == 7 && f.wire == 2 {
			u, err := modelUsage(f.b)
			if err != nil {
				return err
			}
			if len(u) > 0 {
				authoritative = u
			}
		}
		if f.num == 21 && f.wire == 2 && len(f.b) <= 4096 {
			signatureType = string(f.b)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(authoritative) > 0 {
		for k, v := range authoritative {
			s.usage[k] = v
		}
		if err := s.emit(nil, nil, s.usage); err != nil {
			return err
		}
	}
	return walk(b, func(f field) error {
		switch {
		case f.num == 3 && f.wire == 2:
			return s.text("content", string(f.b))
		case f.num == 9 && f.wire == 2:
			return s.text("reasoning_content", string(f.b))
		case f.num == 10 && f.wire == 2:
			if len(f.b) > 1<<20 || !utf8.Valid(f.b) || !utf8.ValidString(signatureType) {
				return errLimit
			}
			sig := EncodeSignature(Signature{Value: string(f.b), Type: signatureType})
			if sig != "" {
				return s.emit(object{"reasoning_signature": sig}, nil, nil)
			}
		case f.num == 6 && f.wire == 2:
			return s.tool(f.b)
		case f.num == 5 && f.wire == 0:
			s.finish = "stop"
			switch f.n {
			case 1, 3:
				s.finish = "length"
			case 10:
				s.finish = "tool_calls"
			case 11:
				s.finish = "content_filter"
			}
		case f.num == 28 && f.wire == 2 && len(authoritative) == 0:
			u, err := dimensionUsage(f.b)
			if err != nil {
				return err
			}
			if len(u) > 0 {
				for k, v := range u {
					s.usage[k] = v
				}
				return s.emit(nil, nil, s.usage)
			}
		}
		return nil
	})
}

func (s *chatStream) tool(b []byte) error {
	id, name, args := "", "", ""
	hasArgs := false
	if err := walk(b, func(f field) error {
		if f.wire != 2 {
			return nil
		}
		if !utf8.Valid(f.b) {
			return errWire
		}
		switch f.num {
		case 1:
			id = string(f.b)
		case 2:
			name = string(f.b)
		case 3:
			args = string(f.b)
			hasArgs = true
		}
		return nil
	}); err != nil {
		return err
	}
	if len(id) > 4096 || len(name) > 4096 {
		return errLimit
	}
	if id != "" && name != "" {
		if s.state != nil {
			var err error
			name, err = s.state.RestoreToolName(name)
			if err != nil {
				return err
			}
		}
		index, exists := s.tools[id]
		if !exists {
			if len(s.tools) >= 1024 {
				return errLimit
			}
			index = len(s.tools)
			s.tools[id] = index
			s.names[id] = name
			call := object{"index": index, "id": id, "type": "function", "function": object{"name": name, "arguments": ""}}
			if s.state != nil {
				for _, tool := range s.state.tools {
					if tool.canonical() == name && (tool.kind != FunctionTool || tool.namespace != "") {
						call["provider_metadata"] = object{"tool_kind": tool.kind, "tool_name": tool.name, "tool_namespace": tool.namespace}
						if tool.kind != FunctionTool {
							if s.projectedArgs == nil {
								s.projectedArgs = map[string]*strings.Builder{}
							}
							s.projectedArgs[id] = &strings.Builder{}
						}
					}
				}
			}
			if err := s.emit(object{"tool_calls": []any{call}}, nil, nil); err != nil {
				return err
			}
		} else if s.names[id] != name {
			return errWire
		}
		s.last = id
	}
	if !hasArgs {
		return nil
	}
	if id == "" {
		id = s.last
	}
	index, ok := s.tools[id]
	if !ok {
		return errWire
	}
	if buffer := s.projectedArgs[id]; buffer != nil {
		if s.projectedBytes+len(args) > maxFrame {
			return errLimit
		}
		s.projectedBytes += len(args)
		buffer.WriteString(args)
	}
	for len(args) > 0 {
		n := len(args)
		if n > 32<<10 {
			n = 32 << 10
			for n > 0 && !utf8.RuneStart(args[n]) {
				n--
			}
		}
		if err := s.emit(object{"tool_calls": []any{object{"index": index, "function": object{"arguments": args[:n]}}}}, nil, nil); err != nil {
			return err
		}
		args = args[n:]
	}
	return nil
}

func modelUsage(b []byte) (object, error) {
	values := map[int]int64{}
	err := walk(b, func(f field) error {
		if f.wire == 0 && f.num >= 2 && f.num <= 5 {
			if f.n > math.MaxInt64 {
				return errWire
			}
			values[f.num] = int64(f.n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	u := object{}
	input, hasInput := values[2]
	output, hasOutput := values[3]
	write, hasWrite := values[4]
	read, hasRead := values[5]
	if write > math.MaxInt64-read {
		return nil, errWire
	}
	cache := write + read
	// The pinned mapping folds cache counts only when they cannot be a subset.
	// Missing input/output fields stay unknown instead of becoming invented zeroes.
	if hasInput && input < cache {
		if input > math.MaxInt64-cache {
			return nil, errWire
		}
		input += cache
	}
	if hasInput {
		u["prompt_tokens"] = input
	}
	if hasOutput {
		u["completion_tokens"] = output
	}
	if hasRead {
		u["prompt_tokens_details"] = object{"cached_tokens": read}
	}
	if hasWrite {
		u["cache_creation_input_tokens"] = write
	}
	if hasInput && hasOutput {
		if input > math.MaxInt64-output {
			return nil, errWire
		}
		u["total_tokens"] = input + output
	}
	return u, nil
}

func dimensionUsage(b []byte) (object, error) {
	u := object{}
	err := walk(b, func(f field) error {
		if f.num != 2 || f.wire != 2 {
			return nil
		}
		metric := ""
		var value *int64
		if err := walk(f.b, func(entry field) error {
			if entry.num == 5 && entry.wire == 2 {
				metric = string(entry.b)
			}
			if entry.num == 4 && entry.wire == 2 {
				return walk(entry.b, func(display field) error {
					if display.num == 2 && display.wire == 5 {
						n := math.Round(float64(math.Float32frombits(binary.LittleEndian.Uint32(display.b))))
						if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= float64(math.MaxInt64) {
							return errWire
						}
						v := int64(n)
						value = &v
					}
					return nil
				})
			}
			return nil
		}); err != nil {
			return err
		}
		if value == nil {
			return nil
		}
		switch metric {
		case "input_tokens":
			u["prompt_tokens"] = *value
		case "output_tokens":
			u["completion_tokens"] = *value
		case "cached_input_tokens", "cache_read_input_tokens":
			d := obj(u["prompt_tokens_details"])
			if d == nil {
				d = object{}
			}
			old, _ := d["cached_tokens"].(int64)
			if old > math.MaxInt64-*value {
				return errWire
			}
			d["cached_tokens"] = old + *value
			u["prompt_tokens_details"] = d
		case "cache_creation_input_tokens":
			u["cache_creation_input_tokens"] = *value
		case "reasoning_tokens", "output_reasoning_tokens":
			u["completion_tokens_details"] = object{"reasoning_tokens": *value}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	in, hasIn := u["prompt_tokens"].(int64)
	out, hasOut := u["completion_tokens"].(int64)
	if hasIn && hasOut {
		if in > math.MaxInt64-out {
			return nil, errWire
		}
		u["total_tokens"] = in + out
	}
	return u, nil
}
