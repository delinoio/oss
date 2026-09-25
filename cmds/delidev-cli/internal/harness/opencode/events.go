package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxQueuedEvents = 128
const maxQueuedEventBytes = 8 << 20
const maxEventIdentities = 65536
const eventIdleLimit = 30 * time.Second

// NativeEvent is a private native transport observation, never a product event.
// The original JSON event ID is not an SSE replay cursor. Properties require a
// separate typed observer before publication, completion or interaction use.
type NativeEvent struct {
	ID         string
	Kind       EventKind
	Properties json.RawMessage `json:"-"`
}

type eventStream struct {
	mu        sync.Mutex
	ctx       context.Context
	body      io.ReadCloser
	cancel    context.CancelFunc
	closeOnce sync.Once
	queue     chan NativeEvent
	done      chan struct{}
	problem   *domain.Error
	pending   int
	last      time.Time
	seen      map[string]bool
	connected bool
	cwd       string
	alive     func() error
	logger    *slog.Logger
	owner     domain.ID
	closing   bool
}

func eventProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode event stream does not match its native profile.", "Retain the original session and reconcile native state without replaying input or assuming a complete stream.")
}

func eventBound() *domain.Error {
	return domain.Fail(domain.ResourceExhausted, "OpenCode event stream exceeded its retained bound.", "Retain original observations and reconcile native state before another execution.")
}

func (s *sessionAPI) openEvents(ctx context.Context) (*eventStream, error) {
	if err := s.enter(ctx); err != nil {
		return nil, err
	}
	defer s.leave()
	if s.eventAttempt || s.problem != nil || s.input != nil {
		return nil, sessionConflict()
	}
	if _, err := s.readSession(ctx); err != nil {
		return nil, err
	}
	s.eventAttempt = true
	streamCtx, cancel := context.WithCancel(ctx)
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, s.origin+"/event", nil)
	if err != nil {
		cancel()
		s.problem = eventProblem()
		return nil, s.problem
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("x-opencode-directory", s.cwd)
	request.SetBasicAuth("delidev", s.password)
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		s.problem = unavailable()
		return nil, s.problem
	}
	values := response.Header.Values("Content-Type")
	validMedia := false
	if len(values) == 1 {
		media, params, err := mime.ParseMediaType(values[0])
		validMedia = err == nil && media == "text/event-stream" && len(params) == 0
	}
	if response.StatusCode != http.StatusOK || !validMedia || len(response.Header.Values("Content-Encoding")) != 0 {
		cancel()
		_ = response.Body.Close()
		s.problem = eventProblem()
		return nil, s.problem
	}
	stream := &eventStream{ctx: streamCtx, body: response.Body, cancel: cancel, queue: make(chan NativeEvent, maxQueuedEvents), done: make(chan struct{}), seen: map[string]bool{}, last: time.Now(), cwd: s.cwd, alive: s.alive, logger: s.logger, owner: s.owner}
	s.events = stream
	go stream.run(streamCtx)
	// The original connected record proves this listener was registered before
	// the caller may submit input. Its event ID remains in the deduplication set.
	ready, err := stream.Next(streamCtx)
	if err != nil || ready.Kind != ServerConnectedEvent {
		stream.Close()
		s.problem = eventProblem()
		if err != nil {
			s.problem = domain.SafeError(err)
		}
		return nil, s.problem
	}
	return stream, nil
}

func (s *eventStream) status() *domain.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.problem
}

func (s *eventStream) fail(problem *domain.Error) {
	s.mu.Lock()
	report := s.problem == nil && !s.closing
	if s.problem == nil {
		s.problem = problem
	}
	s.mu.Unlock()
	if report && s.logger != nil {
		s.logger.Warn("OpenCode native event stream needs reconciliation", "owner_id", s.owner, "code", problem.Code)
	}
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.body.Close()
	})
}

func (s *eventStream) Close() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.fail(unavailable())
	<-s.done
}

func (s *eventStream) Next(ctx context.Context) (NativeEvent, error) {
	// Already validated queued observations remain readable after a later stream
	// failure; that failure still blocks new input and is returned after draining.
	select {
	case event, ok := <-s.queue:
		if !ok {
			return NativeEvent{}, s.status()
		}
		s.mu.Lock()
		s.pending -= len(event.Properties)
		s.mu.Unlock()
		return event, nil
	case <-ctx.Done():
		s.fail(unavailable())
		return NativeEvent{}, unavailable()
	}
}

func (s *eventStream) run(ctx context.Context) {
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.fail(unavailable())
				return
			case now := <-ticker.C:
				s.mu.Lock()
				stale := now.Sub(s.last) >= eventIdleLimit
				s.mu.Unlock()
				if stale || s.alive() != nil {
					s.fail(unavailable())
					return
				}
			}
		}
	}()
	defer func() {
		s.fail(unavailable())
		<-watchDone
		close(s.queue)
		close(s.done)
	}()
	scanner := bufio.NewScanner(s.body)
	scanner.Buffer(make([]byte, 4096), maxHTTPBody+256)
	var data []byte
	eventField := false
	frameBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		frameBytes += len(line) + 1
		if frameBytes > maxHTTPBody+256 {
			s.fail(eventBound())
			return
		}
		if line == "" {
			if data == nil {
				// The native encoder emits one data record per frame. Empty frames
				// and metadata-only frames cannot refresh the heartbeat deadline.
				s.fail(eventProblem())
				return
			}
			if problem := s.accept(data); problem != nil {
				s.fail(problem)
				return
			}
			data, eventField, frameBytes = nil, false, 0
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			s.fail(eventProblem())
			return
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			if eventField || value != "message" {
				s.fail(eventProblem())
				return
			}
			eventField = true
		case "data":
			if len(value) > maxHTTPBody {
				s.fail(eventBound())
				return
			}
			if data != nil {
				s.fail(eventProblem())
				return
			}
			data = []byte(value)
		default:
			// No SSE id/retry, comments or unknown fields are emitted by the
			// pinned native endpoint. Never adopt them as reconnect authority.
			s.fail(eventProblem())
			return
		}
	}
	if scanner.Err() == bufio.ErrTooLong {
		s.fail(eventBound())
	} else if data != nil || eventField || frameBytes != 0 {
		s.fail(eventProblem())
	}
}

func (s *eventStream) accept(raw []byte) *domain.Error {
	fields, err := shape(raw, []string{"id", "type", "properties"}, nil)
	if err != nil {
		return eventProblem()
	}
	id, ok := boundedString(fields["id"], 30, true)
	if !ok || !nativeID(id, "evt") {
		return eventProblem()
	}
	kind, ok := boundedString(fields["type"], 128, true)
	if !ok || !EventKind(kind).valid() {
		return eventProblem()
	}
	properties, err := object(fields["properties"])
	if err != nil {
		return eventProblem()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[id] || !s.connected && EventKind(kind) != ServerConnectedEvent || s.connected && EventKind(kind) == ServerConnectedEvent {
		return eventProblem()
	}
	if len(s.seen) >= maxEventIdentities || s.pending+len(fields["properties"]) > maxQueuedEventBytes {
		return eventBound()
	}
	switch EventKind(kind) {
	case ServerConnectedEvent, ServerHeartbeatEvent, GlobalDisposedEvent:
		if len(properties) != 0 {
			return eventProblem()
		}
	case ServerInstanceDisposedEvent:
		if len(properties) != 1 || !scalar(properties["directory"], s.cwd) {
			return eventProblem()
		}
	}
	event := NativeEvent{ID: id, Kind: EventKind(kind), Properties: fields["properties"]}
	select {
	case s.queue <- event:
		s.pending += len(event.Properties)
		s.seen[id], s.connected, s.last = true, true, time.Now()
	default:
		return eventBound()
	}
	if event.Kind == ServerInstanceDisposedEvent || event.Kind == GlobalDisposedEvent {
		return unavailable()
	}
	return nil
}
