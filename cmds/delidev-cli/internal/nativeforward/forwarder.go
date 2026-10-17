// SPDX-License-Identifier: Apache-2.0
// Package nativeforward owns one execution-bound private loopback listener
// that carries a native Codex API request to the paired server relay through a
// caller-selected HTTP client. It forwards original native bytes, never creates
// a request, never retries, and never follows a redirect.
package nativeforward

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// Limits mirror the server relay so the forwarder neither widens nor narrows
// the accepted native request or response.
const (
	MaxRequestBody  int64 = 32 << 20
	MaxTitleBody    int64 = 1 << 20
	MaxResponseBody int64 = 256 << 20
	// MaxConcurrent matches the relay's concurrent request bound.
	MaxConcurrent = 16

	requestTimeout  = 15 * time.Minute
	bodyReadTimeout = 30 * time.Second
	// Body buffering precedes the relay request. The additional bounded
	// allowance covers desktop proof and outbound connection setup before
	// server admission; the server still enforces its own 15-minute lifetime.
	preRelayAllowance  = 30 * time.Second
	clientWriteTimeout = 30 * time.Second
	closeTimeout       = 5 * time.Second
	copyBufferSize     = 32 << 10
)

// Stage is the closed set of forwarding stages used in structured logs.
type Stage string

const (
	StageOpen     Stage = "open"
	StageRequest  Stage = "request"
	StageUpstream Stage = "upstream"
	StageStream   Stage = "response-stream"
	StageClose    Stage = "close"
)

// Config binds one forwarder to one original execution.
type Config struct {
	OpenRouterTools bool
	// Origin is the immutable paired server origin. It is only the request
	// target seed: Client's transport owns generation proof, endpoint rewrite
	// and bearer wrapping, and must never fall back to this address once the
	// desktop runtime has been followed.
	Origin string
	// Token is the execution credential. Only requests carrying exactly this
	// bearer are forwarded.
	Token string
	// Client sends every forwarded request. The forwarder replaces its redirect
	// policy and applies no client-level timeout so streams are not truncated.
	Client *http.Client
	// CloseIdle releases transport connections after every request is joined.
	CloseIdle func()
	// BodyLimit bounds the buffered native request body; zero selects the relay
	// default.
	BodyLimit int64
	Logger    *slog.Logger
	// ExecutionID is only a log correlation identifier.
	ExecutionID domain.ID
	// These narrow test seams preserve the production bounds in Open.
	relayTimeout         time.Duration
	preparationAllowance time.Duration
}

// Forwarder is a started listener. Close joins it and every in-flight request.
type Forwarder struct {
	config    Config
	origin    *url.URL
	client    http.Client
	listener  net.Listener
	server    *http.Server
	address   string
	life      context.Context
	cancel    context.CancelFunc
	slots     chan struct{}
	mu        sync.Mutex
	closed    bool
	handlers  sync.WaitGroup
	served    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func invalid() error {
	return domain.Fail(domain.Unsupported, "The owned Codex API forwarder is unavailable.", "Reconcile the original Worker pairing; native routing has no saved-address fallback.")
}

// Open starts the listener on an ephemeral 127.0.0.1 port.
func Open(ctx context.Context, config Config) (*Forwarder, error) {
	if ctx.Err() != nil || config.Client == nil || !apiproxy.ValidToken(config.Token) || rpc.ValidateEndpoint(config.Origin) != nil {
		return nil, invalid()
	}
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Path != "" && origin.Path != "/" || origin.RawPath != "" || origin.ForceQuery {
		return nil, invalid()
	}
	origin.Path = ""
	if config.BodyLimit < 0 {
		return nil, invalid()
	}
	if config.BodyLimit == 0 {
		config.BodyLimit = MaxRequestBody
	}
	if config.relayTimeout == 0 {
		config.relayTimeout = requestTimeout
	}
	if config.preparationAllowance == 0 {
		config.preparationAllowance = bodyReadTimeout + preRelayAllowance
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, invalid()
	}
	life, cancel := context.WithCancel(ctx)
	client := *config.Client
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	f := &Forwarder{config: config, origin: origin, client: client, listener: listener, address: listener.Addr().String(), life: life, cancel: cancel, slots: make(chan struct{}, MaxConcurrent), served: make(chan struct{})}
	f.server = &http.Server{
		Handler:           http.HandlerFunc(f.serve),
		ReadHeaderTimeout: bodyReadTimeout,
		MaxHeaderBytes:    32 << 10,
		BaseContext:       func(net.Listener) context.Context { return life },
		ErrorLog:          nil,
	}
	go func() {
		defer close(f.served)
		_ = f.server.Serve(listener)
	}()
	go func() {
		// Execution cancellation closes the listener and active requests; the
		// owner still joins them through Close.
		<-life.Done()
		_ = f.server.Close()
	}()
	f.log(slog.LevelInfo, "worker_native_forward_opened", StageOpen, "", "")
	return f, nil
}

// NativeOrigin is the private origin handed to the native process. It carries
// no credential; the native execution token remains the only bearer.
func (f *Forwarder) NativeOrigin() string { return "http://" + f.address }

func (f *Forwarder) log(level slog.Level, message string, stage Stage, code domain.Code, correlation string) {
	if f.config.Logger == nil {
		return
	}
	// Closed stage/code/correlation fields only: never endpoints, tokens, paths
	// or native content.
	attributes := []any{"execution_id", f.config.ExecutionID, "stage", string(stage)}
	if code != "" {
		attributes = append(attributes, "code", string(code))
	}
	if correlation != "" {
		attributes = append(attributes, "correlation_id", correlation)
	}
	f.config.Logger.Log(context.Background(), level, message, attributes...)
}

// Close stops accepting, cancels active requests and streams, and waits for
// every handler to return. A timeout is reported as unconfirmed cleanup.
func (f *Forwarder) Close() error {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		f.cancel()
		_ = f.server.Close()
		joined := make(chan struct{})
		go func() {
			<-f.served
			f.handlers.Wait()
			close(joined)
		}()
		select {
		case <-joined:
			if f.config.CloseIdle != nil {
				f.config.CloseIdle()
			}
		case <-time.After(closeTimeout):
			f.closeErr = domain.Fail(domain.RecoveryRequired, "The owned native API forwarder cleanup is unconfirmed.", "Retain original runtime ownership until every forwarded request and stream is joined.")
			f.log(slog.LevelWarn, "worker_native_forward_close_unconfirmed", StageClose, domain.RecoveryRequired, "")
		}
	})
	return f.closeErr
}

func allowedPath(path string) bool {
	return path == apiproxy.Prefix+"/responses" || path == apiproxy.Prefix+"/responses/compact"
}

var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHop(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		header.Del(name)
	}
}

type failureBody struct {
	Error struct {
		Type          string `json:"type"`
		Code          string `json:"code"`
		Message       string `json:"message"`
		CorrelationID string `json:"correlation_id"`
	} `json:"error"`
}

func (f *Forwarder) fail(w http.ResponseWriter, status int, code domain.Code, stage Stage, correlation string) {
	var body failureBody
	body.Error.Type = "server_error"
	body.Error.Code = string(code)
	body.Error.Message = "The DeliDev native API route could not complete the request."
	body.Error.CorrelationID = correlation
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
	f.log(slog.LevelWarn, "worker_native_forward_failed", stage, code, correlation)
}

func (f *Forwarder) serve(w http.ResponseWriter, r *http.Request) {
	correlation := string(domain.NewID())
	w.Header().Set("X-Delidev-Correlation-Id", correlation)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		f.fail(w, http.StatusServiceUnavailable, domain.Unavailable, StageRequest, correlation)
		return
	}
	f.handlers.Add(1)
	f.mu.Unlock()
	defer f.handlers.Done()

	host, _, hostErr := net.SplitHostPort(r.RemoteAddr)
	if hostErr != nil || !net.ParseIP(host).IsLoopback() || r.Host != f.address {
		f.fail(w, http.StatusForbidden, domain.PermissionDenied, StageRequest, correlation)
		return
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Upgrade") != "" {
		f.fail(w, http.StatusForbidden, domain.PermissionDenied, StageRequest, correlation)
		return
	}
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 || len(r.Header.Values("X-Api-Key")) != 0 || subtle.ConstantTimeCompare([]byte(authorization[0]), []byte("Bearer "+f.config.Token)) != 1 {
		f.fail(w, http.StatusUnauthorized, domain.Unauthenticated, StageRequest, correlation)
		return
	}
	if r.Method != http.MethodPost || !(allowedPath(r.URL.Path) || f.config.OpenRouterTools && (r.URL.Path == apiproxy.Prefix+"/images/generations" || r.URL.Path == apiproxy.Prefix+"/images/edits" || r.URL.Path == apiproxy.Prefix+"/alpha/search")) || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.URL.Fragment != "" || r.URL.IsAbs() {
		f.fail(w, http.StatusNotFound, domain.Unsupported, StageRequest, correlation)
		return
	}
	select {
	case f.slots <- struct{}{}:
		defer func() { <-f.slots }()
	default:
		f.fail(w, http.StatusTooManyRequests, domain.ResourceExhausted, StageRequest, correlation)
		return
	}
	// The relay starts its 15-minute clock only after it accepts headers.
	// Local body buffering, runtime proof and connection setup happen first.
	// The bounded allowance prevents that preparation from consuming an
	// otherwise valid relay response. Caller and execution cancellation still
	// terminate this same original request.
	ctx, cancel := context.WithTimeout(r.Context(), f.config.relayTimeout+f.config.preparationAllowance)
	defer cancel()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(bodyReadTimeout))
	bodyStopped := make(chan struct{})
	stopBody := context.AfterFunc(ctx, func() {
		defer close(bodyStopped)
		_ = r.Body.Close()
		_ = controller.SetWriteDeadline(time.Now())
	})
	defer func() {
		// Join the callback so a late deadline cannot touch a reused connection.
		if !stopBody() {
			<-bodyStopped
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(r.Body, f.config.BodyLimit+1))
	defer clear(raw)
	if err != nil {
		f.fail(w, http.StatusBadRequest, domain.InvalidArgument, StageRequest, correlation)
		return
	}
	if int64(len(raw)) > f.config.BodyLimit {
		f.fail(w, http.StatusRequestEntityTooLarge, domain.ResourceExhausted, StageRequest, correlation)
		return
	}
	_ = controller.SetReadDeadline(time.Time{})

	target := *f.origin
	target.Path = r.URL.Path
	// NopCloser leaves GetBody unset, so the HTTP stack cannot replay the body
	// on a stale connection; the transport proves the runtime before any bytes.
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		f.fail(w, http.StatusInternalServerError, domain.Internal, StageRequest, correlation)
		return
	}
	upstream.ContentLength = int64(len(raw))
	upstream.Header = r.Header.Clone()
	stripHop(upstream.Header)
	for _, name := range []string{"Content-Length", "Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		upstream.Header.Del(name)
	}
	response, err := f.client.Do(upstream)
	if err != nil {
		if ctx.Err() != nil {
			// Cancellation, deadline or close: the native caller is gone or the
			// execution is ending. Report a classified closed code only.
			f.log(slog.LevelInfo, "worker_native_forward_canceled", StageUpstream, domain.Canceled, correlation)
			if r.Context().Err() == nil {
				f.fail(w, http.StatusGatewayTimeout, domain.Unavailable, StageUpstream, correlation)
			}
			return
		}
		code := domain.SafeError(err).Code
		if code == "" {
			code = domain.Unavailable
		}
		// The checked runtime may be missing, foreign or retired. The request
		// failed before bearer transmission or after an uncertain send; either
		// way, it is never resent and never redirected to another address.
		f.fail(w, http.StatusServiceUnavailable, code, StageUpstream, correlation)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		f.fail(w, http.StatusBadGateway, domain.Unsupported, StageUpstream, correlation)
		return
	}
	stripHop(response.Header)
	response.Header.Del("Set-Cookie")
	out := w.Header()
	for name, values := range response.Header {
		out[name] = append([]string(nil), values...)
	}
	out.Del("Content-Length")
	out.Set("X-Delidev-Correlation-Id", correlation)
	_ = controller.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
	w.WriteHeader(response.StatusCode)
	_ = controller.Flush()
	buffer := make([]byte, copyBufferSize)
	var total int64
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > MaxResponseBody {
				f.log(slog.LevelWarn, "worker_native_forward_failed", StageStream, domain.ResourceExhausted, correlation)
				panic(http.ErrAbortHandler)
			}
			_ = controller.SetWriteDeadline(time.Now().Add(clientWriteTimeout))
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return
			}
			// A truncated upstream stream must not look like clean completion.
			code := domain.Unavailable
			if ctx.Err() != nil {
				code = domain.Canceled
			}
			f.log(slog.LevelWarn, "worker_native_forward_failed", StageStream, code, correlation)
			panic(http.ErrAbortHandler)
		}
	}
}

// ValidNativeOrigin reports whether value is a canonical forwarder origin: an
// IPv4 loopback http origin with an explicit numeric port and nothing else.
func ValidNativeOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	return err == nil && port != 0 && strconv.FormatUint(port, 10) == u.Port() && u.Host == "127.0.0.1:"+u.Port()
}
