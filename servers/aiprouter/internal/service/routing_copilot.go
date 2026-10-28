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
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
)

const copilotSessionHeader = "Copilot-Session-Token"

type copilotJSONSender func(context.Context, string, any, string) (map[string]any, error)

// negotiateCopilot uses the captured account version for every negotiation write.
// The caller must keep the logical target for retries and use the returned target
// for serialization. Refresh and rotation must call this again; session headers
// are never copied from the previous attempt. No prompt or session is persisted.
func (a *inferenceAdapter) negotiateCopilot(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, body json.RawMessage, policy inference.DestinationPolicy) (inference.Target, inference.Credential, time.Time, error) {
	d, version, err := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return t, c, time.Time{}, inferenceError(err)
	}
	if d.Metadata.ProviderId != "github-copilot" {
		return t, c, time.Time{}, nil
	}
	current, err := credentialFor(ctx, d, t)
	if err != nil || current.Secret != c.Secret {
		return t, c, time.Time{}, inference.ErrNoCredential
	}
	if _, ok := modelEvidence(d, t.Model, time.Now()); !ok {
		return t, c, time.Time{}, inference.ErrNoCredential
	}
	if !copilotOrigin(t.BaseURL) {
		return t, c, time.Time{}, inference.ErrDestination
	}
	lease, err := a.current(p)
	if err != nil {
		return t, c, time.Time{}, err
	}
	c.Headers = current.Headers.Clone()
	t.Headers = t.Headers.Clone()
	t.Headers.Del(copilotSessionHeader)
	c.Headers.Del(copilotSessionHeader)
	gate := func(ctx context.Context, start func(context.Context) error) error {
		return inferenceError(a.s.WithAccountDispatch(ctx, lease, c.AccountID, version, start))
	}
	send := func(ctx context.Context, path string, body any, session string) (map[string]any, error) {
		return copilotRequest(ctx, policy, gate, t.BaseURL, c, path, body, session)
	}
	next, credential, expires, err := resolveCopilotAuto(ctx, t, c, body, send)
	if err != nil {
		return t, c, time.Time{}, err
	}
	// A completed response cannot authorize a changed/disabled account.
	_, latest, err := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return t, c, time.Time{}, inferenceError(err)
	}
	if latest != version {
		return t, c, time.Time{}, inference.ErrNoCredential
	}
	return next, credential, expires, nil
}

func copilotOrigin(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && strings.HasSuffix(u.Hostname(), ".githubcopilot.com") && (u.Path == "" || u.Path == "/") && !u.ForceQuery && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(raw, "?#")
}

func copilotRequest(ctx context.Context, policy inference.DestinationPolicy, gate inference.SendGate, base string, c inference.Credential, path string, body any, session string) (map[string]any, error) {
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var raw []byte
	method := http.MethodGet
	if body != nil {
		raw, _ = json.Marshal(body)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(bounded, method, strings.TrimRight(base, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return nil, copilotFailure()
	}
	req.Header = c.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for _, k := range []string{copilotSessionHeader, "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "ChatGPT-Account-Id"} {
		req.Header.Del(k)
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2026-08-01")
	for k, v := range map[string]string{"Editor-Version": "opencodex/0.1.0", "Editor-Plugin-Version": "opencodex/0.1.0", "Copilot-Integration-Id": "vscode-chat"} {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	if session != "" {
		req.Header.Set(copilotSessionHeader, session)
	}
	resp, err := policy.Do(bounded, req, gate, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var known *inference.Error
		if errors.As(err, &known) {
			return nil, known
		}
		return nil, copilotFailure()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		if status != 401 && status != 403 && status != 429 {
			status = 502
		}
		delay := time.Duration(0)
		value := resp.Header.Get("Retry-After")
		if len(value) > 0 && len(value) <= 4 && strings.Trim(value, "0123456789") == "" {
			n, _ := strconv.Atoi(value)
			if n < 1 {
				n = 1
			}
			if n > 3600 {
				n = 3600
			}
			delay = time.Duration(n) * time.Second
		}
		return nil, &inference.Error{Status: status, Code: "copilot_auto_negotiation_failed", RetryAfter: delay}
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return nil, copilotFailure()
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil || out == nil {
		return nil, copilotFailure()
	}
	return out, nil
}
func copilotFailure() error {
	return &inference.Error{Status: 502, Code: "copilot_auto_negotiation_failed"}
}
func utf16Bound(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) > n {
		u = u[:n]
	}
	return string(utf16.Decode(u))
}
func copilotStrings(v any) []string {
	rows, ok := v.([]any)
	if !ok || len(rows) > 512 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		s, ok := r.(string)
		if !ok || s == "" || len(utf16.Encode([]rune(s))) > 256 {
			return nil
		}
		out = append(out, s)
	}
	return out
}
func copilotLastUser(raw json.RawMessage) string {
	var b map[string]any
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	input := b["input"]
	if input == nil {
		input = b["messages"]
	}
	if s, ok := input.(string); ok {
		return utf16Bound(s, 32768)
	}
	rows, _ := input.([]any)
	for i := len(rows) - 1; i >= 0; i-- {
		m, _ := rows[i].(map[string]any)
		if m["role"] != "user" {
			continue
		}
		return utf16Bound(routingText(m["content"]), 32768)
	}
	return ""
}
func routingText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	rows, _ := v.([]any)
	var parts []string
	for _, v := range rows {
		m, _ := v.(map[string]any)
		if m["type"] == "text" || m["type"] == "input_text" || m["type"] == "output_text" {
			if s, ok := m["text"].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}
func resolveCopilotAuto(ctx context.Context, t inference.Target, c inference.Credential, body json.RawMessage, send copilotJSONSender) (inference.Target, inference.Credential, time.Time, error) {
	t.Headers = t.Headers.Clone()
	c.Headers = c.Headers.Clone()
	if c.Headers == nil {
		c.Headers = make(http.Header)
	}
	for k, v := range map[string]string{"Editor-Version": "opencodex/0.1.0", "Editor-Plugin-Version": "opencodex/0.1.0", "Copilot-Integration-Id": "vscode-chat", "X-GitHub-Api-Version": "2026-08-01"} {
		if c.Headers.Get(k) == "" {
			c.Headers.Set(k, v)
		}
	}
	t.Headers.Del(copilotSessionHeader)
	c.Headers.Del(copilotSessionHeader)
	failed := func(e error) (inference.Target, inference.Credential, time.Time, error) { return t, c, time.Time{}, e }
	catalog, err := send(ctx, "/models", nil, "")
	if err != nil {
		if ctx.Err() != nil {
			return failed(ctx.Err())
		}
		// Discovery is additive for named legacy routes. Authorization/budget failures
		// must still fail closed even when a catalog outage would preserve the route.
		var e *inference.Error
		if errors.As(err, &e) && e.Code != "copilot_auto_negotiation_failed" {
			return failed(err)
		}
		return t, c, time.Time{}, nil
	}
	rows, ok := catalog["data"].([]any)
	if !ok || len(rows) > 512 {
		return t, c, time.Time{}, nil
	}
	models := map[string]map[string]any{}
	auto := true
	for _, v := range rows {
		r, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id, ok := r["id"].(string)
		if !ok || id == "" || len(utf16.Encode([]rune(id))) > 256 {
			continue
		}
		models[id] = r
		if enabled, ok := r["model_picker_enabled"].(bool); !ok || enabled {
			auto = false
		}
	}
	if len(models) == 0 || !auto {
		return t, c, time.Time{}, nil
	}
	session, err := send(ctx, "/models/session", map[string]any{"auto_mode": map[string]any{"model_hints": []string{"auto"}}}, "")
	if err != nil {
		return failed(err)
	}
	pool := copilotStrings(session["available_models"])
	token, _ := session["session_token"].(string)
	if token == "" || len(token) > 32768 || len(pool) == 0 {
		return failed(copilotFailure())
	}
	for _, v := range token {
		if v < 33 || v > 126 {
			return failed(copilotFailure())
		}
	}
	expires := time.Now().Add(time.Minute)
	if raw, exists := session["expires_at"]; exists {
		n, ok := raw.(float64)
		if !ok || n <= float64(time.Now().Unix())+1 || n > 1e12 {
			return failed(copilotFailure())
		}
		when := time.UnixMilli(int64(n * 1000))
		if when.Before(expires) {
			expires = when
		}
	}
	intent, err := send(ctx, "/models/session/intent", map[string]any{"prompt": copilotLastUser(body), "available_models": pool}, token)
	if err != nil {
		return failed(err)
	}
	chosen := ""
	for _, candidate := range copilotStrings(intent["candidate_models"]) {
		for _, allowed := range pool {
			if candidate == allowed {
				chosen = candidate
				break
			}
		}
		if chosen != "" {
			break
		}
	}
	if chosen == "" || time.Until(expires) <= time.Second {
		return failed(copilotFailure())
	}
	endpoints := copilotStrings(models[chosen]["supported_endpoints"])
	wire := inference.Wire("")
	for _, endpoint := range endpoints {
		if endpoint == "/responses" {
			wire = inference.Responses
			break
		}
		if endpoint == "/chat/completions" {
			wire = inference.Chat
		}
	}
	if wire == "" {
		return failed(copilotFailure())
	}
	t.Model = chosen
	t.Wire = wire
	// The catalog chose the concrete wire. A logical target's pre-negotiation
	// endpoint must not override that choice during standard-adapter execution.
	path := "/chat/completions"
	if wire == inference.Responses {
		path = "/responses"
	}
	t.Endpoint = strings.TrimRight(t.BaseURL, "/") + path
	if t.Policy != nil {
		selected := *t.Policy
		selected.Model = chosen
		selected.Wire = providers.Wire(wire)
		selected.Endpoint = t.Endpoint
		t.Policy = &selected
	}
	if c.Headers == nil {
		c.Headers = make(http.Header)
	}
	c.Headers.Set(copilotSessionHeader, token)
	c.Headers.Set("X-GitHub-Api-Version", "2026-08-01")
	return t, c, expires, nil
}

var _ inference.Negotiator = (*inferenceAdapter)(nil)

func (a *inferenceAdapter) Negotiate(ctx context.Context, p inference.Principal, target inference.Target, credential inference.Credential, body json.RawMessage, policy inference.DestinationPolicy) (inference.Target, inference.Credential, time.Time, error) {
	next, selected, err := a.negotiateVertexProject(ctx, p, target, credential, policy)
	if err != nil {
		return target, credential, time.Time{}, err
	}
	return a.negotiateCopilot(ctx, p, next, selected, body, policy)
}
