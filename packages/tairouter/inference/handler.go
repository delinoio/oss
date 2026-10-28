// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Handler struct{ c Config }

func New(c Config) (*Handler, error) {
	if c.Authorizer == nil || c.Router == nil || c.Credentials == nil || c.Usage == nil {
		return nil, errors.New("inference: authorizer, router, credentials and usage recorder are required")
	}
	if _, ok := c.Authorizer.(Dispatcher); !ok {
		return nil, errors.New("inference: authorizer must implement atomic Dispatcher")
	}
	if c.MaxRequestBytes > 512<<20 {
		return nil, errors.New("inference: request limit exceeds 512 MiB")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = 256 << 20
	}
	return &Handler{c}, nil
}
func newID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return "resp_" + hex.EncodeToString(b[:])
}
func (h *Handler) event(ctx context.Context, s string) {
	if h.c.Observer != nil {
		h.c.Observer.Event(ctx, s)
	}
}
func writeError(w http.ResponseWriter, err error) {
	e := publicError(err)
	if errors.Is(err, ErrAmbiguous) {
		w.Header().Del("Retry-After")
		w.Header().Set("x-should-retry", "false")
	} else if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(e.RetryAfter.Seconds())+1))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	envelope := object{"error": object{"type": e.Code, "code": e.Code, "message": e.Code}}
	if tw, ok := w.(*trackedWriter); ok && tw.wire == Anthropic {
		kind := "api_error"
		switch e.Status {
		case 400:
			kind = "invalid_request_error"
		case 401:
			kind = "authentication_error"
		case 403:
			kind = "permission_error"
		case 404:
			kind = "not_found_error"
		case 413:
			kind = "request_too_large"
		case 429:
			kind = "rate_limit_error"
		}
		envelope = object{"type": "error", "error": object{"type": kind, "message": e.Code}}
	}
	_ = json.NewEncoder(w).Encode(envelope)
}
func bearer(r *http.Request) string {
	s := r.Header.Get("Authorization")
	if strings.HasPrefix(s, "Bearer ") {
		return strings.TrimPrefix(s, "Bearer ")
	}
	return r.Header.Get("x-api-key")
}
func routePath(path string) (Wire, Operation, bool) {
	switch path {
	case "/v1/chat/completions":
		return Chat, Generate, true
	case "/v1/responses":
		return Responses, Generate, true
	case "/v1/messages":
		return Anthropic, Generate, true
	case "/v1/responses/compact":
		return Responses, Compact, true
	case "/v1/messages/count_tokens":
		return Anthropic, CountTokens, true
	case "/v1/images/generations":
		return Responses, ImageGeneration, true
	case "/v1/images/edits":
		return Responses, ImageEdit, true
	case "/v1/alpha/search":
		return Responses, Search, true
	}
	return "", "", false
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/backend-api/codex/") {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Path = "/v1/" + strings.TrimPrefix(u.Path, "/backend-api/codex/")
		copy.URL = &u
		r = copy
	}
	if h.serveExtra(w, r) {
		return
	}
	tracked := &trackedWriter{ResponseWriter: w}
	w = tracked
	wire, op, ok := routePath(r.URL.Path)
	tracked.wire = wire
	models := r.URL.Path == "/v1/models"
	artifact := strings.HasPrefix(r.URL.Path, "/v1/tairouter/artifacts/") || strings.HasPrefix(r.URL.Path, "/v1/opencodex/artifacts/")
	if !ok && !models && !artifact {
		writeError(w, &Error{404, "not_found", 0})
		return
	}
	if (models || artifact) && r.Method != "GET" || ok && r.Method != "POST" {
		writeError(w, &Error{405, "method_not_allowed", 0})
		return
	}
	p, release, err := h.c.Authorizer.Admit(r.Context(), bearer(r))
	if err != nil {
		writeError(w, err)
		return
	}
	if release != nil {
		defer release()
	}
	if p.OrganizationID == "" || p.UserID == "" || p.KeyID == "" {
		writeError(w, ErrUnauthorized)
		return
	}
	if models {
		if r.URL.RawQuery != "" || h.c.Views != nil {
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				writeError(w, &Error{400, "invalid_model_query", 0})
				return
			}
			for k, v := range query {
				if (k != "ids" && k != "format" && k != "client_version") || len(v) != 1 {
					writeError(w, &Error{400, "invalid_model_query", 0})
					return
				}
			}
			if query.Get("format") == "desktop-config" && (query.Get("ids") == "cli" || query.Get("client_version") != "") {
				writeError(w, &Error{400, "invalid_model_query", 0})
				return
			}
			if h.c.Views == nil {
				writeError(w, ErrUnsupported)
				return
			}
			body, err := h.c.Views.Read(r.Context(), p, Operation("models"), query)
			if err != nil {
				writeError(w, err)
				return
			}
			limit := 16 << 20
			if query.Get("client_version") != "" {
				limit = 256 << 20
			}
			if len(body) > limit || !json.Valid(body) {
				writeError(w, &Error{507, "catalog_too_large", 0})
				return
			}
			if err = h.guard(r.Context(), p); err != nil {
				writeError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(body)
			return
		}
		m, e := h.c.Router.Models(r.Context(), p)
		if e != nil {
			writeError(w, e)
			return
		}
		if e = h.guard(r.Context(), p); e != nil {
			writeError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if m == nil {
			m = []Model{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(object{"object": "list", "data": m})
		return
	}
	if artifact {
		if h.c.Artifacts == nil {
			writeError(w, ErrUnsupported)
			return
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		u, e := h.c.Artifacts.URL(r.Context(), p, id)
		if e != nil {
			writeError(w, e)
			return
		}
		http.Redirect(w, r, u, http.StatusSeeOther)
		return
	}
	raw, e := h.readRequest(r, h.c.MaxRequestBytes)
	if e != nil {
		writeError(w, e)
		return
	}
	if int64(len(raw)) > h.c.MaxRequestBytes {
		writeError(w, &Error{413, "request_too_large", 0})
		return
	}
	b, e := decode(raw)
	if e != nil {
		writeError(w, e)
		return
	}
	if str(b["model"]) == "" && op != Search && op != ImageGeneration && op != ImageEdit {
		writeError(w, &Error{400, "model_required", 0})
		return
	}
	if op == Generate && wire == Responses {
		var state *Continuation
		state, e = h.restore(r.Context(), p, b)
		if e != nil {
			writeError(w, e)
			return
		}
		if state != nil {
			r = r.WithContext(context.WithValue(r.Context(), continuationKey{}, *state))
		}
	}
	id := newID()
	metadata := requestMetadata{id: id, headers: make(http.Header)}
	for _, k := range []string{"session-id", "session_id", "x-session-id", "thread-id", "x-codex-parent-thread-id", "x-client-request-id"} {
		if v := r.Header.Get(k); v != "" && len(v) <= 2048 {
			metadata.headers.Set(k, v)
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), requestMetadataKey{}, metadata))
	w.Header().Set("X-Request-ID", id)
	rec := UsageRecord{RequestID: id, Model: str(b["model"])}
	defer func() {
		rec.Canceled = r.Context().Err() != nil
		if rec.Canceled {
			rec.Outcome = OutcomeCanceled
		}
		aggregateIntermediateUsage(&rec)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if e := h.c.Usage.Record(ctx, p, rec); e != nil {
			h.event(ctx, "usage_write_failed")
			h.c.Logger.ErrorContext(ctx, "inference usage write failed", "request_id", id)
		} else {
			h.event(ctx, "usage_recorded")
		}
	}()
	err = h.execute(r.Context(), w, p, wire, op, b, &rec)
	if err == nil {
		rec.Outcome = OutcomeSuccess
	}
	if err != nil {
		if rec.Outcome == "" {
			rec.Outcome = OutcomeGatewayError
		}
		if typed, ok := err.(*Error); ok {
			if typed.Code == "upstream_refused" {
				rec.Outcome = OutcomeUpstreamError
				if rec.Status == 429 {
					rec.Outcome = OutcomeRateLimited
				}
			}
			if typed.Code == "upstream_rate_limited" {
				rec.Outcome = OutcomeRateLimited
			}
			if typed == ErrAmbiguous {
				rec.Outcome = OutcomeUpstreamError
			}
		}
		rec.Status = publicError(err).Status
		if !tracked.started {
			writeError(w, err)
		} else {
			h.event(r.Context(), "stream_failed")
			if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
				_ = emitSSE(w, "error", object{"type": "error", "error": object{"code": publicError(err).Code, "message": publicError(err).Code}})
			}
		}
	}
}
func (h *Handler) restore(ctx context.Context, p Principal, b object) (*Continuation, error) {
	id := str(b["previous_response_id"])
	if id == "" {
		return nil, nil
	}
	if h.c.Continuations == nil {
		return nil, ErrContinuation
	}
	c, e := h.c.Continuations.Load(ctx, p, id)
	if e != nil {
		return nil, ErrContinuation
	}
	if !c.ExpiresAt.After(time.Now()) || c.Model != str(b["model"]) {
		return nil, ErrContinuation
	}
	var old []any
	if json.Unmarshal(c.Input, &old) != nil {
		return nil, ErrContinuation
	}
	input := b["input"]
	if s, ok := input.(string); ok {
		input = []any{object{"role": "user", "content": s}}
	}
	b["input"] = append(old, arr(input)...)
	delete(b, "previous_response_id")
	c.ResponseID = id
	return &c, nil
}

type requestMetadataKey struct{}
type requestMetadata struct {
	id      string
	headers http.Header
}
type continuationKey struct{}
type BoundCredentials interface {
	SelectBound(context.Context, Principal, Target, string) (Credential, error)
}

func (h *Handler) save(ctx context.Context, p Principal, b, out object, rec *UsageRecord) error {
	rec.ResponseID = str(out["id"])
	if e := h.CaptureReplayReceipts(ctx, out); e != nil {
		return e
	}
	if b["store"] == false {
		return nil
	}
	if h.c.Continuations == nil {
		return nil
	}
	if e := h.c.Authorizer.BeforeSend(ctx, p); e != nil {
		return e
	}
	input := b["input"]
	if rec.continuationInput != nil {
		input = rec.continuationInput
	}
	if s, ok := input.(string); ok {
		input = []any{object{"role": "user", "content": s}}
	}
	history := append(append([]any{}, arr(input)...), arr(out["output"])...)
	data, e := json.Marshal(history)
	if e != nil {
		return e
	}
	if len(data) > 32<<20 {
		return &Error{502, "continuation_too_large", 0}
	}
	return h.c.Continuations.Save(ctx, p, str(out["id"]), Continuation{Provider: rec.Provider, AccountID: rec.AccountID, Model: str(b["model"]), RoutedModel: rec.Model, Input: data, ExpiresAt: time.Now().Add(24 * time.Hour)})
}
func (h *Handler) guard(ctx context.Context, p Principal) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return h.c.Authorizer.BeforeSend(ctx, p)
}
func retryWait(value string, fallback time.Duration) time.Duration {
	if n, e := strconv.ParseInt(value, 10, 32); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(value); e == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return fallback
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (h *Handler) execute(ctx context.Context, w http.ResponseWriter, p Principal, in Wire, op Operation, b object, rec *UsageRecord) error {
	ctx = context.WithValue(ctx, inboundWireKey{}, in)
	rawBody, _ := json.Marshal(b)
	boundOriginalHistory := issuerBoundHistory(b)
	ctx = context.WithValue(ctx, routingRequestKey{}, routingRequest{h: h, p: p, record: rec, body: rawBody})
	var route Route
	var e error
	if forced, ok := ctx.Value(compactionRouteKey{}).(Route); ok {
		route = forced
	} else {
		route, e = h.c.Router.Resolve(ctx, p, str(b["model"]), op)
	}
	if e != nil {
		return e
	}
	if len(route.Targets) == 0 {
		return ErrNoCredential
	}
	if cap, ok := ctx.Value(routingBudgetKey{}).(int); ok && cap < route.MaxSends {
		route.MaxSends = cap
	}
	if route.MaxSends < 1 {
		return &Error{503, "invalid_send_budget", 0}
	}
	ctx = requestSupportBudget(ctx, rec, route.MaxSends)
	for ti, t := range route.Targets {
		adapter := h.c.Adapters[t.Wire]
		if adapter == nil && t.Wire != Chat && t.Wire != Responses && t.Wire != Anthropic {
			return ErrUnsupported
		}
		if op == CountTokens && t.Wire != Anthropic {
			if e := h.guard(ctx, p); e != nil {
				return e
			}
			return writeTokenEstimate(w, b, t, adapter)
		}
		if op == Compact && t.Wire != Responses {
			return h.compactFallback(ctx, w, p, b, Route{Targets: route.Targets[ti:], MaxSends: route.MaxSends}, rec)
		}
		client, local, e := h.c.Destinations.client(t.BaseURL)
		if e != nil {
			return e
		}
		if e = h.guard(ctx, p); e != nil {
			return e
		}
		var cred Credential
		if handoff, ok := ctx.Value(compactionHandoffKey{}).(CompactionHandoff); ok {
			cred = handoff.Credential
		} else if state, ok := ctx.Value(continuationKey{}).(Continuation); ok && state.Provider != "" {
			if t.Provider != state.Provider || t.Model != state.RoutedModel {
				return ErrContinuation
			}
			if bound, ok := h.c.Credentials.(BoundCredentials); ok {
				cred, e = bound.SelectBound(ctx, p, t, state.AccountID)
			} else {
				cred, e = h.c.Credentials.Select(ctx, p, t, "")
				if cred.AccountID != state.AccountID {
					return ErrContinuation
				}
			}
		} else {
			cred, e = h.c.Credentials.Select(ctx, p, t, "")
		}
		if e != nil {
			return e
		}
		logicalTarget := t
		selectedCredential := cred
		negotiationRefreshed := map[string]bool{}
		refreshed := false
		rateRetries, transientSends := 0, 0
		endpointHops := 0
		repairCount := 0
		visited := map[string]bool{cred.AccountID: true}
		for {
			if rec.Sends >= route.MaxSends {
				return &Error{429, "request_send_budget_exhausted", 0}
			}
			attemptBase := ctx
			t, cred = logicalTarget, selectedCredential
			if h.c.Negotiator != nil {
				original, _ := json.Marshal(b)
				var expires time.Time
				t, cred, expires, e = h.c.Negotiator.Negotiate(ctx, p, logicalTarget, selectedCredential, original, h.c.Destinations)
				if e != nil {
					refusal := e
					typed, providerRefusal := e.(*Error)
					// Only explicit support-provider refusals permit recovery. An
					// admission/storage401 must never be mistaken for token expiry.
					providerRefusal = providerRefusal && typed.Code == "copilot_auto_negotiation_failed"
					if providerRefusal && typed.Status == 401 && logicalTarget.Auth == OAuth && !negotiationRefreshed[selectedCredential.AccountID] {
						if e = h.guard(ctx, p); e != nil {
							return e
						}
						account := selectedCredential.AccountID
						updated, err := h.c.Credentials.Refresh(ctx, p, logicalTarget, selectedCredential)
						if err != nil {
							return err
						}
						if updated.AccountID != account {
							return ErrContinuation
						}
						selectedCredential = updated
						negotiationRefreshed[account] = true
						continue
					}
					_, continued := ctx.Value(continuationKey{}).(Continuation)
					if providerRefusal && typed.Status == 429 && logicalTarget.Retry.RotateOn429 && !continued && !boundOriginalHistory && !issuerBoundHistory(b) {
						if e = h.guard(ctx, p); e != nil {
							return e
						}
						if reporter, ok := h.c.Credentials.(FailureReporter); ok {
							if e = reporter.Failed(ctx, p, logicalTarget, selectedCredential, 429, typed.RetryAfter); e != nil {
								return e
							}
						}
						next, err := h.c.Credentials.Select(ctx, p, logicalTarget, selectedCredential.AccountID)
						if err == ErrNoCredential {
							return refusal
						}
						if err != nil {
							return err
						}
						if visited[next.AccountID] {
							return refusal
						}
						selectedCredential = next
						visited[next.AccountID] = true
						continue
					}
					return refusal
				}
				if cred.AccountID != selectedCredential.AccountID {
					return ErrContinuation
				}
				attemptBase = context.WithValue(ctx, negotiationExpiryKey{}, expires)
				if e = validateNegotiationExpiry(attemptBase); e != nil {
					return e
				}
				adapter = h.c.Adapters[t.Wire]
				if adapter == nil && t.Wire != Chat && t.Wire != Responses && t.Wire != Anthropic {
					return ErrUnsupported
				}
			}
			scope := scopedAdapterState{h: h, p: p, t: t, c: cred}
			attemptBody, err := decode(rawBody)
			if err != nil {
				return err
			}
			attemptBody, err = h.expandCompactionReferences(ctx, scope, attemptBody)
			if err != nil {
				return err
			}
			if err = h.validateReplay(ctx, attemptBody, scope); err != nil {
				return err
			}
			attemptBody, err = h.prepareCompactionRetention(ctx, scope, attemptBody)
			if err != nil {
				return err
			}
			b = attemptBody

			attemptCtx := context.WithValue(attemptBase, replayScopeKey{}, scope)
			if t.Policy != nil {
				attemptCtx = context.WithValue(attemptCtx, responseRewriterKey{}, t.Policy.NewResponseRewriterForRequest(rawBody))
			}
			if prepare, ok := adapter.(ContextPreparer); ok {
				attemptCtx = prepare.PrepareContext(attemptCtx)
			}
			attemptTimeout := t.AttemptTimeout
			if attemptTimeout == 0 && t.Wire == Wire("kiro") {
				attemptTimeout = 200 * time.Second
			}
			requestBody := clone(b)
			if t.ReasoningEffort != "" {
				switch in {
				case Responses:
					reasoning := clone(obj(requestBody["reasoning"]))
					reasoning["effort"] = t.ReasoningEffort
					requestBody["reasoning"] = reasoning
				case Chat:
					requestBody["reasoning_effort"] = t.ReasoningEffort
				case Anthropic:
					outputConfig := clone(obj(requestBody["output_config"]))
					outputConfig["effort"] = t.ReasoningEffort
					requestBody["output_config"] = outputConfig
				}
			}
			payload := requestBody
			if op == ImageGeneration || op == ImageEdit {
				payload["response_format"] = "b64_json"
			}
			if op == Generate && (adapter == nil || t.Wire == Chat || t.Wire == Responses || t.Wire == Anthropic) {
				payload, e = translateTargetRequest(requestBody, in, t)
				if e != nil {
					return e
				}
			}
			if t.Model != "" {
				payload["model"] = t.Model
			}
			if t.Wire == Responses && op == Generate {
				payload["store"] = false
			}
			if op == Generate && (adapter == nil || t.Wire == Chat || t.Wire == Responses || t.Wire == Anthropic) {
				attemptCtx, payload, e = h.prepareReasoningReplay(attemptCtx, p, t, cred, payload)
				if e != nil {
					return e
				}
				if t.Policy != nil {
					payload, e = t.Policy.TransformRequest(payload)
					if e != nil {
						return &Error{400, "invalid_provider_policy_request", 0}
					}
				}
			}
			if op == Generate {
				if e = applyFastBody(t, payload); e != nil {
					return e
				}
			}
			path := wirePath(t.Wire)
			switch op {
			case Compact:
				path = "/responses/compact"
			case CountTokens:
				path = "/messages/count_tokens"
			case ImageGeneration, ImageEdit, Search:
				path = "/" + string(op)
			}
			data, e := json.Marshal(payload)
			if e != nil {
				return e
			}
			responseTarget := t
			endpoint := strings.TrimRight(t.BaseURL, "/") + path
			if op == Generate && t.Endpoint != "" {
				u, err := url.Parse(t.Endpoint)
				base, _ := url.Parse(t.BaseURL)
				if err != nil || u.User != nil || u.Fragment != "" || origin(u) != origin(base) {
					return ErrDestination
				}
				endpoint = t.Endpoint
			}
			var adapterHeaders http.Header
			var adapterInput AdapterInput
			var builtRequest AdapterRequest
			var executedResponse *http.Response
			var executionCancel context.CancelFunc
			if adapter != nil {
				adapterInput = AdapterInput{Principal: p, Target: t, Credential: cred, Operation: op, Wire: in, Body: data}
				if t.Wire == Chat || t.Wire == Responses || t.Wire == Anthropic {
					adapterInput.Wire = t.Wire
				}
				if meta, ok := ctx.Value(requestMetadataKey{}).(requestMetadata); ok {
					adapterInput.RequestID = meta.id
					adapterInput.ClientHeaders = meta.headers.Clone()
				}
				if previous, ok := ctx.Value(continuationKey{}).(Continuation); ok {
					adapterInput.PreviousResponseID = previous.ResponseID
				}
				if stateful, ok := adapter.(StatefulAdapter); ok {
					restored, err := stateful.RestoreState(attemptCtx, adapterInput, scopedAdapterState{h: h, p: p, t: t, c: cred})
					if err != nil {
						return err
					}
					attemptCtx = restored
				}
				if pre, ok := adapter.(PreparingAdapter); ok {
					prepared, err := pre.Prepare(attemptCtx, adapterInput, &requestExecutor{h: h, p: p, target: t, credential: cred, record: rec, maxSends: route.MaxSends})
					if err != nil {
						return err
					}
					adapterInput.Body = prepared
				}
				if executing, ok := adapter.(ExecutingAdapter); ok {
					rec.Provider = t.Provider
					rec.Model = str(payload["model"])
					rec.AccountID = cred.AccountID
					executionCtx, stop := context.WithCancel(attemptCtx)
					response, responseWire, err := executing.Execute(executionCtx, adapterInput, &requestExecutor{h: h, p: p, target: t, credential: cred, record: rec, maxSends: route.MaxSends}, scopedAdapterState{h: h, p: p, t: t, c: cred})
					if err != nil {
						stop()
						return err
					}
					if response == nil || response.Body == nil {
						stop()
						return &Error{502, "invalid_adapter_response", 0}
					}
					responseTarget.Wire = responseWire
					executedResponse = response
					executionCancel = stop
					attemptCtx = executionCtx
				}
				if executedResponse == nil {
					built, err := adapter.Build(attemptCtx, adapterInput)
					if err != nil {
						return err
					}
					u, err := url.Parse(built.URL)
					base, _ := url.Parse(t.BaseURL)
					if err != nil || u.User != nil || u.Fragment != "" || origin(u) != origin(base) {
						return ErrDestination
					}
					if built.AttemptTimeout > 0 {
						attemptTimeout = built.AttemptTimeout
					}
					built, err = ApplyRequestPolicy(t, built)
					if err != nil {
						return err
					}
					builtRequest = built
					endpoint = built.URL
					data = built.Body
					adapterHeaders = built.Headers
					responseTarget.Wire = built.ResponseWire
					if responseTarget.Wire != Chat && responseTarget.Wire != Responses && responseTarget.Wire != Anthropic {
						return ErrUnsupported
					}
				}
			}
		retrySend:
			sendCtx, cancel := context.WithCancel(attemptCtx)
			if attemptTimeout > 0 {
				cancel()
				sendCtx, cancel = context.WithTimeout(attemptCtx, attemptTimeout)
			}
			var resp *http.Response
			if executedResponse != nil {
				cancel()
				sendCtx, cancel = attemptCtx, executionCancel
				resp = executedResponse
				transientSends++
			} else {
				req, e := http.NewRequestWithContext(sendCtx, "POST", endpoint, bytes.NewReader(data))
				if e != nil {
					cancel()
					return ErrDestination
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				if t.Wire == Anthropic {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
				for k, vs := range t.Headers {
					for _, v := range vs {
						req.Header.Add(k, v)
					}
				}
				for k, vs := range cred.Headers {
					req.Header.Del(k)
					for _, v := range vs {
						req.Header.Add(k, v)
					}
				}
				if adapter == nil && cred.Secret != "" {
					if t.Wire == Anthropic && t.Auth != OAuth {
						req.Header.Set("x-api-key", cred.Secret)
					} else {
						req.Header.Set("Authorization", "Bearer "+cred.Secret)
					}
				} else if adapter == nil && t.Auth != Anonymous {
					cancel()
					return ErrNoCredential
				}
				for k, vs := range adapterHeaders {
					req.Header.Del(k)
					for _, v := range vs {
						req.Header.Add(k, v)
					}
				}
				if e = applyFastHeaders(t, req.Header); e != nil {
					cancel()
					return e
				}
				if rec.Sends >= route.MaxSends {
					cancel()
					return &Error{429, "request_send_budget_exhausted", 0}
				}
				if e = h.guard(ctx, p); e != nil {
					cancel()
					return e
				}
				if t.Transport == WebSocketTransport {
					if adapter != nil || op != Generate {
						cancel()
						return ErrUnsupported
					}
					rec.Provider = t.Provider
					rec.Model = str(payload["model"])
					rec.AccountID = cred.AccountID
					resp, err := h.responsesSocket(sendCtx, p, t, cred, req.Header, payload, rec)
					if err != nil {
						cancel()
						return err
					}
					rec.Status = resp.StatusCode
					err = h.deliver(sendCtx, w, p, in, op, b, t, local, resp, rec, cancel)
					resp.Body.Close()
					cancel()
					return err
				}
				transientSends++
				rec.Provider = t.Provider
				rec.Model = str(payload["model"])
				rec.AccountID = cred.AccountID
				client, local, e = h.c.Destinations.client(t.BaseURL)
				if e != nil {
					cancel()
					return e
				}
				if transport, ok := client.Transport.(*http.Transport); ok {
					if local {
						transport.ResponseHeaderTimeout = 0
					}
					if t.StallTimeout != nil {
						transport.ResponseHeaderTimeout = *t.StallTimeout
					}
				}
				sent := gateClient(client, sendCtx, p, modelDispatchAuthorizer{Authorizer: modelBudgetAuthorizer{Authorizer: h.c.Authorizer, record: rec, max: route.MaxSends}, record: rec}, func() { rec.Sends++; h.event(ctx, "upstream_send") })
				resp, e = client.Do(req)
				if e != nil {
					if observed := h.observeTransportFailure(sendCtx, p, t, cred, e, sent.Load()); observed != nil {
						if sent.Load() {
							rec.UnmeasuredAttempts++
						}
						cancel()
						return observed
					}
					cancel()
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if !sent.Load() {
						return e
					}
					rec.UnmeasuredAttempts++
					return ErrAmbiguous
				}
			}
			if executedResponse == nil {
				if e = h.observeResponse(sendCtx, p, t, cred, resp); e != nil {
					resp.Body.Close()
					cancel()
					rec.UnmeasuredAttempts++
					return e
				}
			}
			if inspector, ok := adapter.(ResponseInspector); ok && executedResponse == nil {
				original := resp
				inspected, err := inspector.InspectResponse(sendCtx, adapterInput, resp)
				if err != nil {
					original.Body.Close()
					cancel()
					return err
				}
				if inspected == nil || inspected.Body == nil {
					original.Body.Close()
					cancel()
					return &Error{502, "invalid_adapter_response", 0}
				}
				resp = inspected
			}
			rec.Status = resp.StatusCode
			if resp.StatusCode >= 500 {
				rec.UnmeasuredAttempts++
			}
			var terminalRefusal *Error
			var headerDecision *RecoveryDecision
			if policy := h.c.RecoveryPolicies[t.Wire]; policy != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				decision, err := policy.RecoverHTTP(sendCtx, adapterInput, HTTPFailure{Status: resp.StatusCode, Header: resp.Header.Clone(), Attempt: transientSends, Repairs: repairCount, EndpointHops: endpointHops, Refreshed: refreshed})
				if err != nil {
					resp.Body.Close()
					cancel()
					return err
				}
				terminalRefusal = decision.Error
				if decision.Action != RecoveryDefault && decision.Action != "" {
					headerDecision = &decision
				}
			}
			if headerDecision == nil && ((resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == 304) {
				if adapter != nil && executedResponse == nil {
					original := resp
					var decoded *http.Response
					var err error
					if contextual, ok := adapter.(ContextualAdapter); ok {
						decoded, err = contextual.DecodeWithInput(sendCtx, adapterInput, resp)
					} else {
						decoded, err = adapter.Decode(sendCtx, resp)
					}
					if err != nil {
						original.Body.Close()
						cancel()
						return err
					}
					if decoded == nil || decoded.Body == nil {
						original.Body.Close()
						cancel()
						return &Error{502, "invalid_adapter_response", 0}
					}
					resp = decoded
				}
				err := h.deliver(sendCtx, w, p, in, op, b, responseTarget, local, resp, rec, cancel)
				if err == nil {
					h.captureAdapterState(attemptCtx, p, adapter, adapterInput, t, cred, rec, b)
				}
				resp.Body.Close()
				if err != nil && sendCtx.Err() != nil {
					err = sendCtx.Err()
				}
				cancel()
				return err
			}
			// Read only bounded error data for replay markers; never log provider payloads.
			var errorBody []byte
			var readErr error
			if headerDecision == nil {
				errorBody, readErr = readBody(resp.Body, 1<<20, 5*time.Second, 5*time.Second, cancel)
			}
			resp.Body.Close()
			if readErr != nil {
				cancel()
				return &Error{resp.StatusCode, "upstream_refused", 0}
			}
			if m, err := decode(errorBody); err == nil {
				code := str(obj(m["error"])["code"])
				if code == "" {
					code = str(m["code"])
				}
				switch code {
				case "upstream_reset_replay_refused", "upstream_no_response", "upstream_closed_before_response":
					cancel()
					return ErrAmbiguous
				}
			}
			if op == Compact {
				fallback, handoffErr := h.tryCompactionHandoff(ctx, p, t, HTTPFailure{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: errorBody, Attempt: transientSends, Refreshed: refreshed}, b, route, rec)
				if handoffErr != nil {
					cancel()
					return handoffErr
				}
				if fallback != nil {
					cancel()
					w.Header().Set("Content-Type", "application/json")
					_, err := w.Write(fallback.body.Bytes())
					return err
				}
			}
			if op == Compact && resp.StatusCode == 404 {
				cancel()
				bound := Continuation{Provider: t.Provider, AccountID: cred.AccountID, Model: str(b["model"]), RoutedModel: t.Model}
				fallbackCtx := context.WithValue(ctx, continuationKey{}, bound)
				return h.compactFallback(fallbackCtx, w, p, b, Route{Targets: []Target{t}, MaxSends: route.MaxSends}, rec)
			}
			if repairer, ok := adapter.(RequestRepairer); ok && (resp.StatusCode == 400 || resp.StatusCode == 422) && executedResponse == nil && repairCount < 2 && rec.Sends < route.MaxSends {
				repaired, changed, err := repairer.RepairRequest(attemptCtx, adapterInput, builtRequest, HTTPFailure{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: errorBody, Attempt: transientSends, Repairs: repairCount, EndpointHops: endpointHops, Refreshed: refreshed})
				if err != nil {
					cancel()
					return err
				}
				if changed {
					u, err := url.Parse(repaired.URL)
					base, _ := url.Parse(t.BaseURL)
					if err != nil || u.User != nil || u.Fragment != "" || origin(u) != origin(base) || repaired.Method != "" && repaired.Method != "POST" {
						cancel()
						return ErrDestination
					}
					if repaired.ResponseWire != Chat && repaired.ResponseWire != Responses && repaired.ResponseWire != Anthropic {
						cancel()
						return ErrUnsupported
					}
					repairCount++
					cancel()
					builtRequest = repaired
					endpoint = repaired.URL
					data = repaired.Body
					adapterHeaders = repaired.Headers
					responseTarget.Wire = repaired.ResponseWire
					if repaired.AttemptTimeout > 0 {
						attemptTimeout = repaired.AttemptTimeout
					}
					goto retrySend
				}
			}
			if policy := h.c.RecoveryPolicies[t.Wire]; policy != nil {
				input := adapterInput
				if adapter == nil {
					input = AdapterInput{Principal: p, Target: t, Credential: cred, Operation: op, Wire: in, Body: data}
					if previous, ok := ctx.Value(continuationKey{}).(Continuation); ok {
						input.PreviousResponseID = previous.ResponseID
					}
				}
				var decision RecoveryDecision
				var err error
				if headerDecision != nil {
					decision = *headerDecision
				} else {
					decision, err = policy.RecoverHTTP(ctx, input, HTTPFailure{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: errorBody, Attempt: transientSends, Repairs: repairCount, EndpointHops: endpointHops, Refreshed: refreshed})
				}
				if err != nil {
					cancel()
					return err
				}
				terminalRefusal = decision.Error
				if decision.Action != RecoveryDefault && decision.Action != "" {
					if decision.Action == RecoveryStop || rec.Sends >= route.MaxSends || op == ImageGeneration || op == ImageEdit {
						cancel()
						if decision.Error != nil {
							rec.Outcome = OutcomeUpstreamError
							if decision.Error.Status == 429 {
								rec.Outcome = OutcomeRateLimited
							}
							return decision.Error
						}
						return &Error{resp.StatusCode, "upstream_refused", 0}
					}
					if e = h.guard(ctx, p); e != nil {
						cancel()
						return e
					}
					_, continued := ctx.Value(continuationKey{}).(Continuation)
					continued = continued || boundOriginalHistory || issuerBoundHistory(b)
					switch decision.Action {
					case RecoveryRefresh:
						if t.Auth != OAuth || refreshed {
							cancel()
							return &Error{resp.StatusCode, "upstream_refused", 0}
						}
						cred, e = h.c.Credentials.Refresh(ctx, p, logicalTarget, selectedCredential)
						selectedCredential = cred
						refreshed = true
					case RecoveryRotate:
						if continued {
							cancel()
							return ErrContinuation
						}
						next, err := h.c.Credentials.Select(ctx, p, logicalTarget, selectedCredential.AccountID)
						e = err
						if e == ErrNoCredential {
							cancel()
							if decision.Error != nil {
								rec.Outcome = OutcomeUpstreamError
								if decision.Error.Status == 429 {
									rec.Outcome = OutcomeRateLimited
								}
								return decision.Error
							}
							return &Error{resp.StatusCode, "upstream_refused", 0}
						}
						if e == nil {
							if visited[next.AccountID] {
								cancel()
								return ErrNoCredential
							}
							cred = next
							selectedCredential = next
							visited[next.AccountID] = true
						}
					case RecoveryFallback:
						cancel()
						if continued || ti+1 >= len(route.Targets) {
							return &Error{resp.StatusCode, "upstream_refused", 0}
						}
						break
					case RecoveryRetry:
					default:
						cancel()
						return ErrUnsupported
					}
					if e != nil {
						cancel()
						return e
					}
					if decision.Endpoint != "" {
						if h.c.RecoveryDestinations == nil {
							cancel()
							return ErrDestination
						}
						if e = h.c.RecoveryDestinations.AuthorizeRecovery(ctx, p, t, decision.Endpoint); e != nil {
							cancel()
							return e
						}
						if _, _, e = h.c.Destinations.client(decision.Endpoint); e != nil {
							cancel()
							return e
						}
						t.BaseURL = decision.Endpoint
						logicalTarget.BaseURL = decision.Endpoint
						endpointHops++
					}
					cancel()
					if decision.Delay < 0 || decision.Delay > 10*time.Minute {
						return &Error{resp.StatusCode, "upstream_refused", 0}
					}
					if e = wait(ctx, decision.Delay); e != nil {
						return e
					}
					if decision.Action == RecoveryFallback {
						break
					}
					// A typed same-account retry must retain an accepted request repair.
					// Refresh, rotation and endpoint changes rebuild credential-bound headers.
					if decision.Action == RecoveryRetry && decision.Endpoint == "" && executedResponse == nil {
						goto retrySend
					}
					continue
				}
			}
			// These responses have not reached the client. Only explicit refusal policies permit replay.
			delay := retryWait(resp.Header.Get("Retry-After"), t.Retry.Interval)
			maxWait := t.Retry.MaxWait
			if maxWait <= 0 {
				maxWait = 60 * time.Second
			}
			retry := false
			nextTarget := false
			_, continued := ctx.Value(continuationKey{}).(Continuation)
			continued = continued || boundOriginalHistory || issuerBoundHistory(b)
			allowed := rec.Sends < route.MaxSends && op != ImageGeneration && op != ImageEdit
			if continued {
				t.Retry.RotateOn429 = false
				t.Retry.FallbackOn429 = false
				t.Retry.FallbackOn5xx = false
			}
			if reporter, ok := h.c.Credentials.(FailureReporter); ok && resp.StatusCode == 429 {
				if e = h.guard(ctx, p); e == nil {
					e = reporter.Failed(ctx, p, logicalTarget, selectedCredential, resp.StatusCode, delay)
				}
				if e != nil {
					resp.Body.Close()
					cancel()
					return e
				}
			}
			if allowed && resp.StatusCode == 401 && t.Auth == OAuth && t.Retry.RefreshOn401 && !refreshed {
				if e = h.guard(ctx, p); e != nil {
					resp.Body.Close()
					cancel()
					return e
				}
				updated, err := h.c.Credentials.Refresh(ctx, p, logicalTarget, selectedCredential)
				if err == nil {
					cred = updated
					selectedCredential = updated
					retry = true
					refreshed = true
				}
			}
			if allowed && resp.StatusCode == 429 {
				if t.Auth == APIKey && rateRetries < t.Retry.RateLimitRetries && delay <= maxWait {
					retry = true
					rateRetries++
					if e = h.guard(ctx, p); e != nil {
						resp.Body.Close()
						cancel()
						return e
					}
					resp.Body.Close()
					cancel()
					if e = wait(ctx, delay); e != nil {
						return e
					}
					continue
				}
				if t.Retry.RotateOn429 {
					if e = h.guard(ctx, p); e != nil {
						resp.Body.Close()
						cancel()
						return e
					}
					next, err := h.c.Credentials.Select(ctx, p, logicalTarget, selectedCredential.AccountID)
					if err == nil && !visited[next.AccountID] {
						cred = next
						selectedCredential = next
						visited[next.AccountID] = true
						retry = true
						rateRetries = 0
					}
				}
				if !retry && t.Retry.FallbackOn429 && ti+1 < len(route.Targets) {
					nextTarget = true
				}
			}
			if allowed && transientStatus(resp.StatusCode) {
				if t.Auth == APIKey && (t.Wire == Chat || t.Wire == Responses) && transientSends < t.Retry.TransientSends {
					retry = true
				} else if t.Retry.FallbackOn5xx && ti+1 < len(route.Targets) {
					nextTarget = true
				}
			}
			if retry || nextTarget {
				resp.Body.Close()
				cancel()
				if nextTarget {
					break
				}
				continue
			}
			// Never expose arbitrary upstream error bodies: these may contain credentials or internal URLs.
			resp.Body.Close()
			cancel()
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return ErrDestination
			}
			if terminalRefusal != nil {
				rec.Outcome = OutcomeUpstreamError
				if terminalRefusal.Status == 429 {
					rec.Outcome = OutcomeRateLimited
				}
				return terminalRefusal
			}
			if resp.StatusCode == 429 {
				return &Error{429, "upstream_rate_limited", delay}
			}
			return &Error{resp.StatusCode, "upstream_refused", 0}
		}
	}
	return ErrNoCredential
}
func (h *Handler) deliver(ctx context.Context, w http.ResponseWriter, p Principal, in Wire, op Operation, b object, t Target, local bool, resp *http.Response, rec *UsageRecord, cancel context.CancelFunc) error {
	if resp.StatusCode == 204 || resp.StatusCode == 205 || resp.StatusCode == 304 {
		w.WriteHeader(resp.StatusCode)
		return nil
	}
	stall := 300 * time.Second
	if local {
		stall = 0
	}
	if t.StallTimeout != nil {
		stall = *t.StallTimeout
	}
	stream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	if stream {
		resp.Body = wrapTerminalPolicy(ctx, resp.Body, t)
		return h.deliverStream(ctx, w, p, in, op, b, t, resp, rec, cancel, stall)
	}
	total := 180 * time.Second
	idle := 30 * time.Second
	if op == ImageGeneration || op == ImageEdit {
		total = 15 * time.Minute
		idle = stall
	}
	maxBody := int64(32 << 20)
	if op == ImageGeneration || op == ImageEdit {
		maxBody = 140 << 20
	}
	raw, e := readBody(resp.Body, maxBody, total, idle, cancel)
	if e != nil {
		return e
	}
	out, e := decode(raw)
	if e != nil {
		return &Error{502, "invalid_upstream_json", 0}
	}
	rec.ResponseID = str(out["id"])
	if t.Wire == Chat {
		if e = h.CaptureReasoningReplay(ctx, out); e != nil {
			return e
		}
	}
	rec.Usage = readUsage(out, t.Wire)
	rec.ServiceTier = str(out["service_tier"])
	out, e = h.RewriteProviderResponse(ctx, out)
	if e != nil {
		return e
	}
	if op == Generate && (in != t.Wire || forceMessagesTranslation(in, t)) {
		r, e := parseResult(out, t.Wire)
		if e != nil {
			return e
		}
		if e = validateTools(r, b, in); e != nil {
			return e
		}
		out = r.response(in)
	}
	if op == ImageGeneration || op == ImageEdit {
		if e = h.images(ctx, p, out); e != nil {
			return e
		}
	}
	if op == Compact {
		if !validNativeCompact(out) {
			return &Error{502, "invalid_compaction_response", 0}
		}
	}
	if op == Generate || op == Compact {
		if e := h.CaptureReplayReceipts(ctx, out); e != nil {
			return e
		}
	}
	// Do not forget the prior serving route until all required opaque replay
	// receipts for the replacement history are durably fenced and saved.
	if op == Compact && h.c.CompactionHandoffs != nil && ctx.Value(compactionHandoffKey{}) == nil {
		if err := h.c.CompactionHandoffs.Clear(ctx, p); err != nil {
			return err
		}
	}
	if op == Generate && in == Responses {
		if e = h.save(ctx, p, b, out, rec); e != nil {
			h.event(ctx, "continuation_write_failed")
			h.c.Logger.WarnContext(ctx, "inference continuation not saved", "request_id", rec.RequestID)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	return json.NewEncoder(w).Encode(out)
}
func readBody(r io.ReadCloser, max int64, total, idle time.Duration, cancel context.CancelFunc) ([]byte, error) {
	var expired atomic.Bool
	var deadline, inactivity *time.Timer
	if total > 0 {
		deadline = time.AfterFunc(total, func() { expired.Store(true); cancel(); r.Close() })
		defer deadline.Stop()
	}
	if idle > 0 {
		firstByte := total
		if firstByte <= 0 {
			firstByte = idle
		}
		inactivity = time.AfterFunc(firstByte, func() { expired.Store(true); cancel(); r.Close() })
		defer inactivity.Stop()
	}
	var out bytes.Buffer
	buf := make([]byte, 32<<10)
	for {
		n, e := r.Read(buf)
		if n > 0 {
			if int64(out.Len()+n) > max {
				return nil, &Error{502, "upstream_body_too_large", 0}
			}
			out.Write(buf[:n])
			if inactivity != nil {
				inactivity.Reset(idle)
			}
		}
		if expired.Load() {
			return nil, context.DeadlineExceeded
		}
		if e == io.EOF {
			return out.Bytes(), nil
		}
		if e != nil {
			return nil, e
		}
	}
}
func (h *Handler) images(ctx context.Context, p Principal, out object) error {
	if h.c.Artifacts == nil {
		return ErrUnsupported
	}
	var total int
	var images []GeneratedImage
	rows := arr(out["data"])
	for _, v := range rows {
		m := obj(v)
		s := str(m["b64_json"])
		if s == "" {
			return &Error{502, "image_inline_data_required", 0}
		}
		if len(s) > base64.StdEncoding.EncodedLen(50<<20) {
			return &Error{502, "image_too_large", 0}
		}
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			return &Error{502, "invalid_image", 0}
		}
		total += len(b)
		if len(b) > 50<<20 || total > 100<<20 {
			return &Error{502, "image_too_large", 0}
		}
		contentType := http.DetectContentType(b)
		switch contentType {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return &Error{502, "invalid_image_content", 0}
		}
		images = append(images, GeneratedImage{Data: b, ContentType: contentType})
	}
	if len(images) == 0 {
		return &Error{502, "image_result_empty", 0}
	}
	if e := h.guard(ctx, p); e != nil {
		return e
	}
	var urls []string
	if batch, ok := h.c.Artifacts.(ArtifactBatchStore); ok {
		var e error
		urls, e = batch.PutBatch(ctx, p, images)
		if e != nil {
			return e
		}
		if len(urls) != len(images) {
			return &Error{502, "invalid_artifact_batch", 0}
		}
	} else {
		for _, im := range images {
			if e := h.guard(ctx, p); e != nil {
				return e
			}
			u, e := h.c.Artifacts.Put(ctx, p, im.Data, im.ContentType)
			if e != nil {
				return e
			}
			urls = append(urls, u)
		}
	}
	for i, row := range rows {
		m := obj(row)
		delete(m, "b64_json")
		m["url"] = urls[i]
	}
	return nil
}

type trackedWriter struct {
	http.ResponseWriter
	started bool
	wire    Wire
}

func (w *trackedWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *trackedWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}
func (w *trackedWriter) Flush() {
	w.started = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) captureAdapterState(ctx context.Context, p Principal, a Adapter, in AdapterInput, t Target, c Credential, rec *UsageRecord, b object) {
	stateful, ok := a.(StatefulAdapter)
	if !ok || b["store"] == false {
		return
	}
	if e := h.guard(ctx, p); e != nil {
		h.event(ctx, "provider_state_write_failed")
		return
	}
	if e := stateful.CaptureState(ctx, in, rec.ResponseID, scopedAdapterState{h: h, p: p, t: t, c: c}); e != nil {
		h.event(ctx, "provider_state_write_failed")
		h.c.Logger.WarnContext(ctx, "inference provider state not saved", "request_id", rec.RequestID)
	}
}

func transientStatus(status int) bool {
	switch status {
	case 500, 502, 503, 504, 520, 521, 522:
		return true
	}
	return false
}
