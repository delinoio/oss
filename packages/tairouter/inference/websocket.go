// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Socket must support one concurrent reader and writer, bounded reads, and context
// cancellation. The host owns HTTP upgrade, origin policy, and selecting public subprotocols.
type Socket interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
	Close() error
}

// ServeSocket bridges Responses response.create frames to the same HTTP execution path.
// Every turn independently enters authoritative admission. Disconnect cancels the active send.
func (h *Handler) ServeSocket(ctx context.Context, token string, s Socket) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.Close()
	type frame struct {
		data []byte
		err  error
	}
	frames := make(chan frame, 1)
	go func() {
		for {
			b, e := s.Read(ctx)
			select {
			case frames <- frame{b, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	var mu sync.Mutex
	write := func(b []byte) error { mu.Lock(); defer mu.Unlock(); return s.Write(ctx, b) }
	sendError := func(code string) error {
		b, _ := json.Marshal(object{"type": "error", "error": object{"type": code, "code": code, "message": code}})
		return write(b)
	}
	var turnCancel context.CancelFunc
	var done <-chan struct{}
	defer func() {
		cancel()
		if turnCancel != nil {
			turnCancel()
		}
		s.Close()
		if done != nil {
			<-done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			done = nil
			turnCancel = nil
		case f := <-frames:
			if f.err != nil {
				cancel()
				return f.err
			}
			if len(f.data) > int(h.c.MaxRequestBytes) {
				_ = sendError("request_too_large")
				continue
			}
			b, e := decode(f.data)
			if e != nil {
				_ = sendError("invalid_json")
				continue
			}
			switch str(b["type"]) {
			case "response.cancel":
				if turnCancel != nil {
					turnCancel()
				}
				continue
			case "response.create":
			default:
				_ = sendError("unsupported_event")
				continue
			}
			// A finished worker can race with the queued create in the outer select.
			// Observe its final signal here too, after ServeHTTP and release finish.
			if responseWorkerActive(&done, &turnCancel) {
				_ = sendError("response_in_progress")
				continue
			}
			delete(b, "type")
			b["stream"] = true
			if b["generate"] == false {
				p, release, e := h.c.Authorizer.Admit(ctx, token)
				if release != nil {
					release()
				}
				if e != nil || p.OrganizationID == "" || p.UserID == "" {
					_ = sendError("unauthorized")
					continue
				}
				out := object{"id": "", "object": "response", "created_at": time.Now().Unix(), "model": str(b["model"]), "output": []any{}}
				for sequence, name := range []string{"response.created", "response.completed"} {
					status := "in_progress"
					if sequence == 1 {
						status = "completed"
					}
					out["status"] = status
					raw, _ := json.Marshal(object{"type": name, "sequence_number": sequence, "response": out})
					if e = write(raw); e != nil {
						return e
					}
				}
				continue
			}
			delete(b, "generate")
			raw, _ := json.Marshal(b)
			turnCtx, tc := context.WithCancel(ctx)
			turnCancel = tc
			finished := make(chan struct{})
			done = finished
			go func() {
				defer close(finished)
				defer tc()
				req, _ := http.NewRequestWithContext(turnCtx, "POST", "http://inference/v1/responses", bytes.NewReader(raw))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				w := &socketWriter{header: make(http.Header), write: write}
				h.ServeHTTP(w, req)
				if e := w.finish(); e != nil {
					cancel()
				}
			}()
		}
	}
}

func responseWorkerActive(done *<-chan struct{}, turnCancel *context.CancelFunc) bool {
	if *done == nil {
		return false
	}
	select {
	case <-*done:
		*done = nil
		*turnCancel = nil
		return false
	default:
		return true
	}
}

type socketWriter struct {
	header http.Header
	status int
	buf    []byte
	write  func([]byte) error
	err    error
}

func (w *socketWriter) Header() http.Header { return w.header }
func (w *socketWriter) WriteHeader(s int)   { w.status = s }
func (w *socketWriter) Flush()              {}
func (w *socketWriter) Write(b []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(w.buf)+len(b) > 4<<20 {
		return 0, &Error{502, "stream_event_too_large", 0}
	}
	w.buf = append(w.buf, b...)
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		for {
			idx := bytes.Index(w.buf, []byte("\n\n"))
			if idx < 0 {
				break
			}
			event := append([]byte{}, w.buf[:idx+2]...)
			w.buf = w.buf[idx+2:]
			e := readSSE(bytes.NewReader(event), func(_ string, data []byte) error {
				if string(data) == "[DONE]" {
					return nil
				}
				return w.write(data)
			})
			if e != nil {
				w.err = e
				return 0, e
			}
		}
	}
	return len(b), nil
}
func (w *socketWriter) finish() error {
	if w.err != nil {
		return w.err
	}
	if len(w.buf) == 0 {
		return nil
	}
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		return io.ErrUnexpectedEOF
	}
	m, e := decode(w.buf)
	if e != nil {
		return e
	}
	m["type"] = "error"
	b, _ := json.Marshal(m)
	return w.write(b)
}
