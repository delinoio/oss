// SPDX-License-Identifier: Apache-2.0
// Native protocol behavior is adapted from opencodex; see LICENSE.upstream.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type nativeCall struct {
	id, name, fallback string
	args               object
	size               int
}
type responseState struct {
	responseID string
	calls      []*nativeCall
	issued     map[string]bool
	parallel   bool
	usage      object
	model      string
	retained   int
}

func validID(s string) bool {
	if s == "" || len(s) > 256 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, e := n.Int64()
	return i, e == nil && i >= 0 && i <= 9007199254740991
}
func (s *responseState) ingest(p object) (object, error) {
	if p["error"] != nil {
		return nil, &inference.Error{Status: 502, Code: "ollama_native_error"}
	}
	if d, exists := p["done"]; exists {
		if _, ok := d.(bool); !ok {
			return nil, malformed()
		}
	}
	if model := str(p["model"]); model != "" {
		s.model = model
	}
	if in, ok := integer(p["prompt_eval_count"]); ok {
		if s.usage == nil {
			s.usage = object{}
		}
		s.usage["prompt_tokens"] = in
	}
	if out, ok := integer(p["eval_count"]); ok {
		if s.usage == nil {
			s.usage = object{}
		}
		s.usage["completion_tokens"] = out
	}
	delta := object{}
	if p["message"] == nil {
		return delta, nil
	}
	m := obj(p["message"])
	if m == nil {
		return nil, malformed()
	}
	if role, ok := m["role"]; ok && role != "assistant" {
		return nil, malformed()
	}
	for _, pair := range [][2]string{{"content", "content"}, {"thinking", "reasoning_content"}} {
		if v, ok := m[pair[0]]; ok {
			if _, ok := v.(string); !ok {
				return nil, malformed()
			}
			delta[pair[1]] = v
		}
	}
	if raw, ok := m["tool_calls"]; ok {
		calls, ok := raw.([]any)
		if !ok {
			return nil, malformed()
		}
		frame := map[*nativeCall]string{}
		for pos, raw := range calls {
			c := obj(raw)
			f := obj(c["function"])
			name := str(f["name"])
			args := obj(f["arguments"])
			if strings.TrimSpace(name) == "" || len(name) > 1024 || args == nil {
				return nil, malformed()
			}
			id := str(c["id"])
			if !validID(id) {
				id = ""
			}
			fallback := fmt.Sprintf("position:%d", pos)
			if index, ok := integer(f["index"]); ok {
				fallback = fmt.Sprintf("index:%d", index)
			}
			available := func(c *nativeCall) bool { seen, ok := frame[c]; return !ok || (id != "" && seen == id) }
			var existing *nativeCall
			if id != "" {
				for _, candidate := range s.calls {
					if candidate.id == id && available(candidate) {
						existing = candidate
						break
					}
				}
			}
			if existing == nil {
				for _, candidate := range s.calls {
					if candidate.fallback == fallback && (id == "" || candidate.id == "") && available(candidate) {
						existing = candidate
						break
					}
				}
			}
			if existing == nil {
				if len(s.calls) >= MaxPendingCalls || (!s.parallel && len(s.calls) > 0) {
					return nil, malformed()
				}
				existing = &nativeCall{id: id, name: name, fallback: fallback}
				s.calls = append(s.calls, existing)
				s.retained += len(name) + 128
			} else {
				if existing.name != name {
					return nil, malformed()
				}
				if existing.id == "" {
					existing.id = id
				}
			}
			data, e := json.Marshal(args)
			if e != nil {
				return nil, malformed()
			}
			s.retained += len(data) - existing.size
			if len(data) > MaxCallArgumentBytes || s.retained > MaxRetainedBytes {
				return nil, &inference.Error{Status: 502, Code: "translation_buffer_limit"}
			}
			existing.args = args
			existing.size = len(data)
			frame[existing] = id
		}
	}
	return delta, nil
}
func (s *responseState) flush() ([]any, error) {
	out := []any{}
	for i, c := range s.calls {
		id := c.id
		if !validID(id) || s.issued[id] {
			var b [16]byte
			if _, e := rand.Read(b[:]); e != nil {
				return nil, e
			}
			id = "ollama_call_" + hex.EncodeToString(b[:])
		}
		s.issued[id] = true
		args, e := json.Marshal(c.args)
		if e != nil {
			return nil, e
		}
		out = append(out, object{"index": i, "id": id, "type": "function", "function": object{"name": c.name, "arguments": string(args)}})
	}
	return out, nil
}
func (s *responseState) finish(p object) string {
	if len(s.calls) > 0 {
		return "tool_calls"
	}
	if p["done_reason"] == "length" {
		return "length"
	}
	if r := str(p["done_reason"]); r != "" {
		return r
	}
	return "stop"
}
func (a *Adapter) Decode(ctx context.Context, r *http.Response) (*http.Response, error) {
	return a.decode(ctx, inference.AdapterInput{}, r)
}
func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, r *http.Response) (*http.Response, error) {
	return a.decode(ctx, in, r)
}
func (a *Adapter) decode(ctx context.Context, in inference.AdapterInput, r *http.Response) (*http.Response, error) {
	if e := ctx.Err(); e != nil {
		if r != nil && r.Body != nil {
			r.Body.Close()
		}
		return nil, e
	}
	if r == nil || r.Body == nil {
		return nil, malformed()
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return r, nil
	}
	var responseID [16]byte
	if _, e := rand.Read(responseID[:]); e != nil {
		r.Body.Close()
		return nil, e
	}
	state := &responseState{responseID: "ollama_" + hex.EncodeToString(responseID[:]), issued: map[string]bool{}, parallel: true}
	if len(in.Body) > 0 {
		b, e := inference.TranslateRequestJSON(in.Body, in.Wire, inference.Chat)
		if e != nil {
			r.Body.Close()
			return nil, e
		}
		m, e := decode(b)
		if e != nil {
			r.Body.Close()
			return nil, e
		}
		state.parallel = m["parallel_tool_calls"] != false
		for _, v := range arr(m["messages"]) {
			for _, v := range arr(obj(v)["tool_calls"]) {
				state.issued[str(obj(v)["id"])] = true
			}
		}
	}
	out := new(http.Response)
	*out = *r
	out.Header = r.Header.Clone()
	out.Header.Del("Content-Length")
	out.Header.Del("Content-Encoding")
	out.ContentLength = -1
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "ndjson") || strings.Contains(ct, "jsonl") {
		out.Header.Set("Content-Type", "text/event-stream")
		s := &stream{ctx: ctx, source: r.Body, state: state}
		s.scan = bufio.NewScanner(r.Body)
		s.scan.Buffer(make([]byte, 4096), MaxFrameBytes+1)
		s.stop = context.AfterFunc(ctx, func() { s.closeSource() })
		out.Body = s
		return out, nil
	}
	defer r.Body.Close()
	stop := context.AfterFunc(ctx, func() { r.Body.Close() })
	defer stop()
	data, e := io.ReadAll(io.LimitReader(r.Body, MaxFrameBytes+1))
	if e != nil {
		return nil, e
	}
	if len(data) > MaxFrameBytes || !utf8.Valid(data) {
		return nil, malformed()
	}
	p, e := decode(data)
	if e != nil {
		return nil, e
	}
	if p["error"] != nil {
		return nil, &inference.Error{Status: 502, Code: "ollama_native_error"}
	}
	if p["done"] != true {
		return nil, malformed()
	}
	delta, e := state.ingest(p)
	if e != nil {
		return nil, e
	}
	calls, e := state.flush()
	if e != nil {
		return nil, e
	}
	delta["role"] = "assistant"
	if len(calls) > 0 {
		for _, c := range calls {
			delete(obj(c), "index")
		}
		delta["tool_calls"] = calls
	}
	body := object{"id": state.responseID, "object": "chat.completion", "model": state.model, "choices": []any{object{"index": 0, "message": delta, "finish_reason": state.finish(p)}}}
	if state.usage != nil {
		body["usage"] = state.usage
	}
	data, e = json.Marshal(body)
	if e != nil {
		return nil, e
	}
	out.Header.Set("Content-Type", "application/json")
	out.Body = io.NopCloser(bytes.NewReader(data))
	return out, nil
}

type stream struct {
	ctx     context.Context
	source  io.ReadCloser
	scan    *bufio.Scanner
	state   *responseState
	pending []byte
	done    bool
	err     error
	once    sync.Once
	stop    func() bool
}

func (s *stream) closeSource() { s.once.Do(func() { s.source.Close() }) }
func (s *stream) Close() error {
	if s.stop != nil {
		s.stop()
	}
	s.closeSource()
	return nil
}
func (s *stream) emit(v any) {
	b, _ := json.Marshal(v)
	s.pending = append(s.pending, []byte("data: ")...)
	s.pending = append(s.pending, b...)
	s.pending = append(s.pending, '\n', '\n')
}
func (s *stream) chunk(delta object, finish any, usage object) {
	v := object{"id": s.state.responseID, "object": "chat.completion.chunk", "model": s.state.model, "choices": []any{object{"index": 0, "delta": delta, "finish_reason": finish}}}
	if usage != nil {
		v["usage"] = usage
	}
	s.emit(v)
}
func (s *stream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for len(s.pending) == 0 && !s.done {
		if e := s.ctx.Err(); e != nil {
			s.err = e
			s.done = true
			break
		}
		if !s.scan.Scan() {
			s.err = s.scan.Err()
			if s.err == nil {
				s.err = malformed()
			}
			s.done = true
			break
		}
		line := bytes.TrimSpace(s.scan.Bytes())
		if len(line) == 0 {
			continue
		}
		if len(line) > MaxFrameBytes || !utf8.Valid(line) {
			s.err = malformed()
			s.done = true
			break
		}
		p, e := decode(line)
		if e != nil {
			s.err = e
			s.done = true
			break
		}
		delta, e := s.state.ingest(p)
		if e != nil {
			s.err = e
			s.done = true
			break
		}
		if len(delta) > 0 {
			s.chunk(delta, nil, nil)
		}
		if p["done"] == true {
			calls, e := s.state.flush()
			if e != nil {
				s.err = e
				s.done = true
				break
			}
			if len(calls) > 0 {
				s.chunk(object{"tool_calls": calls}, nil, nil)
			}
			s.chunk(object{}, s.state.finish(p), s.state.usage)
			s.pending = append(s.pending, []byte("data: [DONE]\n\n")...)
			s.done = true
		} else if len(delta) == 0 {
			s.pending = []byte(": ollama heartbeat\n\n")
		}
	}
	if s.done {
		s.Close()
	}
	if len(s.pending) > 0 {
		n := copy(b, s.pending)
		s.pending = s.pending[n:]
		return n, nil
	}
	if s.err != nil {
		return 0, s.err
	}
	return 0, io.EOF
}
