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
	"strings"
	"time"
)

// LiveBindingStore persists encrypted, expiry-bounded bindings scoped to org/user/key and
// membership generation. Resolve must materialize the exact original account with current
// credential generation; missing/expired aliases must never fall back to a native call ID.
type LiveBinding struct {
	Target                    Target
	AccountID, UpstreamCallID string
	Frameless                 bool
	ExpiresAt                 time.Time
}
type LiveBindingStore interface {
	SaveBinding(context.Context, Principal, string, LiveBinding) error
	ResolveBinding(context.Context, Principal, string) (LiveBinding, Credential, error)
}

// SocketDialer must use the supplied HTTP client for DNS/TLS/redirect/dispatch policy.
// It must not create another client or dial path. Its returned Socket has bounded frames.
type SocketDialer interface {
	Dial(context.Context, string, http.Header, *http.Client) (Socket, error)
}

func parseOffer(r *http.Request) (object, error) {
	data, e := boundedUpload(r, 16<<20)
	if e != nil {
		return nil, e
	}
	ct, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil {
		return nil, &Error{400, "invalid_live_offer", 0}
	}
	var out object
	switch ct {
	case "application/json":
		out, e = decode(data)
	case "application/sdp":
		out = object{"sdp": string(data)}
	case "multipart/form-data":
		out = object{}
		rd := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		for {
			p, err := rd.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, &Error{400, "invalid_live_offer", 0}
			}
			name := p.FormName()
			if _, ok := out[name]; ok {
				return nil, &Error{400, "duplicate_live_field", 0}
			}
			v, err := io.ReadAll(p)
			if err != nil {
				return nil, err
			}
			switch name {
			case "sdp":
				out[name] = string(v)
			case "session":
				var m object
				if json.Unmarshal(v, &m) != nil || m == nil {
					return nil, &Error{400, "invalid_live_session", 0}
				}
				out[name] = m
			default:
				return nil, &Error{400, "unknown_live_field", 0}
			}
		}
	default:
		return nil, &Error{400, "invalid_live_offer", 0}
	}
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(str(out["sdp"])) == "" {
		return nil, &Error{400, "invalid_live_offer", 0}
	}
	if v, ok := out["session"]; ok && obj(v) == nil {
		return nil, &Error{400, "invalid_live_session", 0}
	}
	return out, nil
}

var liveHeaders = []string{"openai-alpha", "x-session-id", "session-id", "thread-id", "originator", "x-oai-attestation", "x-codex-turn-metadata", "voice-session-id"}

func (h *Handler) liveCall(ctx context.Context, w http.ResponseWriter, r *http.Request, p Principal, op Operation, rec *UsageRecord) error {
	if h.c.LiveBindings == nil {
		return &Error{503, "live_binding_store_unavailable", 0}
	}
	offer, e := parseOffer(r)
	if e != nil {
		return e
	}
	session := obj(offer["session"])
	if session == nil && op == LiveCall {
		session = object{"model": "gpt-live-1-codex", "instructions": "", "audio": object{"output": object{"voice": "cove"}}, "delegation": object{"type": "client"}}
	}
	model := str(session["model"])
	if model == "" || model == "gpt-live-1" {
		model = "gpt-live-1-codex"
		if session != nil {
			session["model"] = model
		}
	}
	t, c, e := h.selectSingle(ctx, p, model, op)
	if e != nil {
		return e
	}
	t.Model = model
	var data []byte
	headers := http.Header{}
	for _, k := range liveHeaders {
		if v := r.Header.Get(k); v != "" && len(v) <= 8192 {
			headers.Set(k, v)
		}
	}
	if op == LiveCall && headers.Get("openai-alpha") == "" {
		headers.Set("openai-alpha", "quicksilver=v2")
	}
	path := "/realtime/calls?intent=quicksilver&architecture=avas"
	if t.Auth == OAuth {
		if t.Provider != "openai" && t.Provider != "chatgpt" {
			return ErrUnsupported
		}
		base, err := url.Parse(t.BaseURL)
		if err != nil {
			return ErrDestination
		}
		base.Path = "/backend-api"
		t.BaseURL = base.String()
		offer = object{"sdp": offer["sdp"]}
		if session != nil {
			offer["session"] = session
		}
		data, _ = json.Marshal(offer)
		headers.Set("Content-Type", "application/json")
	} else {
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		form.WriteField("sdp", str(offer["sdp"]))
		if session != nil {
			b, _ := json.Marshal(session)
			form.WriteField("session", string(b))
		}
		form.Close()
		data = buf.Bytes()
		headers.Set("Content-Type", form.FormDataContentType())
		if op == LiveCall {
			path = "/live"
		}
	}
	resp, e := h.sendOnce(ctx, p, t, c, path, headers, data, rec)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	body, e := readBody(resp.Body, 16<<20, 120*time.Second, 0, func() {})
	if e != nil {
		return e
	}
	location, e := url.Parse(resp.Header.Get("Location"))
	if e != nil {
		return &Error{502, "invalid_live_answer", 0}
	}
	call := location.Path[strings.LastIndex(location.Path, "/")+1:]
	if call == "" || !sessionPattern.MatchString(call) || len(body) == 0 {
		return &Error{502, "invalid_live_answer", 0}
	}
	alias := newID()
	binding := LiveBinding{Target: t, AccountID: c.AccountID, UpstreamCallID: call, Frameless: op == LiveCall, ExpiresAt: time.Now().Add(5 * time.Minute)}
	if e = h.guard(ctx, p); e != nil {
		return e
	}
	if e = h.c.LiveBindings.SaveBinding(ctx, p, alias, binding); e != nil {
		return e
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Location", "/v1/"+string(op)+"/"+alias)
	w.WriteHeader(resp.StatusCode)
	_, e = w.Write(body)
	return e
}

// ServeRealtimeSocket relays a single bounded live/dictation session. It never executes
// tool, shell, filesystem or media commands locally. Host owns inbound upgrade and origins.
func (h *Handler) ServeRealtimeSocket(ctx context.Context, token, path string, query url.Values, down Socket) error {
	if h.c.SocketDialer == nil {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	defer down.Close()
	p, release, e := h.c.Authorizer.Admit(ctx, token)
	if e != nil {
		return e
	}
	if release != nil {
		defer release()
	}
	var t Target
	var c Credential
	var suffix string
	call := query.Get("call_id")
	for _, prefix := range []string{"/v1/live/", "/v1/realtime/calls/"} {
		if strings.HasPrefix(path, prefix) {
			call = strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/")
		}
	}
	if call != "" {
		if h.c.LiveBindings == nil {
			return &Error{404, "live_call_unavailable", 0}
		}
		binding, cred, err := h.c.LiveBindings.ResolveBinding(ctx, p, call)
		if err != nil {
			return err
		}
		if !binding.ExpiresAt.After(time.Now()) || binding.AccountID != cred.AccountID {
			return &Error{404, "live_call_unavailable", 0}
		}
		t = binding.Target
		c = cred
		suffix = "/realtime?call_id=" + url.QueryEscape(binding.UpstreamCallID)
		if binding.Frameless {
			suffix = "/live/" + url.PathEscape(binding.UpstreamCallID)
		}
	} else {
		allowed := map[string]bool{"model": true, "intent": true}
		for k, v := range query {
			if !allowed[k] || len(v) != 1 {
				return &Error{400, "invalid_live_query", 0}
			}
		}
		model := query.Get("model")
		if model == "" || model == "gpt-live-1" {
			model = "gpt-live-1-codex"
		}
		op := LiveCall
		if path == "/v1/audio/transcriptions/stream" {
			op = Transcription
			model = "gpt-4o-transcribe"
		} else if path != "/v1/live" && path != "/v1/realtime" {
			return &Error{404, "not_found", 0}
		}
		t, c, e = h.selectSingle(ctx, p, model, op)
		if e != nil {
			return e
		}
		switch path {
		case "/v1/audio/transcriptions/stream":
			if t.Auth != OAuth || (t.Provider != "openai" && t.Provider != "chatgpt") {
				return ErrUnsupported
			}
			u, _ := url.Parse(t.BaseURL)
			u.Path = "/backend-api"
			t.BaseURL = u.String()
			suffix = "/dictation/stream"
		case "/v1/live":
			suffix = "/live?model=" + url.QueryEscape(model)
		default:
			suffix = "/realtime?model=" + url.QueryEscape(model)
			if intent := query.Get("intent"); intent != "" {
				if intent != "quicksilver" {
					return &Error{400, "invalid_live_query", 0}
				}
				suffix += "&intent=quicksilver"
			}
		}
	}
	if t.Auth == OAuth && path != "/v1/audio/transcriptions/stream" {
		if t.Provider != "openai" && t.Provider != "chatgpt" {
			return ErrUnsupported
		}
		base, _ := url.Parse(t.BaseURL)
		if base.Hostname() != "chatgpt.com" {
			return ErrDestination
		}
		t.BaseURL = "https://api.openai.com/v1"
	}
	client, _, e := h.c.Destinations.client(t.BaseURL)
	if e != nil {
		return e
	}
	headers := t.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for k, v := range c.Headers {
		headers[k] = append([]string{}, v...)
	}
	headers.Set("Authorization", "Bearer "+c.Secret)
	dictation := path == "/v1/audio/transcriptions/stream"
	var protocols []string
	if dictation {
		if c.Secret == "" || strings.ContainsAny(c.Secret, " \t\r\n,") {
			return ErrNoCredential
		}
		headers.Del("Authorization")
		protocols = []string{"chatgpt-dictation", "openai-bearer." + c.Secret, "codex-desktop"}
	}
	if strings.HasPrefix(path, "/v1/live") {
		headers.Set("openai-alpha", "quicksilver=v2")
	}
	gateClient(client, ctx, p, h.c.Authorizer, func() { h.event(ctx, "upstream_send") })
	var up Socket
	endpoint := strings.TrimRight(t.BaseURL, "/") + suffix
	if dictation {
		dialer, ok := h.c.SocketDialer.(SubprotocolSocketDialer)
		if !ok {
			return ErrUnsupported
		}
		up, e = dialer.DialProtocols(ctx, endpoint, headers, protocols, client)
	} else {
		up, e = h.c.SocketDialer.Dial(ctx, endpoint, headers, client)
	}
	if e != nil {
		return e
	}
	defer up.Close()
	frames := &dictationFrames{}
	done := make(chan error, 2)
	copyFrames := func(dst, src Socket, fromClient bool) {
		for {
			var b []byte
			var e error
			if framed, ok := src.(FrameSocket); ok && fromClient {
				var text bool
				b, text, e = framed.ReadFrame(ctx)
				if e == nil && !text {
					e = &Error{400, "invalid_frame", 0}
				}
			} else {
				b, e = src.Read(ctx)
			}
			if e != nil {
				done <- e
				return
			}
			limit := 50 << 20
			if dictation && fromClient {
				limit = 64 << 10
			}
			if len(b) > limit {
				done <- &Error{413, "frame_too_large", 0}
				return
			}
			if fromClient {
				if e = h.guard(ctx, p); e != nil {
					done <- e
					return
				}
				if !json.Valid(b) || (dictation && !frames.validate(b)) {
					done <- &Error{400, "invalid_frame", 0}
					return
				}
			}
			if fromClient {
				sendCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				e = h.c.Authorizer.(Dispatcher).Dispatch(sendCtx, p, func(gateCtx context.Context) error { return dst.Write(gateCtx, b) })
				stop()
			} else {
				e = dst.Write(ctx, b)
			}
			if e != nil {
				done <- e
				return
			}
		}
	}
	go copyFrames(up, down, true)
	go copyFrames(down, up, false)
	remaining := 2
	select {
	case e = <-done:
		remaining--
	case <-ctx.Done():
		e = ctx.Err()
	}
	cancel()
	up.Close()
	down.Close()
	// Socket implementations must unblock Read/Write on Close or cancellation.
	// Join both directions before releasing this request's admission lease.
	for ; remaining > 0; remaining-- {
		<-done
	}
	return e
}

// SubprotocolSocketDialer negotiates provider-mandated protocols. It must use the
// supplied gated client and must never log credential-bearing protocol values.
type SubprotocolSocketDialer interface {
	DialProtocols(context.Context, string, http.Header, []string, *http.Client) (Socket, error)
}

// FrameSocket exposes text/binary framing for strict inbound protocol validation.
type FrameSocket interface {
	ReadFrame(context.Context) ([]byte, bool, error)
}
