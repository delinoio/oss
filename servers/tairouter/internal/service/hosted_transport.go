// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	"github.com/google/uuid"
)

// Each helper owns an immutable account fence. It must never change the parent
// inference adapter's active account while the parent response is in progress.
type hostedTransport struct {
	mu                    sync.Mutex
	completed             int
	lastOutcome           store.RequestOutcome
	policy                inference.DestinationPolicy
	gate                  inference.SendGate
	record                func(context.Context, string, store.Usage) error
	price                 func(*store.Usage, inference.Usage)
	provider, model, base string
}

func hostedRead(body io.ReadCloser, limit int64) ([]byte, error) {
	defer body.Close()
	b, e := io.ReadAll(io.LimitReader(body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, &inference.Error{Status: 413, Code: "hosted_response_too_large"}
	}
	return b, nil
}

func (t *hostedTransport) finishExchange(ctx context.Context, id uuid.UUID, u store.Usage, usage inference.Usage) error {
	if t.record == nil {
		return inference.ErrUnavailable
	}
	t.mu.Lock()
	t.completed++
	t.lastOutcome = u.Outcome
	t.mu.Unlock()
	if t.price != nil {
		t.price(&u, usage)
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := t.record(cleanup, id.String(), u); err != nil {
		return inference.ErrUnavailable
	}
	return nil
}

// hostedBody keeps the dispatched exchange alive until its consumer finishes.
// The extra byte detects oversize responses without handing it to the adapter.
type hostedBody struct {
	mu          sync.Mutex
	once        sync.Once
	finished    atomic.Bool
	readEnd     atomic.Value
	reader      io.Reader
	body        io.ReadCloser
	ctx         context.Context
	cancel      context.CancelFunc
	stop        func() bool
	t           *hostedTransport
	id          uuid.UUID
	u           store.Usage
	limit       int64
	contentType string
	data        []byte
	result      error
}

func (b *hostedBody) finish(outcome store.RequestOutcome, readErr error) error {
	b.once.Do(func() {
		terminalBytes := 0
		if outcome == store.OutcomeUnknown && readErr == nil && b.ctx.Err() == nil {
			b.mu.Lock()
			terminalBytes = hostedCompletedSSELength(b.data, b.contentType)
			b.mu.Unlock()
			if terminalBytes > 0 {
				outcome = store.OutcomeSucceeded
			}
		}
		end := io.ErrClosedPipe
		if outcome == store.OutcomeSucceeded && readErr == nil {
			end = io.EOF
		}
		if readErr != nil {
			end = readErr
		}
		b.readEnd.Store(end)
		b.finished.Store(true)
		if errors.Is(b.ctx.Err(), context.Canceled) {
			outcome = store.OutcomeCanceled
		}
		if b.cancel != nil {
			b.cancel()
		}
		b.body.Close()
		b.mu.Lock()
		b.u.Outcome = outcome
		var usage inference.Usage
		if readErr == nil && outcome == store.OutcomeSucceeded {
			data := b.data
			if terminalBytes > 0 {
				data = data[:terminalBytes]
			}
			usage = hostedReportedUsage(data, b.contentType)
			b.u.InputTokens, b.u.OutputTokens = usage.InputTokens, usage.OutputTokens
		}
		b.mu.Unlock()
		b.result = errors.Join(readErr, b.t.finishExchange(b.ctx, b.id, b.u, usage))
		if b.stop != nil {
			b.stop()
		}
	})
	return b.result
}

func (b *hostedBody) Read(p []byte) (int, error) {
	if b.finished.Load() {
		return 0, b.readEnd.Load().(error)
	}
	n, err := b.reader.Read(p)
	if b.finished.Load() {
		return 0, b.readEnd.Load().(error)
	}
	b.mu.Lock()
	remaining := int(b.limit) - len(b.data)
	if n > remaining {
		n = remaining
		b.data = append(b.data, p[:n]...)
		b.mu.Unlock()
		return n, b.finish(store.OutcomeUnknown, &inference.Error{Status: 413, Code: "hosted_response_too_large"})
	}
	b.data = append(b.data, p[:n]...)
	b.mu.Unlock()
	if err != nil {
		if err == io.EOF {
			if finalErr := b.finish(store.OutcomeSucceeded, nil); finalErr != nil {
				return n, finalErr
			}
			return n, io.EOF
		}
		return n, b.finish(store.OutcomeUnknown, err)
	}
	return n, nil
}

func (b *hostedBody) Close() error { return b.finish(store.OutcomeUnknown, nil) }

// A terminal-aware decoder may close before HTTP EOF. Only complete SSE events
// can prove completion; a partial marker or usage-only body remains unknown.
// Return the terminal prefix so read-ahead after it cannot alter reported usage.
func hostedCompletedSSELength(data []byte, contentType string) int {
	if !strings.Contains(contentType, "text/event-stream") {
		return 0
	}
	var payload []byte
	for offset := 0; offset < len(data); {
		end := bytes.IndexByte(data[offset:], '\n')
		if end < 0 {
			return 0
		}
		end += offset + 1
		line := bytes.TrimSuffix(data[offset:end-1], []byte("\r"))
		offset = end
		if len(line) == 0 {
			raw := bytes.TrimSpace(payload)
			payload = nil
			if len(raw) == 0 {
				continue
			}
			if bytes.Equal(raw, []byte("[DONE]")) {
				return end
			}
			var event map[string]json.RawMessage
			if raw[0] != '{' || json.Unmarshal(raw, &event) != nil {
				return 0
			}
			var typ string
			_ = json.Unmarshal(event["type"], &typ)
			errorValue := event["error"]
			if typ == "error" || typ == "response.failed" || (len(errorValue) > 0 && !bytes.Equal(errorValue, []byte("null"))) {
				return 0
			}
			if typ == "response.completed" || typ == "message_stop" {
				return end
			}
		} else if bytes.HasPrefix(line, []byte("data:")) {
			payload = append(payload, bytes.TrimPrefix(line[5:], []byte(" "))...)
			payload = append(payload, '\n')
		}
	}
	return 0
}

// exchange records every physical exchange independently, including ambiguous
// writes and canceled body reads. Accounting cleanup cannot dispatch more work.
func (t *hostedTransport) exchange(ctx context.Context, req *http.Request, limit int64) (data []byte, header http.Header, err error) {
	id, e := uuid.NewV7()
	if e != nil {
		return nil, nil, e
	}
	sent := false
	u := store.Usage{Provider: t.provider, Model: t.model, Outcome: store.OutcomeUnknown}
	var usage inference.Usage
	defer func() {
		if !sent {
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			u.Outcome = store.OutcomeCanceled
		}
		if t.record == nil {
			err = errors.Join(err, inference.ErrUnavailable)
			return
		}
		t.mu.Lock()
		t.completed++
		t.lastOutcome = u.Outcome
		t.mu.Unlock()
		if t.price != nil {
			t.price(&u, usage)
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := t.record(cleanup, id.String(), u); e != nil {
			err = errors.Join(err, inference.ErrUnavailable)
		}
	}()
	resp, e := t.policy.Do(ctx, req, t.gate, func() { sent = true })
	if e != nil {
		return nil, nil, e
	}
	header = resp.Header.Clone()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		u.Outcome = store.OutcomeUpstreamError
		if resp.StatusCode == 429 {
			u.Outcome = store.OutcomeRateLimited
		}
		return nil, header, &inference.Error{Status: resp.StatusCode, Code: "hosted_upstream_error"}
	}
	data, err = hostedRead(resp.Body, limit)
	if err != nil {
		return nil, header, err
	}
	u.Outcome = store.OutcomeSucceeded
	usage = hostedReportedUsage(data, header.Get("Content-Type"))
	u.InputTokens = usage.InputTokens
	u.OutputTokens = usage.OutputTokens

	return data, header, nil
}

func (t *hostedTransport) Do(ctx context.Context, r inference.AdapterRequest) (*http.Response, error) {
	base, e := url.Parse(t.base)
	if e != nil {
		return nil, inference.ErrDestination
	}
	dest, e := url.Parse(r.URL)
	if e != nil || dest.Scheme != base.Scheme || dest.Host != base.Host || dest.User != nil {
		return nil, inference.ErrDestination
	}
	var cancel context.CancelFunc
	if r.AttemptTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.AttemptTimeout)
	}
	req, e := http.NewRequestWithContext(ctx, r.Method, r.URL, bytes.NewReader(r.Body))
	if e != nil {
		if cancel != nil {
			cancel()
		}
		return nil, e
	}
	req.Header = r.Headers.Clone()
	id, e := uuid.NewV7()
	if e != nil {
		if cancel != nil {
			cancel()
		}
		return nil, e
	}
	sent := false
	u := store.Usage{Provider: t.provider, Model: t.model, Outcome: store.OutcomeUnknown}
	resp, e := t.policy.Do(ctx, req, t.gate, func() { sent = true })
	if e != nil {
		if sent {
			if errors.Is(ctx.Err(), context.Canceled) {
				u.Outcome = store.OutcomeCanceled
			}
			e = errors.Join(e, t.finishExchange(ctx, id, u, inference.Usage{}))
		}
		if cancel != nil {
			cancel()
		}
		return nil, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		u.Outcome = store.OutcomeUpstreamError
		if resp.StatusCode == http.StatusTooManyRequests {
			u.Outcome = store.OutcomeRateLimited
		}
		e = errors.Join(&inference.Error{Status: resp.StatusCode, Code: "hosted_upstream_error"}, t.finishExchange(ctx, id, u, inference.Usage{}))
		if cancel != nil {
			cancel()
		}
		return nil, e
	}
	b := &hostedBody{reader: io.LimitReader(resp.Body, (32<<20)+1), body: resp.Body, ctx: ctx, cancel: cancel, t: t, id: id, u: u, limit: 32 << 20, contentType: resp.Header.Get("Content-Type")}
	b.stop = context.AfterFunc(ctx, func() { b.finish(store.OutcomeCanceled, ctx.Err()) })
	resp.Body = b
	return resp, nil
}
func (t *hostedTransport) json(ctx context.Context, method, path, secret string, body any, limit int64) ([]byte, error) {
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		data = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, t.base+path, data)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	out, _, e := t.exchange(ctx, req, limit)
	return out, e
}

// SSE terminal usage is authoritative. Partial/unterminated streams never invent counts.
func hostedReportedUsage(data []byte, contentType string) inference.Usage {
	var result inference.Usage
	consume := func(raw []byte) {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return
		}
		if nested := obj["response"]; len(nested) > 0 {
			_ = json.Unmarshal(nested, &obj)
		}
		if nested := obj["message"]; len(nested) > 0 {
			_ = json.Unmarshal(nested, &obj)
		}
		var google struct {
			Input  *int64 `json:"promptTokenCount"`
			Output *int64 `json:"candidatesTokenCount"`
			Cache  *int64 `json:"cachedContentTokenCount"`
		}
		if json.Unmarshal(obj["usageMetadata"], &google) == nil {
			result.InputTokens = google.Input
			result.OutputTokens = google.Output
			result.CacheReadTokens = google.Cache
		}
		var u struct {
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			Prompt        *int64 `json:"prompt_tokens"`
			Completion    *int64 `json:"completion_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
			CacheWrite    *int64 `json:"cache_creation_input_tokens"`
			CacheWrite1h  *int64 `json:"cache_creation_1h_input_tokens"`
			CacheCreation struct {
				Long *int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			InputDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			PromptDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		}
		if json.Unmarshal(obj["usage"], &u) == nil {
			if u.Input == nil {
				u.Input = u.Prompt
			}
			if u.Output == nil {
				u.Output = u.Completion
			}
			if u.Input != nil {
				result.InputTokens = u.Input
			}
			if u.Output != nil {
				result.OutputTokens = u.Output
			}
			if u.CacheRead == nil {
				u.CacheRead = u.InputDetails.Cached
			}
			if u.CacheRead == nil {
				u.CacheRead = u.PromptDetails.Cached
			}
			if u.CacheRead != nil {
				result.CacheReadTokens = u.CacheRead
			}
			if u.CacheWrite1h == nil {
				u.CacheWrite1h = u.CacheCreation.Long
			}
			if u.CacheWrite1h != nil {
				result.CacheWrite1hTokens = u.CacheWrite1h
			}
			if u.CacheWrite != nil {
				result.CacheWriteTokens = u.CacheWrite
			}
		}
	}
	if strings.Contains(contentType, "text/event-stream") {
		terminal := false
		for _, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if bytes.Equal(raw, []byte("[DONE]")) {
				terminal = true
				continue
			}
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &event)
			if event.Type == "response.completed" || event.Type == "message_stop" {
				terminal = true
			}
			consume(raw)
		}
		if !terminal {
			return inference.Usage{}
		}
	} else {
		consume(data)
	}
	return result
}
