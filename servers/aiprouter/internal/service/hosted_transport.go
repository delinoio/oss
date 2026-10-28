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
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
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
	if r.AttemptTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.AttemptTimeout)
		defer cancel()
	}
	req, e := http.NewRequestWithContext(ctx, r.Method, r.URL, bytes.NewReader(r.Body))
	if e != nil {
		return nil, e
	}
	req.Header = r.Headers.Clone()
	b, h, e := t.exchange(ctx, req, 32<<20)
	if e != nil {
		return nil, e
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(b))}, nil
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
