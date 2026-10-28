// SPDX-License-Identifier: Apache-2.0
// Framing behavior adapted from opencodex src/adapters/zed.ts; see LICENSE.upstream.
package zed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
)

type eventStream struct {
	ctx                      context.Context
	source                   io.ReadCloser
	reader                   *bufio.Reader
	provider                 Provider
	buffer                   []byte
	finished, nativeTerminal bool
	closeOnce                sync.Once
	stop                     func() bool
	google                   googleStream
	aliases                  map[string]string
	custom                   map[string]bool
}

func newEventStream(ctx context.Context, body io.ReadCloser, provider Provider) *eventStream {
	s := &eventStream{ctx: ctx, source: body, reader: bufio.NewReaderSize(body, MaxFrameBytes+2), provider: provider}
	s.stop = context.AfterFunc(ctx, func() { s.closeSource() })
	return s
}
func (s *eventStream) closeSource() { s.closeOnce.Do(func() { _ = s.source.Close() }) }
func (s *eventStream) Close() error { s.stop(); s.closeSource(); return nil }

// Read retains at most one bounded input frame and its output. It uses downstream
// backpressure rather than a producer goroutine or an unbounded event queue.
func (s *eventStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.buffer) == 0 {
		if err := s.ctx.Err(); err != nil {
			s.closeSource()
			return 0, err
		}
		if s.finished {
			return 0, io.EOF
		}
		line, err := s.reader.ReadSlice('\n')
		frameSize := len(line)
		if frameSize > 0 && line[frameSize-1] == '\n' {
			frameSize--
		}
		if frameSize > MaxFrameBytes || errors.Is(err, bufio.ErrBufferFull) {
			s.fail("Zed stream frame exceeded the size limit")
		} else if err != nil {
			if s.ctx.Err() != nil {
				return 0, s.ctx.Err()
			}
			switch {
			case err != io.EOF:
				s.fail("Zed stream read failed")
			case strings.TrimSpace(string(line)) != "":
				s.fail("Zed stream ended inside a partial frame")
			case !s.nativeTerminal:
				s.fail("Zed stream ended before completion")
			default:
				s.finish()
			}
		} else {
			s.process(strings.TrimSpace(string(line)))
		}
	}
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	n := copy(p, s.buffer)
	s.buffer = s.buffer[n:]
	return n, nil
}
func (s *eventStream) emit(m object) {
	restoreNames(m, s.aliases, s.custom)
	b, err := json.Marshal(m)
	if err != nil {
		s.fail("Zed stream sent an invalid event")
		return
	}
	s.buffer = append(s.buffer, "data: "...)
	s.buffer = append(s.buffer, b...)
	s.buffer = append(s.buffer, '\n', '\n')
}
func (s *eventStream) end() { s.finished = true; s.stop(); s.closeSource() }
func (s *eventStream) fail(message string) {
	// Provider text can contain credentials even in nested error objects. Emit only
	// fixed local diagnostics; no account-specific redaction state is retained.
	e := object{"message": message, "type": "api_error"}
	s.emit(object{"type": "error", "error": e})
	s.end()
}
func (s *eventStream) finish() {
	// Do not overwrite a real terminal snapshot with an empty synthetic response.
	if !s.nativeTerminal {
		switch s.provider {
		case Anthropic:
			s.emit(object{"type": "message_stop"})
		case OpenAI:
			s.emit(object{"type": "response.completed", "response": object{"output": []any{}}})
		default:
			s.emit(object{"choices": []any{object{"index": 0, "delta": object{}, "finish_reason": "stop"}}})
		}
	}
	if s.provider == Google || s.provider == XAI {
		s.buffer = append(s.buffer, "data: [DONE]\n\n"...)
	}
	s.end()
}
func (s *eventStream) process(line string) {
	if strings.HasPrefix(line, "data:") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	}
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return
	}
	if line == "[DONE]" {
		s.finish()
		return
	}
	m, err := decodeObject([]byte(line))
	if err != nil {
		s.fail("Zed stream sent a malformed frame")
		return
	}
	if status, ok := m["status"]; ok {
		kind := stringOf(status)
		if st := objectOf(status); st != nil {
			kind = stringOf(st["type"])
			if kind == "" && len(st) == 1 {
				for k := range st {
					kind = k
				}
			}
		}
		switch kind {
		case "failed", "error":
			s.fail("Zed request failed")
		case "stream_ended", "completed":
			s.finish()
		}
		return
	}
	if event, ok := m["event"]; ok {
		m = objectOf(event)
		if m == nil {
			s.fail("Zed stream sent a malformed frame")
			return
		}
	}
	if m["error"] != nil || m["type"] == "error" || m["type"] == "response.failed" {
		s.fail("Zed request failed")
		return
	}
	if nativeTerminal(m) {
		s.nativeTerminal = true
	}
	if s.provider == Google {
		event, err := s.google.convert(m)
		if err != nil {
			s.fail("Zed sent an unsupported Google event")
			return
		}
		if event != nil {
			s.emit(event)
		}
		return
	}
	s.emit(m)
}
func nativeTerminal(m object) bool {
	switch m["type"] {
	case "message_stop", "response.completed", "response.incomplete", "response.failed":
		return true
	}
	for _, v := range arrayOf(m["choices"]) {
		if objectOf(v)["finish_reason"] != nil {
			return true
		}
	}
	for _, v := range arrayOf(m["candidates"]) {
		if objectOf(v)["finishReason"] != nil {
			return true
		}
	}
	return false
}
