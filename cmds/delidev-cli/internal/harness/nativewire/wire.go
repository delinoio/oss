// Package nativewire provides bounded private JSON-RPC stdio for native harness
// adapters. It is never a public RPC endpoint and never logs protocol content.
package nativewire

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const MaxFrame = 1 << 20
const maxPending = 128
const maxEventBytes = 8 << 20
const maxRequests = 4096

// Profile names one fixed transport profile. Each profile owns its frame, queued
// byte and queued event-count bounds; none of them is configurable by callers.
type Profile string

const (
	// ProfileCodex is the ordinary omitted-version Codex profile.
	ProfileCodex Profile = "codex"
	// ProfileImage is the Codex profile that admits bounded image observations.
	ProfileImage Profile = "image"
	// ProfileJSONRPC is the explicit JSON-RPC 2.0 profile used by Grok.
	ProfileJSONRPC Profile = "jsonrpc"
)

// Queued event-count bounds. Codex streams fragment one response into many small
// notifications while the Worker publishes each event durably before reading the
// next, so its profiles tolerate a larger burst than the JSON-RPC profile.
// Each slot reserves one Event descriptor (144 bytes on 64-bit targets), so the
// Codex queue reserves at most 65,536 * sizeof(Event) bytes of descriptors in
// addition to the separately bounded queued payload bytes.
const (
	maxCodexEvents   = 65536
	maxJSONRPCEvents = 128
)

// LimitKind is the closed classification of a transport bound that failed a
// connection with ResourceExhausted.
type LimitKind string

const (
	LimitEventCount LimitKind = "event_count"
	LimitEventBytes LimitKind = "event_bytes"
	LimitFrameBytes LimitKind = "frame_bytes"
)

// profileLimits holds the bounds selected by one profile.
type profileLimits struct {
	jsonrpc    string
	frameBytes int
	queueBytes int
	eventCount int
}

func limitsFor(profile Profile) profileLimits {
	switch profile {
	case ProfileJSONRPC:
		return profileLimits{jsonrpc: "2.0", frameBytes: MaxFrame, queueBytes: maxEventBytes, eventCount: maxJSONRPCEvents}
	case ProfileImage:
		return profileLimits{frameBytes: 16 << 20, queueBytes: 64 << 20, eventCount: maxCodexEvents}
	default:
		return profileLimits{frameBytes: MaxFrame, queueBytes: 32 << 20, eventCount: maxCodexEvents}
	}
}

type EventKind string

const (
	Notification  EventKind = "notification"
	ServerRequest EventKind = "server-request"
	LateResponse  EventKind = "late-response"
)

type Event struct {
	Kind        EventKind
	Method      string
	ID          json.RawMessage
	Token       domain.ID
	Params      json.RawMessage
	Response    Response
	EmittedAtMS *int64
	size        int
}
type Response struct {
	Result    json.RawMessage
	ErrorCode *int64
}
type envelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
	// Codex app-server wraps native notifications with their emission time.
	// Preserve this optional provenance without treating it as an ordering ID.
	EmittedAtMS *int64 `json:"emittedAtMs,omitempty"`
}
type wireError struct {
	Code    *int64          `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}
type pending struct {
	response  chan Response
	abandoned bool
}
type incoming struct {
	token    domain.ID
	replying bool
}

type Connection struct {
	profile         Profile
	inputFrameLimit int
	eventByteLimit  int
	protected       security.ProtectedJSON
	jsonrpc         string
	process         *process.Handle
	cancel          context.CancelFunc
	done            chan struct{}
	writeGate       chan struct{}
	events          chan Event
	mu              sync.Mutex
	pending         map[string]*pending
	seen            map[domain.ID]bool
	incoming        map[string]incoming
	eventBytes      int
	problem         *domain.Error
	cleanup         error
	closing         bool
	eofOnce         sync.Once
	eofDone         chan struct{}
	logger          *slog.Logger
}

func protocolFailure() *domain.Error {
	return domain.Fail(domain.Unsupported, "The native harness sent an incompatible protocol message.", "Stop and reconcile the native session; use a supported installed harness version.")
}
func uncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native request delivery is uncertain.", "Inspect the native session and reconcile this operation before any retry; never replay blindly.")
}

// Start crosses the durable native start barrier, but sends no harness request.
// The adapter must complete its handshake before exposing product operations.
func Start(ctx context.Context, config process.Config) (*Connection, error) {
	return startBounded(ctx, config, ProfileCodex)
}

// StartJSONRPC requires explicit JSON-RPC 2.0 envelopes in both directions.
// Codex's existing omitted-version profile remains independent.
func StartJSONRPC(ctx context.Context, config process.Config) (*Connection, error) {
	return startBounded(ctx, config, ProfileJSONRPC)
}

// StartImageObservations admits bounded native base64 image notifications only
// for the explicitly selected original image adapter. Ordinary profiles retain
// their one-MiB frame bounds, with a 32-MiB aggregate queue for Codex and
// an eight-MiB aggregate queue for JSON-RPC.
func StartImageObservations(ctx context.Context, config process.Config) (*Connection, error) {
	return startBounded(ctx, config, ProfileImage)
}

// newConnection builds an unstarted connection whose channel capacity and
// admission checks share one per-profile event-count value.
func newConnection(profile Profile, cancel context.CancelFunc, logger *slog.Logger, protectedValues []string) *Connection {
	limits := limitsFor(profile)
	if logger == nil {
		logger = slog.Default()
	}
	return &Connection{profile: profile, inputFrameLimit: limits.frameBytes, eventByteLimit: limits.queueBytes, jsonrpc: limits.jsonrpc, cancel: cancel, done: make(chan struct{}), writeGate: make(chan struct{}, 1), events: make(chan Event, limits.eventCount), pending: map[string]*pending{}, seen: map[domain.ID]bool{}, incoming: map[string]incoming{}, logger: logger, protected: security.NewProtectedJSON(protectedValues)}
}
func startBounded(ctx context.Context, config process.Config, profile Profile) (*Connection, error) {
	life, cancel := context.WithCancel(ctx)
	c := newConnection(profile, cancel, config.Logger, config.ProtectedValues)
	version := c.jsonrpc
	config.Stdout = &frameWriter{connection: c}
	config.Stderr = io.Discard
	if version != "" {
		config.Stderr = &boundedDiagnostic{connection: c}
	}
	h, err := process.Start(life, config)
	if err != nil {
		cancel()
		return nil, err
	}
	c.process = h
	go func() {
		err := h.Wait()
		cleanup := h.Close()
		c.mu.Lock()
		c.cleanup = cleanup
		if cleanup != nil {
			c.problem = uncertain()
		} else if c.problem == nil && !c.closing {
			c.problem = domain.Fail(domain.Unavailable, "The native harness connection ended.", "Inspect the accepted native operation and resume explicitly after reconciliation.")
			if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
				c.problem = uncertain()
			}
		}
		c.mu.Unlock()
		cancel()
		close(c.done)
	}()
	if err := h.Resume(); err != nil {
		c.fail(domain.SafeError(err))
		if cleanup := c.Close(); cleanup != nil {
			return nil, uncertain()
		}
		return nil, err
	}
	return c, nil
}
func (c *Connection) fail(problem *domain.Error) {
	c.mu.Lock()
	if c.problem == nil {
		c.problem = problem
	}
	c.mu.Unlock()
	c.cancel()
}
func (c *Connection) status() *domain.Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.problem != nil {
		return c.problem
	}
	if c.closing {
		return domain.Fail(domain.Canceled, "The native connection is closing.", "Reconcile the previous execution before opening a replacement.")
	}
	return nil
}
func (c *Connection) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.cancel()
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cleanup
}

// CloseGracefully fences new writes and gives the original process a bounded
// stdin EOF window. Codex needs normal exit to drop its temporary helper aliases;
// forced cancellation skips those destructors. Timeout and independent failure
// still stop and join the retained original owner, never a replacement process.
func (c *Connection) CloseGracefully(grace time.Duration) error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.eofOnce.Do(func() {
		c.eofDone = make(chan struct{})
		go func() {
			defer close(c.eofDone)
			select {
			case c.writeGate <- struct{}{}:
				defer c.release()
				if err := c.process.CloseInput(); err != nil {
					c.cancel()
				}
			case <-c.done:
			}
		}()
	})
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-timer.C:
		c.logger.Info("native_graceful_shutdown_timeout")
		c.cancel()
		<-c.done
	}
	<-c.eofDone
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger.Info("native_graceful_shutdown_joined", "cleanup_confirmed", c.cleanup == nil)
	return c.cleanup
}
func (c *Connection) Done() <-chan struct{}     { return c.done }
func (c *Connection) Identity() process.Process { return c.process.Identity() }
func (c *Connection) Err() *domain.Error        { return c.status() }

func (c *Connection) Next(ctx context.Context) (Event, error) {
	for {
		if problem := c.status(); problem != nil {
			return Event{}, problem
		}
		select {
		case event := <-c.events:
			c.mu.Lock()
			c.eventBytes -= event.size
			c.mu.Unlock()
			if problem := c.status(); problem != nil {
				return Event{}, problem
			}
			return event, nil
		case <-ctx.Done():
			return Event{}, domain.SafeError(ctx.Err())
		case <-c.done:
		}
	}
}

// queueLocked admits one event without ever blocking the sole stdout reader:
// the count check against the channel capacity guarantees a free slot because
// this is the only sender. The count bound is checked before
// the byte bound, so an event exceeding both is classified as event_count.
func (c *Connection) queueLocked(event Event) error {
	if len(c.events) >= c.countLimit() {
		c.logLimit(LimitEventCount, len(c.events)+1, c.countLimit())
		return queueExhausted()
	}
	if c.eventBytes+event.size > c.eventLimit() {
		c.logLimit(LimitEventBytes, c.eventBytes+event.size, c.eventLimit())
		return queueExhausted()
	}
	c.eventBytes += event.size
	c.events <- event
	return nil
}
func queueExhausted() *domain.Error {
	return domain.Fail(domain.ResourceExhausted, "Native event buffering reached its bound.", "Reconcile the native session before reconnecting; reduce consumer lag.")
}

// logLimit records one structured record per exhausted bound. It carries only a
// closed classification and counters, never frame bodies, prompts or native text.
func (c *Connection) logLimit(kind LimitKind, observed, maximum int) {
	c.logger.Warn("native_limit_exceeded", "limit_kind", kind, "profile", c.profile, "observed", observed, "maximum", maximum)
}

// Call never retries. The caller allocates and persists the operation identity
// before calling when the method has side effects. Timed-out sent calls remain
// tracked and their definitive replies are delivered as LateResponse events.
func (c *Connection) Call(ctx context.Context, id domain.ID, method string, params any) (Response, error) {
	if err := id.Validate(); err != nil {
		return Response{}, err
	}
	raw, err := marshal(envelope{JSONRPC: c.jsonrpc, ID: json.RawMessage(`"` + string(id) + `"`), Method: method}, params)
	if err != nil {
		return Response{}, err
	}
	if err := c.acquire(ctx); err != nil {
		return Response{}, err
	}
	key := "s:" + string(id)
	p := &pending{response: make(chan Response, 1)}
	c.mu.Lock()
	if c.seen[id] {
		c.mu.Unlock()
		c.release()
		return Response{}, domain.Fail(domain.Conflict, "This native request identity was already used.", "Reconcile the accepted request instead of sending it again.")
	}
	if len(c.pending) >= maxPending || len(c.seen) >= maxRequests {
		c.mu.Unlock()
		c.release()
		return Response{}, domain.Fail(domain.ResourceExhausted, "Native request tracking reached its bound.", "Drain and reconcile pending operations before opening a new connection.")
	}
	c.pending[key], c.seen[id] = p, true
	c.mu.Unlock()
	err = c.write(ctx, raw)
	c.release()
	if err != nil {
		return Response{}, err
	}
	select {
	case response := <-p.response:
		return response, nil
	case <-ctx.Done():
	case <-c.done:
	}
	c.mu.Lock()
	if _, exists := c.pending[key]; !exists {
		c.mu.Unlock()
		// Response delivery under the same lock wins over concurrent timeout.
		return <-p.response, nil
	}
	p.abandoned = true
	c.mu.Unlock()
	return Response{}, uncertain()
}
func (c *Connection) Notify(ctx context.Context, method string, params any) error {
	raw, err := marshal(envelope{JSONRPC: c.jsonrpc, Method: method}, params)
	if err != nil {
		return err
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return c.write(ctx, raw)
}

// Reply only sends a response to the exact outstanding server request. The
// adapter validates the native question/approval and persists delivery state.
// Successful pipe delivery is not proof of native semantic acceptance.
func (c *Connection) Reply(ctx context.Context, event Event, result any) error {
	key, err := idKey(event.ID)
	if err != nil || event.Kind != ServerRequest {
		return domain.Fail(domain.InvalidArgument, "A native server request is required.", "Reply to the original pending interaction.")
	}
	raw, err := c.resultEnvelope(event, result)
	if err != nil {
		return err
	}
	return c.replyEnvelope(ctx, event, key, raw)
}

func (c *Connection) resultEnvelope(event Event, result any) ([]byte, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid native response.", "Use the original interaction's typed response schema.")
	}
	raw, err := json.Marshal(envelope{JSONRPC: c.jsonrpc, ID: event.ID, Result: resultJSON})
	if err != nil || len(raw) > MaxFrame {
		return nil, domain.Fail(domain.ResourceExhausted, "Native response exceeds its bound.", "Reduce the response size.")
	}
	return raw, nil
}

// ReplyGenerated claims the exact outstanding arrival before producing a value.
// The producer must be a bounded synchronous operation with no native calls.
// A stale, canceled or replaced arrival never invokes it. Once claimed, even a
// production/encoding failure retains uncertainty and cannot authorize resampling.
func (c *Connection) ReplyGenerated(ctx context.Context, event Event, produce func() (any, error)) error {
	key, err := idKey(event.ID)
	if err != nil || event.Kind != ServerRequest || event.Token.Validate() != nil || produce == nil {
		return domain.Fail(domain.InvalidArgument, "The original native server request is required.", "Retain its exact request and arrival identity before responding.")
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	if err := c.claimReply(event, key); err != nil {
		return err
	}
	result, err := produce()
	if err != nil {
		c.fail(uncertain())
		return uncertain()
	}
	raw, err := c.resultEnvelope(event, result)
	var checked envelope
	if err != nil || domain.Decode(raw, &checked) != nil {
		c.fail(uncertain())
		return uncertain()
	}
	return c.sendClaimedReply(ctx, key, raw)
}

// RefuseExternalTokenRefresh answers only the exact pending token callback with a fixed safe error.
func (c *Connection) RefuseExternalTokenRefresh(ctx context.Context, event Event) error {
	key, err := idKey(event.ID)
	if err != nil || event.Kind != ServerRequest || event.Method != "account/chatgptAuthTokens/refresh" {
		return protocolFailure()
	}
	code, message := int64(-32000), "External token refresh is disabled for quota observation."
	raw, err := json.Marshal(envelope{JSONRPC: c.jsonrpc, ID: event.ID, Error: &wireError{Code: &code, Message: &message}})
	if err != nil {
		return protocolFailure()
	}
	return c.replyEnvelope(ctx, event, key, raw)
}

func (c *Connection) replyEnvelope(ctx context.Context, event Event, key string, raw []byte) error {
	var checked envelope
	if err := domain.Decode(raw, &checked); err != nil {
		return err
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	if err := c.claimReply(event, key); err != nil {
		return err
	}
	return c.sendClaimedReply(ctx, key, raw)
}

func (c *Connection) claimReply(event Event, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.problem != nil {
		return c.problem
	}
	if c.closing {
		return domain.Fail(domain.Canceled, "The native connection is closing.", "Reconcile the previous execution before opening a replacement.")
	}
	original, ok := c.incoming[key]
	if !ok || original.token != event.Token || original.replying {
		return domain.Fail(domain.Conflict, "The native interaction was already answered or replaced.", "Reload the current interaction before responding.")
	}
	original.replying = true
	c.incoming[key] = original
	return nil
}

func (c *Connection) sendClaimedReply(ctx context.Context, key string, raw []byte) error {
	if err := c.write(ctx, raw); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.incoming, key)
	c.mu.Unlock()
	return nil
}

// RetireRequest invalidates only this exact arrival when an adapter observes
// native cancellation/resolution. It sends no answer and proves no semantic
// acceptance. False means the arrival is no longer pending, which may include
// successful pipe transmission. A replacement or in-flight reply is never
// removed: its ownership must be reconciled separately.
func (c *Connection) RetireRequest(event Event) (bool, error) {
	key, err := idKey(event.ID)
	if err != nil || event.Kind != ServerRequest || event.Token.Validate() != nil {
		return false, domain.Fail(domain.InvalidArgument, "The original native server request is required.", "Retain its exact request and arrival identity before resolution.")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	original, exists := c.incoming[key]
	if !exists {
		return false, nil
	}
	if original.token != event.Token || original.replying {
		return false, domain.Fail(domain.Conflict, "The native request was replaced or its response is in flight.", "Reconcile the original arrival without invalidating another response.")
	}
	delete(c.incoming, key)
	return true, nil
}
func marshal(message envelope, params any) ([]byte, error) {
	if err := domain.Text(message.Method, "native method", 256, true); err != nil {
		return nil, err
	}
	var raw []byte
	var err error
	if _, omitted := params.(OmittedParams); !omitted {
		raw, err = json.Marshal(params)
		if err != nil {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid native request parameters.", "Use the adapter's typed request schema.")
		}
		if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
			return nil, domain.Fail(domain.InvalidArgument, "Native parameters must be a structured object or array.", "Use the adapter's typed parameter schema.")
		}
		message.Params = raw
	}
	raw, err = json.Marshal(message)
	if err != nil || len(raw) > MaxFrame {
		return nil, domain.Fail(domain.ResourceExhausted, "Native request exceeds its bound.", "Reduce the request size.")
	}
	var checked envelope
	if err := domain.Decode(raw, &checked); err != nil {
		return nil, err
	}
	return raw, nil
}
func (c *Connection) acquire(ctx context.Context) error {
	select {
	case c.writeGate <- struct{}{}:
		if ctx.Err() != nil {
			c.release()
			return domain.SafeError(ctx.Err())
		}
		if problem := c.status(); problem != nil {
			c.release()
			return problem
		}
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	case <-c.done:
		return domain.Fail(domain.Unavailable, "The native connection has ended.", "Inspect and resume the native session explicitly.")
	}
}
func (c *Connection) release() { <-c.writeGate }
func (c *Connection) write(ctx context.Context, raw []byte) error {
	finished := make(chan error, 1)
	go func() { _, err := c.process.Write(append(raw, '\n')); finished <- err }()
	select {
	case err := <-finished:
		if err == nil {
			return nil
		}
	case <-ctx.Done():
	case <-c.done:
	}
	// A blocked native input write cannot leave an unbounded writer goroutine or
	// authorize a duplicate send. Stopping the owned process releases its pipe.
	c.fail(uncertain())
	<-c.done
	return uncertain()
}

var integerID = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,18})$`)

func idKey(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > 256 {
		return "", protocolFailure()
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", protocolFailure()
		}
		if err := domain.Text(value, "native request identity", 128, true); err != nil {
			return "", protocolFailure()
		}
		return "s:" + value, nil
	}
	if integerID.Match(raw) {
		return "n:" + string(raw), nil
	}
	return "", protocolFailure()
}
func (c *Connection) rejectProtocol(reason string) error {
	c.logger.Warn("native_protocol_rejected", "profile", c.profile, "reason", reason)
	return protocolFailure()
}
func (c *Connection) receive(raw []byte) error {
	if !c.protected.Safe(raw) {
		return domain.Fail(domain.PermissionDenied, "The native protocol reflected protected runtime authority.", "Stop and reconcile the original runtime without publishing its private output.")
	}
	var message envelope
	if err := domain.DecodeBounded(raw, &message, c.frameLimit()); err != nil {
		return c.rejectProtocol("envelope")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return c.rejectProtocol("object")
	}
	_, hasMethod := fields["method"]
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	_, hasParams := fields["params"]
	if message.JSONRPC != "" && message.JSONRPC != "2.0" {
		return c.rejectProtocol("jsonrpc-version")
	}
	if c.jsonrpc != "" {
		if string(fields["jsonrpc"]) != `"2.0"` {
			return c.rejectProtocol("jsonrpc-required")
		}
		for key := range fields {
			switch key {
			case "jsonrpc", "id", "method", "params", "result", "error":
			default:
				return c.rejectProtocol("jsonrpc-extra-field")
			}
		}
		if hasError {
			var errorFields map[string]json.RawMessage
			if json.Unmarshal(fields["error"], &errorFields) != nil {
				return c.rejectProtocol("error-object")
			}
			for key := range errorFields {
				if key != "code" && key != "message" && key != "data" {
					return c.rejectProtocol("error-extra-field")
				}
			}
		}
	}
	if message.EmittedAtMS != nil && (*message.EmittedAtMS < 0 || *message.EmittedAtMS > 253402300799999) {
		return c.rejectProtocol("emitted-time")
	}
	key := ""
	if len(message.ID) > 0 {
		var err error
		key, err = idKey(message.ID)
		if err != nil {
			return c.rejectProtocol("request-id")
		}
	}
	if hasMethod {
		if domain.Text(message.Method, "native method", 256, true) != nil || hasResult || hasError {
			return c.rejectProtocol("notification-shape")
		}
		event := Event{Kind: Notification, Method: message.Method, Params: message.Params, EmittedAtMS: message.EmittedAtMS, size: len(raw)}
		c.mu.Lock()
		defer c.mu.Unlock()
		if key != "" {
			if _, exists := c.incoming[key]; exists {
				return c.rejectProtocol("duplicate-arrival")
			}
			if len(c.incoming) >= maxPending {
				return domain.Fail(domain.ResourceExhausted, "Too many pending native interactions.", "Resolve or cancel existing native interactions before resuming.")
			}
			event.Kind, event.ID, event.Token = ServerRequest, message.ID, domain.NewID()
			c.incoming[key] = incoming{token: event.Token}
		}
		return c.queueLocked(event)
	}
	if key == "" || hasParams || hasResult == hasError || (hasError && (message.Error == nil || message.Error.Code == nil || message.Error.Message == nil)) {
		return c.rejectProtocol("response-shape")
	}
	response := Response{Result: message.Result}
	if message.Error != nil {
		code := *message.Error.Code
		response.ErrorCode = &code
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, exists := c.pending[key]
	if !exists {
		return c.rejectProtocol("orphan-response")
	}
	delete(c.pending, key)
	if p.abandoned {
		return c.queueLocked(Event{Kind: LateResponse, ID: message.ID, Response: response, size: len(raw)})
	}
	p.response <- response
	return nil
}

type frameWriter struct {
	connection *Connection
	buffer     []byte
	stopped    bool
}

const maxDiagnosticBytes = 64 << 10

type boundedDiagnostic struct {
	connection *Connection
	count      int
}

func (w *boundedDiagnostic) Write(data []byte) (int, error) {
	if len(data) > maxDiagnosticBytes-w.count {
		w.connection.fail(domain.Fail(domain.ResourceExhausted, "Native diagnostic output exceeded its bound.", "Reconcile the native process and inspect its supported protocol profile."))
	}
	w.count = min(maxDiagnosticBytes, w.count+min(maxDiagnosticBytes, len(data)))
	return len(data), nil
}

func (w *frameWriter) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 && !w.stopped {
		end := bytes.IndexByte(data, '\n')
		size := len(data)
		if end >= 0 {
			size = end
		}
		if len(w.buffer)+size > w.connection.frameLimit() {
			w.stopped = true
			// Report the first byte past the bound, independent of how the pipe chunked the data.
			w.connection.logLimit(LimitFrameBytes, w.connection.frameLimit()+1, w.connection.frameLimit())
			w.buffer = nil
			w.connection.fail(domain.Fail(domain.ResourceExhausted, "A native protocol frame exceeds its bound.", "Use a supported harness output size and reconcile the interrupted session."))
			break
		}
		w.buffer = append(w.buffer, data[:size]...)
		data = data[size:]
		if end < 0 {
			break
		}
		data = data[1:]
		if err := w.connection.receive(w.buffer); err != nil {
			w.stopped = true
			w.connection.fail(domain.SafeError(err))
		}
		w.buffer = nil
	}
	// Continue draining until native ownership cleanup confirms termination.
	return length, nil
}

// OmittedParams is an explicit adapter-owned no-parameter protocol profile.
// Ordinary nil/scalar parameters remain invalid; this never changes inbound validation.
type OmittedParams struct{}

func (c *Connection) frameLimit() int {
	if c.inputFrameLimit == 0 {
		return MaxFrame
	}
	return c.inputFrameLimit
}

// countLimit is the channel capacity itself, so allocation and admission can
// never disagree about the per-connection event-count bound.
func (c *Connection) countLimit() int { return cap(c.events) }
func (c *Connection) eventLimit() int {
	if c.eventByteLimit == 0 {
		return maxEventBytes
	}
	return c.eventByteLimit
}
