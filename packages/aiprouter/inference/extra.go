// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	Transcription  Operation = "audio/transcriptions"
	LiveCall       Operation = "live"
	RealtimeCall   Operation = "realtime/calls"
	ContextHistory Operation = "context"
	Catalog        Operation = "catalog"
	HubState       Operation = "hub-state"
	UserUsage      Operation = "usage"
)

// ClientViews returns already-redacted, organization/user/key-scoped compatibility views.
// It must not return provider secrets, other users' metadata, or cached authorization.
type ClientViews interface {
	Read(context.Context, Principal, Operation, url.Values) (json.RawMessage, error)
}

// ContextBinding proves the exact account that owns a client context session.
// Resolver must check user's protocol opt-in, ownership, and credential generation on
// every resolution. Missing/ambiguous ownership is 409, never a fresh pool selection.
type ContextBinding struct {
	Target     Target
	Credential Credential
	Receipt    string
}
type ContextResolver interface {
	ResolveContext(context.Context, Principal, string) (ContextBinding, error)
	ValidateContext(context.Context, Principal, string, ContextBinding) error
}

var contextPaths = map[string]bool{"alpha/history/v2/list_windows": true, "alpha/history/v2/list_items": true, "alpha/history/v2/read_item": true, "alpha/history/v2/search_contents": true, "alpha/notes/v2/thread_hint": true, "alpha/notes/v2/list_files_by_prefix": true, "alpha/notes/v2/read_file": true, "alpha/notes/v2/search_contents": true, "alpha/notes/v2/append_to_file": true, "alpha/notes/v2/write_file": true}
var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,512}$`)

func (h *Handler) serveExtra(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	isContext := contextPaths[path]
	isAudio := path == string(Transcription)
	view := Operation(path)
	isView := view == Catalog || view == HubState || view == UserUsage
	isLive := path == string(LiveCall) || path == string(RealtimeCall)
	if !isContext && !isAudio && !isView && !isLive {
		return false
	}
	if isView && r.Method != "GET" && (r.Method != "HEAD" || view == UserUsage) || !isView && r.Method != "POST" {
		writeError(w, &Error{405, "method_not_allowed", 0})
		return true
	}
	timeout := 120 * time.Second
	if isContext {
		timeout = 35 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	r = r.WithContext(ctx)
	// Close uploads on deadline as net/http request Body does not take a context argument.
	stop := context.AfterFunc(ctx, func() { r.Body.Close() })
	defer stop()
	p, release, e := h.c.Authorizer.Admit(ctx, bearer(r))
	if e != nil {
		writeError(w, e)
		return true
	}
	if release != nil {
		defer release()
	}
	if p.OrganizationID == "" || p.UserID == "" || p.KeyID == "" {
		writeError(w, ErrUnauthorized)
		return true
	}
	if isView {
		if h.c.Views == nil {
			writeError(w, ErrUnsupported)
			return true
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, &Error{400, "invalid_query", 0})
			return true
		}
		allowed := map[string]bool{"range": true, "surface": true, "provider": true, "model": true, "since": true, "until": true}
		for k, v := range query {
			if view != UserUsage || !allowed[k] || len(v) != 1 {
				writeError(w, &Error{400, "invalid_query", 0})
				return true
			}
		}
		b, err := h.c.Views.Read(ctx, p, view, query)
		if err != nil {
			writeError(w, err)
			return true
		}
		limit, code := 16<<20, "view_too_large"
		if view == Catalog {
			limit, code = 256<<20, "catalog_too_large"
		}
		if len(b) > limit {
			writeError(w, &Error{507, code, 0})
			return true
		}
		if !json.Valid(b) {
			writeError(w, ErrUnavailable)
			return true
		}
		if err = h.guard(ctx, p); err != nil {
			writeError(w, err)
			return true
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if r.Method != "HEAD" {
			w.Write(b)
		}
		return true
	}
	rec := UsageRecord{RequestID: newID()}
	w.Header().Set("X-Request-ID", rec.RequestID)
	defer func() {
		rec.Canceled = ctx.Err() != nil
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := h.c.Usage.Record(c, p, rec); e != nil {
			h.event(c, "usage_write_failed")
			h.c.Logger.ErrorContext(c, "inference usage write failed", "request_id", rec.RequestID)
		}
	}()
	switch {
	case isContext:
		e = h.contextRelay(ctx, w, r, p, path, &rec)
	case isAudio:
		e = h.transcribe(ctx, w, r, p, &rec)
	case isLive:
		e = h.liveCall(ctx, w, r, p, Operation(path), &rec)
	}
	if e != nil {
		rec.Status = publicError(e).Status
		writeError(w, e)
	}
	return true
}
func boundedUpload(r *http.Request, max int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r.Body, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, &Error{413, "request_too_large", 0}
	}
	return b, nil
}
func (h *Handler) contextRelay(ctx context.Context, w http.ResponseWriter, r *http.Request, p Principal, path string, rec *UsageRecord) error {
	if h.c.Contexts == nil {
		return &Error{409, "context_account_unavailable", 0}
	}
	data, e := h.readRequest(r, 16<<20)
	if e != nil {
		return e
	}
	b, e := decode(data)
	if e != nil {
		return e
	}
	session := str(obj(b["context"])["session_id"])
	if !sessionPattern.MatchString(session) {
		return &Error{400, "invalid_context_session", 0}
	}
	for _, k := range []string{"x-codex-parent-thread-id", "session-id"} {
		if v := r.Header.Get(k); v != "" && v != session {
			return &Error{409, "context_account_unavailable", 0}
		}
	}
	binding, e := h.c.Contexts.ResolveContext(ctx, p, session)
	if e != nil {
		return e
	}
	if binding.Receipt == "" || binding.Credential.AccountID == "" {
		return &Error{409, "context_account_unavailable", 0}
	}
	headers := http.Header{"Content-Type": []string{"application/json"}}
	for _, k := range []string{"x-openai-encrypted-tool-arguments", "x-openai-tool-output-truncation-policy"} {
		if v := r.Header.Get(k); len(v) <= 8192 {
			headers.Set(k, v)
		}
	}
	if e = h.c.Contexts.ValidateContext(ctx, p, session, binding); e != nil {
		return e
	}
	resp, e := h.sendOnce(ctx, p, binding.Target, binding.Credential, "/"+path, headers, data, rec)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	body, e := readBody(resp.Body, 16<<20, 35*time.Second, 0, func() {})
	if e != nil {
		return e
	}
	if !json.Valid(body) {
		return &Error{502, "invalid_upstream_json", 0}
	}
	for _, k := range []string{"x-openai-encrypted-tool-arguments", "x-openai-tool-output-truncation-policy"} {
		if v := resp.Header.Get(k); len(v) <= 8192 {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, e = w.Write(body)
	return e
}
func (h *Handler) selectSingle(ctx context.Context, p Principal, model string, op Operation) (Target, Credential, error) {
	route, e := h.c.Router.Resolve(ctx, p, model, op)
	if e != nil {
		return Target{}, Credential{}, e
	}
	if len(route.Targets) == 0 || route.MaxSends < 1 {
		return Target{}, Credential{}, ErrNoCredential
	}
	t := route.Targets[0]
	if e = h.guard(ctx, p); e != nil {
		return t, Credential{}, e
	}
	c, e := h.c.Credentials.Select(ctx, p, t, "")
	return t, c, e
}
func (h *Handler) sendOnce(ctx context.Context, p Principal, t Target, c Credential, path string, headers http.Header, body []byte, rec *UsageRecord) (*http.Response, error) {
	client, _, e := h.c.Destinations.client(t.BaseURL)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(t.BaseURL, "/")+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	for k, v := range t.Headers {
		req.Header[k] = append([]string{}, v...)
	}
	for k, v := range c.Headers {
		req.Header[k] = append([]string{}, v...)
	}
	for k, v := range headers {
		req.Header[k] = append([]string{}, v...)
	}
	if c.Secret == "" && t.Auth != Anonymous {
		return nil, ErrNoCredential
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	sent := gateClient(client, ctx, p, h.c.Authorizer, func() { rec.Sends++; h.event(ctx, "upstream_send") })
	rec.Provider = t.Provider
	rec.Model = t.Model
	rec.AccountID = c.AccountID
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if sent.Load() {
			return nil, ErrAmbiguous
		}
		return nil, e
	}
	rec.Status = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return nil, ErrDestination
		}
		return nil, &Error{resp.StatusCode, "upstream_refused", retryWait(resp.Header.Get("Retry-After"), 0)}
	}
	return resp, nil
}

type transcriptionInput struct {
	file                                      []byte
	filename, model, prompt, language, format string
}

func parseTranscription(r *http.Request) (transcriptionInput, error) {
	var out transcriptionInput
	out.format = "json"
	bad := &Error{400, "invalid_transcription", 0}
	if e := r.Header.Get("Content-Encoding"); e != "" && e != "identity" {
		return out, bad
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "multipart/form-data" {
		return out, bad
	}
	data, e := boundedUpload(r, 32<<20)
	if e != nil {
		return out, e
	}
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	seen := map[string]bool{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, bad
		}
		name := part.FormName()
		if seen[name] {
			return out, bad
		}
		seen[name] = true
		value, e := io.ReadAll(part)
		if e != nil {
			return out, bad
		}
		if name == "file" {
			if part.FileName() == "" || len(value) == 0 {
				return out, bad
			}
			if len(value) > 25_000_000 {
				return out, &Error{413, "audio_file_too_large", 0}
			}
			out.file = value
			out.filename = "audio"
			continue
		}
		if part.FileName() != "" || len(value) > 16<<10 {
			return out, bad
		}
		switch name {
		case "model":
			out.model = string(value)
		case "prompt":
			out.prompt = string(value)
		case "language":
			out.language = string(value)
		case "response_format":
			out.format = string(value)
		default:
			return out, bad
		}
	}
	if len(out.file) == 0 || (out.model != "gpt-4o-transcribe" && out.model != "gpt-4o-mini-transcribe" && out.model != "whisper-1") || (out.format != "json" && out.format != "text") {
		return out, bad
	}
	if out.language != "" && !regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})?$`).MatchString(out.language) {
		return out, bad
	}
	return out, nil
}
func (h *Handler) transcribe(ctx context.Context, w http.ResponseWriter, r *http.Request, p Principal, rec *UsageRecord) error {
	input, e := parseTranscription(r)
	if e != nil {
		return e
	}
	t, c, e := h.selectSingle(ctx, p, input.model, Transcription)
	if e != nil {
		return e
	}
	path := "/audio/transcriptions"
	if t.Auth == OAuth {
		if t.Provider != "openai" && t.Provider != "chatgpt" {
			return ErrUnsupported
		}
		if input.model != "gpt-4o-transcribe" {
			return &Error{400, "unsupported_transcription_model", 0}
		}
		u, e := url.Parse(t.BaseURL)
		if e != nil {
			return ErrDestination
		}
		u.Path = "/backend-api"
		t.BaseURL = u.String()
		path = "/transcribe"
	}
	var b bytes.Buffer
	form := multipart.NewWriter(&b)
	part, _ := form.CreateFormFile("file", input.filename)
	part.Write(input.file)
	if input.prompt != "" {
		form.WriteField("prompt", input.prompt)
	}
	if input.language != "" {
		form.WriteField("language", input.language)
	}
	if t.Auth != OAuth {
		form.WriteField("model", input.model)
		form.WriteField("response_format", "json")
	}
	form.Close()
	resp, e := h.sendOnce(ctx, p, t, c, path, http.Header{"Content-Type": []string{form.FormDataContentType()}}, b.Bytes(), rec)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, e := readBody(resp.Body, 2<<20, 120*time.Second, 0, func() {})
	if e != nil {
		return e
	}
	out, e := decode(data)
	if e != nil {
		return &Error{502, "invalid_transcription_result", 0}
	}
	text, ok := out["text"].(string)
	if !ok {
		return &Error{502, "invalid_transcription_result", 0}
	}
	if input.format == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, e = w.Write([]byte(text))
		return e
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(object{"text": text})
}
