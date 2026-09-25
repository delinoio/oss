package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const maxStreamFrame = 1 << 20
const maxStreamPending = 128
const maxStreamEvents = 128
const maxStreamBytes = 8 << 20
const maxStreamIdentities = 4096

type StreamEventKind string

const (
	NativeMessage      StreamEventKind = "message"
	NativeRequest      StreamEventKind = "control-request"
	NativeCancellation StreamEventKind = "control-cancellation"
	NativeLateResponse StreamEventKind = "late-control-response"
)

// StreamEvent is private transport evidence. Typed session/interaction adapters
// must validate it before product publication; raw native data is JSON-excluded.
type StreamEvent struct {
	Kind      StreamEventKind `json:"-"`
	Type      string          `json:"-"`
	RequestID string          `json:"-"`
	ArrivalID domain.ID       `json:"-"`
	Body      json.RawMessage `json:"-"`
	Response  StreamResponse  `json:"-"`
	size      int
}

type StreamResponse struct {
	Result json.RawMessage `json:"-"`
	Failed bool            `json:"-"`
}

type streamPending struct {
	result    chan StreamResponse
	abandoned bool
}

type streamArrival struct {
	id       domain.ID
	claimed  bool
	closed   bool
	canceled bool
}

// Stream owns Claude's NDJSON control/input transport, not session authority.
// It never initializes, sends input, grants permissions or retries on its own.
type Stream struct {
	process        *process.Handle
	logger         *slog.Logger
	owner          domain.ID
	cancel         context.CancelFunc
	done           chan struct{}
	gate           chan struct{}
	notify         chan struct{}
	mu             sync.Mutex
	closing        bool
	problem        *domain.Error
	cleanup        error
	seen           map[domain.ID]bool
	pending        map[domain.ID]*streamPending
	incoming       map[string]streamArrival
	activeIncoming int
	events         []StreamEvent
	eventBytes     int
}

func streamIncompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "Claude Code sent an incompatible streaming protocol message.", "Retain the native session and validate the selected installed-version adapter before resuming.")
}

func streamUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Claude Code request delivery is uncertain.", "Inspect the original native operation before any retry; do not resend the input or response blindly.")
}

func streamEnded() *domain.Error {
	return domain.Fail(domain.Unavailable, "The Claude Code streaming connection ended.", "Reconcile the original execution and its owned cleanup before explicit Resume.")
}

func StartStream(ctx context.Context, config process.Config) (*Stream, error) {
	life, cancel := context.WithCancel(ctx)
	s := &Stream{cancel: cancel, done: make(chan struct{}), gate: make(chan struct{}, 1), notify: make(chan struct{}, 1), seen: map[domain.ID]bool{}, pending: map[domain.ID]*streamPending{}, incoming: map[string]streamArrival{}}
	s.logger, s.owner = config.Logger, config.OwnerID
	if s.logger == nil {
		s.logger = slog.Default()
	}
	frames := &streamFrames{stream: s}
	config.Stdout, config.Stderr = frames, io.Discard
	h, err := process.Start(life, config)
	if err != nil {
		cancel()
		return nil, err
	}
	s.process = h
	go func() {
		_ = h.Wait()
		cleanup := h.Close()
		s.mu.Lock()
		s.cleanup = cleanup
		if cleanup != nil {
			s.problem = streamUncertain()
		} else if s.problem == nil && len(frames.buffer) != 0 {
			s.problem = streamIncompatible()
		} else if s.problem == nil && !s.closing {
			s.problem = streamEnded()
		}
		s.mu.Unlock()
		if cleanup != nil {
			s.logger.Warn("Claude Code stream cleanup remains uncertain", "owner", s.owner, "code", domain.RecoveryRequired)
		}
		cancel()
		close(s.done)
	}()
	if err := h.Resume(); err != nil {
		s.fail(domain.SafeError(err))
		if cleanup := s.Close(); cleanup != nil {
			return nil, streamUncertain()
		}
		return nil, err
	}
	return s, nil
}

func (s *Stream) fail(problem *domain.Error) {
	s.mu.Lock()
	first := s.problem == nil
	if s.problem == nil {
		s.problem = problem
	}
	s.mu.Unlock()
	if first {
		s.logger.Warn("Claude Code stream failed", "owner", s.owner, "code", problem.Code)
	}
	s.cancel()
}

func (s *Stream) statusLocked() *domain.Error {
	if s.problem != nil {
		return s.problem
	}
	if s.closing {
		return domain.Fail(domain.Canceled, "The Claude Code stream is closing.", "Reconcile the owned native session before opening a replacement.")
	}
	return nil
}

func (s *Stream) Err() *domain.Error        { s.mu.Lock(); defer s.mu.Unlock(); return s.statusLocked() }
func (s *Stream) Done() <-chan struct{}     { return s.done }
func (s *Stream) Identity() process.Process { return s.process.Identity() }

// Close proves owned descendant cleanup only. A terminal frame, successful
// stdin write or canceled native interaction never substitutes for this proof.
func (s *Stream) Close() error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	<-s.done
	// A writer that already claimed the gate must finish before callers can
	// release its runtime. New writers observe closing and cannot transmit.
	s.gate <- struct{}{}
	s.release()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanup
}

func (s *Stream) Next(ctx context.Context) (StreamEvent, error) {
	for {
		s.mu.Lock()
		problem := s.statusLocked()
		if problem != nil {
			s.mu.Unlock()
			return StreamEvent{}, problem
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return StreamEvent{}, domain.SafeError(ctx.Err())
		}
		if len(s.events) > 0 {
			event := s.events[0]
			copy(s.events, s.events[1:])
			s.events[len(s.events)-1] = StreamEvent{}
			s.events = s.events[:len(s.events)-1]
			s.eventBytes -= event.size
			s.mu.Unlock()
			return event, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return StreamEvent{}, domain.SafeError(ctx.Err())
		case <-s.done:
		case <-s.notify:
		}
	}
}

func (s *Stream) queueLocked(event StreamEvent) error {
	if len(s.events) >= maxStreamEvents || s.eventBytes+event.size > maxStreamBytes {
		return domain.Fail(domain.ResourceExhausted, "Claude Code event buffering reached its bound.", "Reconcile the interrupted native stream before explicit continuation.")
	}
	s.events = append(s.events, event)
	s.eventBytes += event.size
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *Stream) acquire(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		if ctx.Err() != nil {
			s.release()
			return domain.SafeError(ctx.Err())
		}
		if problem := s.Err(); problem != nil {
			s.release()
			return problem
		}
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	case <-s.done:
		return streamEnded()
	}
}
func (s *Stream) release() { <-s.gate }

func (s *Stream) write(ctx context.Context, raw []byte) error {
	finished := make(chan error, 1)
	go func() {
		data := append(raw, '\n')
		n, err := s.process.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		finished <- err
	}()
	joined := false
	select {
	case err := <-finished:
		joined = true
		if err == nil {
			return nil
		}
	case <-ctx.Done():
	case <-s.done:
	}
	// A blocked writer loses the connection and must terminate with its owned
	// process. It cannot outlive the caller or authorize a duplicate operation.
	s.fail(streamUncertain())
	<-s.done
	if !joined {
		<-finished
	}
	return streamUncertain()
}

func streamJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid Claude Code request.", "Use the native adapter's typed request schema.")
	}
	if len(raw) > maxStreamFrame {
		return nil, domain.Fail(domain.ResourceExhausted, "Claude Code request exceeds its bound.", "Reduce the native request size before retrying.")
	}
	var object map[string]json.RawMessage
	if domain.Decode(raw, &object) != nil || object == nil {
		return nil, streamIncompatible()
	}
	return raw, nil
}

func (s *Stream) claimLocked(id domain.ID) error {
	if s.seen[id] {
		return domain.Fail(domain.Conflict, "This Claude Code operation identity was already used.", "Reconcile the original operation instead of resending it.")
	}
	if len(s.seen) >= maxStreamIdentities {
		return domain.Fail(domain.ResourceExhausted, "Claude Code operation tracking reached its bound.", "Retain and reconcile the session before replacing its connection.")
	}
	s.seen[id] = true
	return nil
}

// Call preserves late control acknowledgments under their original identity.
// Callers persist mutating operations before transmission. It never retries.
func (s *Stream) Call(ctx context.Context, id domain.ID, request any) (StreamResponse, error) {
	if err := id.Validate(); err != nil {
		return StreamResponse{}, err
	}
	params, err := streamJSON(request)
	if err != nil {
		return StreamResponse{}, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(params, &fields)
	var subtype string
	if json.Unmarshal(fields["subtype"], &subtype) != nil || domain.Text(subtype, "native control subtype", 128, true) != nil {
		return StreamResponse{}, streamIncompatible()
	}
	raw, err := streamJSON(struct {
		Type      string          `json:"type"`
		RequestID domain.ID       `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}{"control_request", id, params})
	if err != nil {
		return StreamResponse{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return StreamResponse{}, err
	}
	p := &streamPending{result: make(chan StreamResponse, 1)}
	s.mu.Lock()
	if len(s.pending) >= maxStreamPending {
		s.mu.Unlock()
		s.release()
		return StreamResponse{}, domain.Fail(domain.ResourceExhausted, "Too many pending Claude Code controls.", "Reconcile outstanding controls before sending another.")
	}
	if err := s.claimLocked(id); err != nil {
		s.mu.Unlock()
		s.release()
		return StreamResponse{}, err
	}
	s.pending[id] = p
	s.mu.Unlock()
	err = s.write(ctx, raw)
	s.release()
	if err != nil {
		return StreamResponse{}, err
	}
	select {
	case response := <-p.result:
		return response, nil
	case <-ctx.Done():
	case <-s.done:
	}
	s.mu.Lock()
	if _, exists := s.pending[id]; !exists {
		s.mu.Unlock()
		return <-p.result, nil // Already observed acknowledgment wins over timeout.
	}
	p.abandoned = true
	s.mu.Unlock()
	return StreamResponse{}, streamUncertain()
}

// SendInput reports pipe delivery only. Native command_lifecycle, replayed user
// identity/content and the exact result must separately establish acceptance.
func (s *Stream) SendInput(ctx context.Context, id, session domain.ID, prompt string) error {
	for _, value := range []domain.ID{id, session} {
		if err := value.Validate(); err != nil {
			return err
		}
	}
	if err := (domain.SessionInput{Mode: domain.ExecuteMode, Prompt: prompt}).Validate(); err != nil {
		return err
	}
	raw, err := streamJSON(struct {
		Type            string    `json:"type"`
		UUID            domain.ID `json:"uuid"`
		SessionID       domain.ID `json:"session_id"`
		ParentToolUseID *string   `json:"parent_tool_use_id"`
		Message         struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}{Type: "user", UUID: id, SessionID: session, Message: struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: prompt}})
	if err != nil {
		return err
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	s.mu.Lock()
	err = s.claimLocked(id)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.write(ctx, raw)
}

// Reply claims exactly one original callback arrival. Cancellation closes that
// arrival without proving whether an already claimed response was accepted.
func (s *Stream) Reply(ctx context.Context, event StreamEvent, result any) error {
	if event.Kind != NativeRequest || event.ArrivalID.Validate() != nil || domain.Text(event.RequestID, "native callback identity", 128, true) != nil {
		return streamIncompatible()
	}
	params, err := streamJSON(result)
	if err != nil {
		return err
	}
	raw, err := streamJSON(struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
	}{Type: "control_response", Response: struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	}{"success", event.RequestID, params}})
	if err != nil {
		return err
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	s.mu.Lock()
	arrival, exists := s.incoming[event.RequestID]
	if !exists || arrival.id != event.ArrivalID || arrival.claimed || arrival.closed {
		s.mu.Unlock()
		return domain.Fail(domain.Conflict, "The Claude Code callback was already answered, canceled or replaced.", "Use only the original pending native interaction.")
	}
	arrival.claimed = true
	s.incoming[event.RequestID] = arrival
	s.mu.Unlock()
	if err := s.write(ctx, raw); err != nil {
		return err
	}
	s.mu.Lock()
	arrival = s.incoming[event.RequestID]
	if !arrival.closed {
		s.activeIncoming--
		arrival.closed = true
		s.incoming[event.RequestID] = arrival
	}
	s.mu.Unlock()
	return nil
}

func (s *Stream) receive(raw []byte) error {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return streamIncompatible()
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil || domain.Text(kind, "native message type", 128, true) != nil {
		return streamIncompatible()
	}
	switch kind {
	case "control_response":
		return s.receiveResponse(raw)
	case "control_request", "control_cancel_request":
		return s.receiveRequest(raw, kind)
	default:
		// Unknown message families stay private for their typed adapter to reject;
		// they can never be interpreted as a control response or acknowledgement.
		if _, exists := fields["request_id"]; exists {
			return streamIncompatible()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.queueLocked(StreamEvent{Kind: NativeMessage, Type: kind, Body: bytes.Clone(raw), size: len(raw)})
	}
}

func (s *Stream) receiveResponse(raw []byte) error {
	var message struct {
		Type     string `json:"type"`
		Response *struct {
			Subtype   string          `json:"subtype"`
			RequestID domain.ID       `json:"request_id"`
			Response  json.RawMessage `json:"response,omitempty"`
			Error     json.RawMessage `json:"error,omitempty"`
		} `json:"response"`
	}
	if domain.Decode(raw, &message) != nil || message.Response == nil || message.Response.RequestID.Validate() != nil {
		return streamIncompatible()
	}
	r := message.Response
	response := StreamResponse{Result: r.Response}
	switch r.Subtype {
	case "success":
		if len(r.Error) != 0 {
			return streamIncompatible()
		}
		if len(r.Response) != 0 {
			var result map[string]json.RawMessage
			if json.Unmarshal(r.Response, &result) != nil {
				return streamIncompatible()
			}
		}
	case "error":
		var message *string
		if len(r.Response) != 0 || json.Unmarshal(r.Error, &message) != nil || message == nil {
			return streamIncompatible()
		}
		response = StreamResponse{Failed: true} // Never retain reflected native error text.
	default:
		return streamIncompatible()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, exists := s.pending[r.RequestID]
	if !exists {
		return streamIncompatible()
	}
	delete(s.pending, r.RequestID)
	if p.abandoned {
		return s.queueLocked(StreamEvent{Kind: NativeLateResponse, RequestID: string(r.RequestID), Response: response, size: len(raw)})
	}
	p.result <- response
	return nil
}

func (s *Stream) receiveRequest(raw []byte, kind string) error {
	var message struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request,omitempty"`
	}
	if domain.Decode(raw, &message) != nil || domain.Text(message.RequestID, "native callback identity", 128, true) != nil {
		return streamIncompatible()
	}
	if kind == "control_request" {
		var request map[string]json.RawMessage
		var subtype string
		if domain.Decode(message.Request, &request) != nil || request == nil || json.Unmarshal(request["subtype"], &subtype) != nil || domain.Text(subtype, "native callback subtype", 128, true) != nil {
			return streamIncompatible()
		}
	} else if len(message.Request) != 0 {
		return streamIncompatible()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	arrival, exists := s.incoming[message.RequestID]
	if kind == "control_cancel_request" {
		if !exists || arrival.canceled {
			return streamIncompatible()
		}
		arrival.canceled = true
		if !arrival.closed {
			s.activeIncoming--
			arrival.closed = true
		}
		s.incoming[message.RequestID] = arrival
		return s.queueLocked(StreamEvent{Kind: NativeCancellation, RequestID: message.RequestID, ArrivalID: arrival.id, size: len(raw)})
	}
	if exists {
		return streamIncompatible()
	}
	if s.activeIncoming >= maxStreamPending || len(s.incoming) >= maxStreamIdentities {
		return domain.Fail(domain.ResourceExhausted, "Claude Code callback tracking reached its bound.", "Resolve and reconcile native callbacks before explicit continuation.")
	}
	arrival = streamArrival{id: domain.NewID()}
	s.incoming[message.RequestID] = arrival
	s.activeIncoming++
	return s.queueLocked(StreamEvent{Kind: NativeRequest, RequestID: message.RequestID, ArrivalID: arrival.id, Body: bytes.Clone(message.Request), size: len(raw)})
}

type streamFrames struct {
	stream  *Stream
	buffer  []byte
	stopped bool
}

func (w *streamFrames) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 && !w.stopped {
		end := bytes.IndexByte(data, '\n')
		size := len(data)
		if end >= 0 {
			size = end
		}
		if len(w.buffer)+size > maxStreamFrame {
			w.buffer, w.stopped = nil, true
			w.stream.fail(domain.Fail(domain.ResourceExhausted, "Claude Code stream frame exceeds its bound.", "Reconcile the interrupted native session before continuing."))
			break
		}
		w.buffer = append(w.buffer, data[:size]...)
		data = data[size:]
		if end < 0 {
			break
		}
		data = data[1:]
		if err := w.stream.receive(w.buffer); err != nil {
			w.stopped = true
			w.stream.fail(domain.SafeError(err))
		}
		w.buffer = nil
	}
	return length, nil
}
