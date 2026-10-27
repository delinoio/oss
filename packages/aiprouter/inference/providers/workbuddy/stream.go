// SPDX-License-Identifier: Apache-2.0
package workbuddy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const maxEvent = 1 << 20

func wireError() error { return &inference.Error{Status: 502, Code: "workbuddy_invalid_stream"} }

// streamReader translates one event at a time without producer goroutines. Close
// closes the upstream immediately; cancellation interrupts a blocked upstream read.
type streamReader struct {
	terminal bool
	metadata map[string]object
	ctx      context.Context
	source   io.ReadCloser
	scan     *bufio.Scanner
	pending  *bytes.Reader
	aliases  map[string]string
	names    map[string]string
	sent     map[string]bool
	stop     func() bool
	once     sync.Once
}

func (s *streamReader) Close() error {
	var e error
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
		}
		e = s.source.Close()
	})
	return e
}
func decodeStream(ctx context.Context, r *http.Response, names map[string]string, metadata map[string]object) (*http.Response, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if r == nil || r.Body == nil {
		return nil, wireError()
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return r, nil
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "text/event-stream") {
		return nil, wireError()
	}
	s := &streamReader{ctx: ctx, source: r.Body, scan: bufio.NewScanner(r.Body), aliases: names, metadata: metadata, names: map[string]string{}, sent: map[string]bool{}}
	s.scan.Buffer(make([]byte, 4096), maxEvent)
	s.stop = context.AfterFunc(ctx, func() { _ = r.Body.Close() })
	out := new(http.Response)
	*out = *r
	out.Header = r.Header.Clone()
	out.Header.Del("Content-Length")
	out.Header.Del("Content-Encoding")
	out.ContentLength = -1
	out.Body = s
	return out, nil
}
func (s *streamReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if e := s.ctx.Err(); e != nil {
			return 0, e
		}
		if s.pending != nil && s.pending.Len() > 0 {
			return s.pending.Read(p)
		}
		if s.terminal {
			return 0, io.EOF
		}
		var data []string
		size := 0
		found := false
		for s.scan.Scan() {
			line := s.scan.Text()
			size += len(line) + 1
			if size > maxEvent {
				return 0, wireError()
			}
			if line == "" {
				if found {
					break
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				v := strings.TrimPrefix(line, "data:")
				v = strings.TrimPrefix(v, " ")
				data = append(data, v)
				found = true
			}
		}
		if e := s.scan.Err(); e != nil {
			return 0, e
		}
		if !found {
			return 0, io.EOF
		}
		payload := strings.TrimSpace(strings.Join(data, "\n"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			s.terminal = true
			for key, name := range s.names {
				if name != "" && !s.sent[key] {
					return 0, wireError()
				}
			}
			s.pending = bytes.NewReader([]byte("data: [DONE]\n\n"))
			continue
		}
		m, e := decode([]byte(payload))
		if e != nil {
			return 0, wireError()
		}
		if m["error"] != nil {
			return 0, wireError()
		}
		for ci, v := range arr(m["choices"]) {
			choice := obj(v)
			choiceIndex := strIndex(choice["index"], ci)
			d := obj(choice["delta"])
			for ti, v := range arr(d["tool_calls"]) {
				call := obj(v)
				f := obj(call["function"])
				name := str(f["name"])
				key := choiceIndex + ":" + strIndex(call["index"], ti)
				if name != "" {
					if len(s.names) >= 4096 && s.names[key] == "" {
						return 0, wireError()
					}
					s.names[key] += name
					if len(s.names[key]) > 64 {
						return 0, wireError()
					}
					delete(f, "name")
					if original, ok := s.aliases[s.names[key]]; ok {
						if s.sent[key] {
							return 0, wireError()
						}
						f["name"] = original
						if meta := s.metadata[original]; meta != nil {
							call["provider_metadata"] = meta
						}
						s.sent[key] = true
					} else if len(s.aliases) == 0 {
						f["name"] = name
						s.sent[key] = true
					} else {
						prefix := false
						for alias := range s.aliases {
							if strings.HasPrefix(alias, s.names[key]) {
								prefix = true
								break
							}
						}
						if !prefix {
							return 0, wireError()
						}
					}
				}
				if str(f["arguments"]) != "" && s.names[key] != "" && !s.sent[key] {
					return 0, wireError()
				}
			}
			if choice["finish_reason"] != nil {
				for key, name := range s.names {
					if strings.HasPrefix(key, choiceIndex+":") && name != "" && !s.sent[key] {
						return 0, wireError()
					}
				}
			}
		}
		b, e := json.Marshal(m)
		if e != nil {
			return 0, wireError()
		}
		s.pending = bytes.NewReader(append(append([]byte("data: "), b...), '\n', '\n'))
	}
}
func strIndex(v any, fallback int) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return strconv.Itoa(fallback)
}

// ErrorCode exposes only fixed diagnostics; arbitrary upstream text is private.
func ErrorCode(status int, payload []byte) string {
	var m object
	_ = json.Unmarshal(payload, &m)
	code := str(m["code"])
	if n, ok := m["code"].(float64); ok {
		code = strconv.FormatFloat(n, 'f', -1, 64)
	}
	switch code {
	case "11128":
		return "workbuddy_client_channel_rejected"
	case "11140":
		return "workbuddy_account_restricted"
	case "14018":
		return "workbuddy_credits_exhausted"
	}
	return "workbuddy_upstream_error"
}

// RecoverHTTP preserves central OAuth refresh/rotation policy. Known business
// refusals use fixed public codes and never expose private provider text.
// Retryable statuses retain the central refresh, rotation, and fallback policy.
func (*Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if e := ctx.Err(); e != nil {
		return inference.RecoveryDecision{}, e
	}
	code := ErrorCode(f.Status, f.Body)
	if code != "workbuddy_upstream_error" {
		action := inference.RecoveryStop
		if f.Status == 401 || f.Status == 429 || f.Status >= 500 {
			action = inference.RecoveryDefault
		}
		return inference.RecoveryDecision{Action: action, Error: &inference.Error{Status: f.Status, Code: code}}, nil
	}
	return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
}
