// SPDX-License-Identifier: Apache-2.0
// Zed protocol behavior is adapted from opencodex; see LICENSE.upstream.
package zed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type Provider string

const (
	Anthropic       Provider       = "anthropic"
	OpenAI          Provider       = "open_ai"
	Google          Provider       = "google"
	XAI             Provider       = "x_ai"
	WireAnthropic   inference.Wire = "zed-anthropic"
	WireOpenAI      inference.Wire = "zed-openai"
	WireGoogle      inference.Wire = "zed-google"
	WireXAI         inference.Wire = "zed-xai"
	MaxRequestBytes                = 32 << 20
	MaxFrameBytes                  = 1 << 20
)

// Adapter is immutable. No request credentials or response state are shared.
type Adapter struct{ provider Provider }

var _ inference.Adapter = (*Adapter)(nil)

func New(provider Provider) (*Adapter, error) {
	switch provider {
	case Anthropic, OpenAI, Google, XAI:
		return &Adapter{provider: provider}, nil
	default:
		return nil, inference.ErrUnsupported
	}
}

// NormalizeProvider uses the catalog family first, then the pinned model heuristic.
func NormalizeProvider(value, model string) Provider {
	switch strings.ToLower(value) {
	case "anthropic":
		return Anthropic
	case "openai", "open_ai":
		return OpenAI
	case "google", "gemini":
		return Google
	case "xai", "x_ai", "x-ai":
		return XAI
	}
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude"):
		return Anthropic
	case strings.Contains(m, "gemini"):
		return Google
	case strings.Contains(m, "grok"), strings.Contains(m, "xai"):
		return XAI
	default:
		return OpenAI
	}
}

// NeedsTokenRefresh is a signal for the central executor, never permission to replay.
func NeedsTokenRefresh(r *http.Response) bool {
	if r == nil {
		return false
	}
	if r.StatusCode == http.StatusUnauthorized {
		return true
	}
	for k := range r.Header {
		if strings.EqualFold(k, "x-zed-expired-token") || strings.EqualFold(k, "x-zed-outdated-token") {
			return true
		}
	}
	return false
}

func (a *Adapter) responseWire() inference.Wire {
	switch a.provider {
	case Anthropic:
		return inference.Anthropic
	case OpenAI:
		return inference.Responses
	default:
		return inference.Chat
	}
}

func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if err := ctx.Err(); err != nil {
		return inference.AdapterRequest{}, err
	}
	var optionsErr error
	ctx, optionsErr = optionsForInput(ctx, in)
	if optionsErr != nil {
		return inference.AdapterRequest{}, optionsErr
	}
	if in.Operation != inference.Generate {
		return inference.AdapterRequest{}, inference.ErrUnsupported
	}
	if len(in.Body) > MaxRequestBytes {
		return inference.AdapterRequest{}, requestError("zed_request_too_large")
	}
	body, err := decodeObject(in.Body)
	if err != nil {
		return inference.AdapterRequest{}, requestError("invalid_json")
	}
	if strings.TrimSpace(in.Credential.Secret) == "" {
		return inference.AdapterRequest{}, inference.ErrNoCredential
	}
	u, err := url.Parse(in.Target.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return inference.AdapterRequest{}, inference.ErrDestination
	}
	model := in.Target.Model
	if model == "" {
		model = stringOf(body["model"])
	}
	if model == "" {
		return inference.AdapterRequest{}, requestError("missing_model")
	}
	body["model"] = model
	for i, v := range arrayOf(body["tools"]) {
		t := objectOf(v)
		if t["type"] == "custom" && objectOf(t["custom"]) != nil {
			c := objectOf(t["custom"])
			c["type"] = "custom"
			arrayOf(body["tools"])[i] = c
		}
	}
	state, _ := ctx.Value(stateKey{}).(*requestState)
	if state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.built {
			return inference.AdapterRequest{}, requestError("zed_request_context_reused")
		}
	}
	aliases, custom := map[string]string{}, map[string]bool{}
	if a.provider != OpenAI {
		if err := flattenToolGroups(body); err != nil {
			return inference.AdapterRequest{}, err
		}
		aliases, custom = aliasTools(body)
		if len(aliases) > 0 && state == nil {
			return inference.AdapterRequest{}, requestError("zed_request_context_required")
		}
	}
	// A stored continuation needs central owner-scoped expansion before reaching Zed.
	if stringOf(body["previous_response_id"]) != "" {
		return inference.AdapterRequest{}, inference.ErrContinuation
	}
	request, err := buildProviderRequest(body, in.Wire, a.provider)
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	if a.provider != Google {
		request["model"] = model
		request["stream"] = true
	}
	if a.provider == Google {
		applyGooglePolicy(request, body, model, requestOptions(ctx))
	}
	thread, err := randomID()
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	if trusted := requestOptions(ctx).ThreadID; trusted != "" {
		if len(trusted) > 512 || strings.ContainsAny(trusted, "\x00\r\n") {
			return inference.AdapterRequest{}, requestError("invalid_zed_thread_id")
		}
		thread = trusted
	}
	prompt, err := randomID()
	if err != nil {
		return inference.AdapterRequest{}, err
	}
	data, err := json.Marshal(object{"thread_id": thread, "prompt_id": prompt, "provider": a.provider, "model": model, "provider_request": request})
	if err != nil {
		return inference.AdapterRequest{}, requestError("invalid_json")
	}
	if len(data) > MaxRequestBytes {
		return inference.AdapterRequest{}, requestError("zed_request_too_large")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/completions"
	u.RawPath = ""
	h := make(http.Header)
	h[http.CanonicalHeaderKey(PolicyHeader)] = nil
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/x-ndjson, text/event-stream, */*")
	h.Set("User-Agent", "OpenCodex/zed")
	h.Set("x-zed-version", "0.200.0")
	h.Set("x-zed-client-supports-status-messages", "true")
	h.Set("x-zed-client-supports-stream-ended-request-completion-status", "true")
	// Secret is the short-lived LLM token supplied by the central credential service.
	h.Set("Authorization", "Bearer "+in.Credential.Secret)
	if state != nil {
		state.aliases = aliases
		state.custom = custom
		state.built = true
		state.threadID = thread
		state.binding = inputBinding(in)
		state.showThinking = objectOf(objectOf(request["generationConfig"])["thinkingConfig"])["includeThoughts"] == true
	}
	return inference.AdapterRequest{URL: u.String(), Headers: h, Body: data, ResponseWire: a.responseWire()}, nil
}

func (a *Adapter) Decode(ctx context.Context, response *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, &inference.Error{Status: 502, Code: "zed_missing_body"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}
	if NeedsTokenRefresh(response) {
		return nil, &inference.Error{Status: 502, Code: "zed_token_refresh_required"}
	}
	out := new(http.Response)
	*out = *response
	out.Header = response.Header.Clone()
	out.Header.Set("Content-Type", "text/event-stream")
	out.Header.Del("Content-Length")
	out.Header.Del("Content-Encoding")
	out.ContentLength = -1
	s := newEventStream(ctx, response.Body, a.provider)
	s.google.showThinking = requestOptions(ctx).ShowThinkingSummaries
	if state, _ := ctx.Value(stateKey{}).(*requestState); state != nil {
		state.mu.Lock()
		s.aliases = state.aliases
		s.custom = state.custom
		s.google.showThinking = state.showThinking
		state.mu.Unlock()
	}
	out.Body = s
	return out, nil
}

type object = map[string]any

func objectOf(v any) object          { m, _ := v.(map[string]any); return m }
func arrayOf(v any) []any            { a, _ := v.([]any); return a }
func stringOf(v any) string          { s, _ := v.(string); return s }
func requestError(code string) error { return &inference.Error{Status: 400, Code: code} }
func decodeObject(b []byte) (object, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m object
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("not an object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return m, nil
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
