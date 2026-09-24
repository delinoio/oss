// Package nativewire provides bounded private JSON-RPC stdio for native harness
// adapters. It is never a public RPC endpoint and never logs protocol content.
package nativewire

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const MaxFrame = 1 << 20
const maxPending = 128
const maxEvents = 128
const maxEventBytes = 8 << 20
const maxRequests = 4096

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
	process    *process.Handle
	cancel     context.CancelFunc
	done       chan struct{}
	writeGate  chan struct{}
	events     chan Event
	mu         sync.Mutex
	pending    map[string]*pending
	seen       map[domain.ID]bool
	incoming   map[string]incoming
	eventBytes int
	problem    *domain.Error
	cleanup    error
	closing    bool
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
	life, cancel := context.WithCancel(ctx)
	c := &Connection{cancel: cancel, done: make(chan struct{}), writeGate: make(chan struct{}, 1), events: make(chan Event, maxEvents), pending: map[string]*pending{}, seen: map[domain.ID]bool{}, incoming: map[string]incoming{}}
	config.Stdout = &frameWriter{connection: c}
	config.Stderr = io.Discard
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
func (c *Connection) queueLocked(event Event) error {
	if len(c.events) >= maxEvents || c.eventBytes+event.size > maxEventBytes {
		return domain.Fail(domain.ResourceExhausted, "Native event buffering reached its bound.", "Reconcile the native session before reconnecting; reduce consumer lag.")
	}
	c.eventBytes += event.size
	c.events <- event
	return nil
}

// Call never retries. The caller allocates and persists the operation identity
// before calling when the method has side effects. Timed-out sent calls remain
// tracked and their definitive replies are delivered as LateResponse events.
func (c *Connection) Call(ctx context.Context, id domain.ID, method string, params any) (Response, error) {
	if err := id.Validate(); err != nil {
		return Response{}, err
	}
	raw, err := marshal(envelope{ID: json.RawMessage(`"` + string(id) + `"`), Method: method}, params)
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
	raw, err := marshal(envelope{Method: method}, params)
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
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid native response.", "Use the original interaction's typed response schema.")
	}
	raw, err := json.Marshal(envelope{ID: event.ID, Result: resultJSON})
	if err != nil || len(raw) > MaxFrame {
		return domain.Fail(domain.ResourceExhausted, "Native response exceeds its bound.", "Reduce the response size.")
	}
	var checked envelope
	if err := domain.Decode(raw, &checked); err != nil {
		return err
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	original, ok := c.incoming[key]
	if !ok || original.token != event.Token || original.replying {
		c.mu.Unlock()
		return domain.Fail(domain.Conflict, "The native interaction was already answered or replaced.", "Reload the current interaction before responding.")
	}
	original.replying = true
	c.incoming[key] = original
	c.mu.Unlock()
	if err := c.write(ctx, raw); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.incoming, key)
	c.mu.Unlock()
	return nil
}
func marshal(message envelope, params any) ([]byte, error) {
	if err := domain.Text(message.Method, "native method", 256, true); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid native request parameters.", "Use the adapter's typed request schema.")
	}
	if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
		return nil, domain.Fail(domain.InvalidArgument, "Native parameters must be a structured object or array.", "Use the adapter's typed parameter schema.")
	}
	message.Params = raw
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
func (c *Connection) receive(raw []byte) error {
	var message envelope
	if err := domain.Decode(raw, &message); err != nil {
		return protocolFailure()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return protocolFailure()
	}
	_, hasMethod := fields["method"]
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	_, hasParams := fields["params"]
	if message.JSONRPC != "" && message.JSONRPC != "2.0" {
		return protocolFailure()
	}
	if message.EmittedAtMS != nil && (*message.EmittedAtMS < 0 || *message.EmittedAtMS > 253402300799999) {
		return protocolFailure()
	}
	key := ""
	if len(message.ID) > 0 {
		var err error
		key, err = idKey(message.ID)
		if err != nil {
			return err
		}
	}
	if hasMethod {
		if domain.Text(message.Method, "native method", 256, true) != nil || hasResult || hasError {
			return protocolFailure()
		}
		event := Event{Kind: Notification, Method: message.Method, Params: message.Params, EmittedAtMS: message.EmittedAtMS, size: len(raw)}
		c.mu.Lock()
		defer c.mu.Unlock()
		if key != "" {
			if _, exists := c.incoming[key]; exists {
				return protocolFailure()
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
		return protocolFailure()
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
		return protocolFailure()
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

func (w *frameWriter) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 && !w.stopped {
		end := bytes.IndexByte(data, '\n')
		size := len(data)
		if end >= 0 {
			size = end
		}
		if len(w.buffer)+size > MaxFrame {
			w.stopped = true
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
