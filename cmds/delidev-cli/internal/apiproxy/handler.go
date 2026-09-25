package apiproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxBody = 32 << 20
const maxStream = 256 << 20
const requestTimeout = 15 * time.Minute
const clientWriteTimeout = 30 * time.Second

type requestPhase string

const (
	phaseCredential requestPhase = "credential"
	phaseRequest    requestPhase = "request"
	phaseHeaders    requestPhase = "response-headers"
	phaseStream     requestPhase = "response-stream"
	phaseBody       requestPhase = "response-body"
	phaseComplete   requestPhase = "complete"
)

type Handler struct {
	authority Authority
	logger    *slog.Logger
	client    *http.Client
	slots     chan struct{}
}

func New(authority Authority, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	transport := &http.Transport{Proxy: nil, DialContext: directDial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Minute, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &Handler{authority: authority, logger: logger, slots: make(chan struct{}, 16), client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func directDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if strings.EqualFold(host, "localhost") {
		// Do not let a changed resolver send an explicitly local model endpoint
		// to a non-loopback host while retaining its native API key.
		var last error
		for _, ip := range []string{"127.0.0.1", "::1"} {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, last
	}
	return dialer.DialContext(ctx, network, address)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	correlation := string(domain.NewID())
	operation := operationForPath(r.URL.Path)
	protocol := operation.protocol()
	w.Header().Set("X-Delidev-Correlation-Id", correlation)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int, code domain.Code) { writeError(w, status, protocol, code, correlation) }
	if r.TLS == nil {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			fail(http.StatusForbidden, domain.PermissionDenied)
			return
		}
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		fail(http.StatusForbidden, domain.PermissionDenied)
		return
	}
	token, err := executionToken(r.Header, protocol)
	if err != nil || h.authority == nil {
		fail(http.StatusUnauthorized, domain.Unauthenticated)
		return
	}
	// Claude's native beta Messages client uses this exact query spelling. It
	// selects the same scoped operation, not another route/provider. Preserve
	// it upstream; every other query (including equivalent encodings) fails.
	queryAllowed := r.URL.RawQuery == "" || (operation.protocol() == domain.AnthropicMessages && r.URL.RawQuery == "beta=true")
	if r.Method != http.MethodPost || operation == "" || !queryAllowed || r.URL.ForceQuery || r.URL.RawPath != "" || r.URL.Fragment != "" || r.URL.IsAbs() {
		fail(http.StatusNotFound, domain.Unsupported)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) > 1 {
		fail(http.StatusBadRequest, domain.InvalidArgument)
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (len(params) > 0 && (len(params) != 1 || !strings.EqualFold(params["charset"], "utf-8"))) || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		fail(http.StatusUnsupportedMediaType, domain.Unsupported)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	lease, err := h.authority.Acquire(ctx, token)
	if lease != nil && lease.Release != nil {
		defer lease.Release()
	}
	if err != nil {
		fail(errorStatus(err), safeCode(err))
		return
	}
	if lease == nil || lease.Release == nil || lease.Context == nil {
		fail(http.StatusServiceUnavailable, domain.Unavailable)
		return
	}
	if err := lease.Scope.Validate(); err != nil {
		fail(http.StatusServiceUnavailable, domain.Unsupported)
		return
	}
	if !lease.Scope.allows(operation) {
		fail(http.StatusForbidden, domain.PermissionDenied)
		return
	}
	stopRevocation := context.AfterFunc(lease.Context, cancel)
	defer stopRevocation()
	if lease.Context.Err() != nil {
		cancel()
	}
	if ctx.Err() != nil {
		fail(http.StatusUnauthorized, domain.Unauthenticated)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		fail(http.StatusTooManyRequests, domain.ResourceExhausted)
		return
	}
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(time.Now().Add(30*time.Second)) != nil {
		fail(http.StatusServiceUnavailable, domain.Unavailable)
		return
	}
	stopBody := context.AfterFunc(ctx, func() { _ = r.Body.Close(); _ = controller.SetWriteDeadline(time.Now()) })
	defer stopBody()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	defer clear(raw)
	if err != nil {
		fail(http.StatusBadRequest, domain.InvalidArgument)
		return
	}
	if len(raw) > maxBody {
		fail(http.StatusRequestEntityTooLarge, domain.ResourceExhausted)
		return
	}
	stream, err := validateRequest(ctx, raw, lease, operation)
	if err != nil {
		fail(errorStatus(err), safeCode(err))
		return
	}
	// After the complete native request is bounded/authorized, release its read
	// deadline. Streaming still has an overall deadline and bounded writes.
	_ = controller.SetReadDeadline(time.Time{})
	started := time.Now()
	status := 0
	code := domain.Code("")
	submitted := false
	phase := phaseCredential
	defer func() {
		h.logger.Info("api_proxy_request_finished", "correlation_id", correlation, "execution_id", lease.Scope.ExecutionID, "session_id", lease.Scope.SessionID, "account_id", lease.Scope.AccountID, "provider_id", lease.Scope.ProviderID, "model_id", lease.Scope.ModelID, "operation", operation, "phase", phase, "stream", stream, "submitted", submitted, "http_status", status, "error_code", code, "duration_ms", time.Since(started).Milliseconds())
	}()
	var key []byte
	if lease.Scope.Provider.Authentication != domain.KeylessAuth {
		if lease.Key == nil {
			code = domain.Unavailable
			fail(http.StatusServiceUnavailable, code)
			return
		}
		key, err = lease.Key(ctx)
		defer clear(key)
		if err != nil {
			code = safeCode(err)
			fail(errorStatus(err), code)
			return
		}
		if err = domain.ValidateAPIKey(key, false); err != nil {
			code = domain.Unavailable
			fail(http.StatusServiceUnavailable, code)
			return
		}
	}
	if ctx.Err() != nil {
		code = domain.Canceled
		fail(http.StatusUnauthorized, domain.Unauthenticated)
		return
	}
	guard := newSecretGuard(key, []byte(token))
	upstream, err := upstreamRequest(ctx, r, raw, lease.Scope, key, operation, stream, correlation)
	if err != nil {
		code = safeCode(err)
		fail(errorStatus(err), code)
		return
	}
	defer upstream.Header.Del("Authorization")
	defer upstream.Header.Del("x-api-key")
	submitted = true
	phase = phaseRequest
	response, err := h.client.Do(upstream)
	if err != nil {
		code = networkCode(err)
		fail(http.StatusBadGateway, code)
		return
	}
	defer response.Body.Close()
	status = response.StatusCode
	phase = phaseHeaders
	if status != http.StatusOK {
		// Keep only bounded closed native machine codes from error JSON. Never
		// relay diagnostic text, cookies, challenges, locations or request IDs.
		if value := safeRetryAfter(response.Header.Get("Retry-After"), time.Now()); value != "" && !guard.contains(value) {
			w.Header().Set("Retry-After", value)
		}
		code = providerCode(status)
		var nativeCode string
		code, nativeCode = readProviderError(response, code)
		outStatus := status
		if status < 400 || status > 599 {
			outStatus = http.StatusBadGateway
		}
		body := errorBodyWithNativeCode(protocol, code, correlation, nativeCode)
		if guard.contains(string(body)) {
			// A permitted machine code can itself equal the protected key.
			// Prefer the local classification; even that fixed body must pass
			// the guard before delivery. HTTP status still reports failure.
			body = errorBody(protocol, code, correlation)
			if guard.contains(string(body)) {
				body = nil
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(outStatus)
		_, _ = w.Write(body)
		return
	}
	responseType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || response.Header.Get("Content-Encoding") != "" || (stream && responseType != "text/event-stream") || (!stream && responseType != "application/json") {
		code = domain.Unsupported
		fail(http.StatusBadGateway, code)
		return
	}
	if stream {
		phase = phaseStream
		if started, err := relayStream(ctx, w, response.Body, operation, lease, guard, correlation); err != nil {
			var native *nativeFailure
			if errors.As(err, &native) {
				code = native.code
				return
			}
			code = networkCode(err)
			if !started {
				fail(http.StatusBadGateway, code)
				return
			}
			// Once native streaming has started, abort the HTTP response instead
			// of emitting a forged completion or replaying a provider operation.
			panic(http.ErrAbortHandler)
		}
		phase = phaseComplete
		return
	}
	phase = phaseBody
	result, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	defer clear(result)
	if err != nil || len(result) > maxBody {
		code = domain.Unavailable
		fail(http.StatusBadGateway, code)
		return
	}
	object, err := document(result, secretGuard{})
	if err != nil {
		code = domain.Unavailable
		fail(http.StatusBadGateway, code)
		return
	}
	if nonNull(object["error"]) {
		var nativeCode string
		code, nativeCode = nativeErrorCode(object["error"], domain.Unavailable)
		var nativeObject string
		_ = json.Unmarshal(object["object"], &nativeObject)
		if operation == ResponseCreate && nativeObject == "response" {
			object["error"] = nativeErrorValue(protocol, code, correlation, nativeCode)
			clear(result)
			result, _ = json.Marshal(object)
			defer clear(result)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write(errorBodyWithNativeCode(protocol, code, correlation, nativeCode))
			return
		}
	}
	if _, err = document(result, guard); err != nil {
		code = domain.Unavailable
		fail(http.StatusBadGateway, code)
		return
	}
	if err = observeReference(ctx, lease, operation, object); err != nil {
		code = safeCode(err)
		fail(http.StatusBadGateway, code)
		return
	}
	if ctx.Err() != nil {
		code = domain.Canceled
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if controller.SetWriteDeadline(time.Now().Add(clientWriteTimeout)) != nil {
		code = domain.Unavailable
		return
	}
	if _, err = w.Write(result); err != nil {
		code = networkCode(err)
	} else {
		phase = phaseComplete
	}
}

func executionToken(header http.Header, protocol domain.APIProtocol) (string, error) {
	var token string
	authorization, apiKey := header.Values("Authorization"), header.Values("X-Api-Key")
	switch {
	case len(authorization) == 1 && len(apiKey) == 0:
		if !strings.HasPrefix(authorization[0], "Bearer ") {
			return "", errors.New("invalid authorization")
		}
		token = strings.TrimPrefix(authorization[0], "Bearer ")
	case len(authorization) == 0 && len(apiKey) == 1:
		token = apiKey[0]
	case protocol == domain.AnthropicMessages && len(authorization) == 1 && len(apiKey) == 1:
		// The full native Claude profile explicitly pins both authentication
		// sources to the same execution credential so it cannot consult ambient
		// subscription state. Two different credentials remain ambiguous.
		if !strings.HasPrefix(authorization[0], "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authorization[0], "Bearer ")), []byte(apiKey[0])) != 1 {
			return "", errors.New("ambiguous authorization")
		}
		token = apiKey[0]
	default:
		return "", errors.New("ambiguous authorization")
	}
	if !ValidToken(token) {
		return "", errors.New("invalid execution credential")
	}
	return token, nil
}

func upstreamRequest(ctx context.Context, incoming *http.Request, raw []byte, scope Scope, key []byte, operation Operation, stream bool, correlation string) (*http.Request, error) {
	base, err := url.Parse(scope.Provider.Endpoint)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + strings.TrimPrefix(incoming.URL.Path, Prefix)
	base.RawQuery = incoming.URL.RawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	// Fresh HTTP/1 connections and a non-replayable body forbid transport-owned
	// retries, including a dropped response. Native harness retries remain native.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("HTTP-Referer", "https://deli.dev")
	req.Header.Set("User-Agent", "delidev/0.1.0")
	req.Header.Set("X-Client-Request-Id", correlation)
	switch scope.Provider.Authentication {
	case domain.BearerAuth:
		req.Header.Set("Authorization", "Bearer "+string(key))
	case domain.APIKeyAuth:
		req.Header.Set("x-api-key", string(key))
	}
	if operation.protocol() == domain.AnthropicMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
		if values := incoming.Header.Values("anthropic-beta"); len(values) > 0 {
			if len(values) != 1 || len(values[0]) > 1024 || values[0] == "" || strings.IndexFunc(values[0], func(r rune) bool { return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != ',' }) >= 0 {
				return nil, domain.Fail(domain.InvalidArgument, "Invalid native beta-header selection.", "Use bounded native Anthropic beta identifiers.")
			}
			req.Header.Set("anthropic-beta", values[0])
		}
	}
	return req, nil
}

func observeReference(ctx context.Context, lease *Lease, operation Operation, object map[string]json.RawMessage) error {
	if operation != ResponseCreate || lease.ObserveReference == nil {
		return nil
	}
	var id string
	if value, ok := object["id"]; ok {
		if json.Unmarshal(value, &id) != nil || domain.Text(id, "native response identity", 256, true) != nil {
			return errInvalidDocument
		}
		return lease.ObserveReference(ctx, ResponseReference, id)
	}
	return nil
}

func safeRetryAfter(value string, now time.Time) string {
	if len(value) > 128 {
		return ""
	}
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil && seconds <= 86400 {
		return strconv.FormatUint(seconds, 10)
	}
	if at, err := http.ParseTime(value); err == nil {
		seconds := int64(at.Sub(now).Seconds())
		if seconds >= 0 && seconds <= 86400 {
			return strconv.FormatInt(seconds, 10)
		}
	}
	return ""
}
