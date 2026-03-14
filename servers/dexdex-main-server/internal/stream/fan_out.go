package stream

import (
	"log/slog"
	"sync"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EventEnvelope wraps a stream response with workspace scoping for internal buffering.
type EventEnvelope struct {
	WorkspaceID string
	Response    *dexdexv1.StreamWorkspaceEventsResponse
}

// FanOut is a workspace-scoped event broadcaster that supports publish/subscribe
// with bounded retention for replay.
type FanOut struct {
	mu          sync.RWMutex
	subscribers map[string][]subscriberEntry // keyed by workspace_id
	buffer      []EventEnvelope              // bounded retention buffer
	sequence    uint64                       // monotonically increasing event sequence
	nextSubID   uint64                       // monotonically increasing subscriber ID
	bufferSize  int
	logger      *slog.Logger
}

type subscriberEntry struct {
	ch chan *dexdexv1.StreamWorkspaceEventsResponse
	id uint64
}

// NewFanOut creates a new FanOut with the given buffer size for event retention.
func NewFanOut(bufferSize int, logger *slog.Logger) *FanOut {
	return &FanOut{
		subscribers: make(map[string][]subscriberEntry),
		buffer:      make([]EventEnvelope, 0, bufferSize),
		bufferSize:  bufferSize,
		logger:      logger,
	}
}

// Publish broadcasts an event to all subscribers of the given workspace and stores
// it in the retention buffer. The payload must be a valid
// isStreamWorkspaceEventsResponse_Payload value (e.g., *StreamWorkspaceEventsResponse_Task).
func (f *FanOut) Publish(workspaceID string, eventType dexdexv1.StreamEventType, payload isStreamWorkspaceEventsResponsePayload) {
	f.mu.Lock()

	f.sequence++
	seq := f.sequence

	resp := &dexdexv1.StreamWorkspaceEventsResponse{
		Sequence:    seq,
		WorkspaceId: workspaceID,
		EventType:   eventType,
		OccurredAt:  timestamppb.New(time.Now()),
	}

	if payload != nil {
		payload.setPayload(resp)
	}

	envelope := EventEnvelope{
		WorkspaceID: workspaceID,
		Response:    resp,
	}

	// Append to buffer, evicting oldest if full.
	if len(f.buffer) >= f.bufferSize {
		f.buffer = append(f.buffer[1:], envelope)
	} else {
		f.buffer = append(f.buffer, envelope)
	}

	// Copy subscriber list to avoid holding lock during send.
	subs := make([]subscriberEntry, len(f.subscribers[workspaceID]))
	copy(subs, f.subscribers[workspaceID])

	f.mu.Unlock()

	f.logger.Debug("event published",
		"workspace_id", workspaceID,
		"event_type", eventType.String(),
		"sequence", seq,
		"subscriber_count", len(subs),
	)

	// Send to subscribers without holding the lock.
	for _, sub := range subs {
		select {
		case sub.ch <- resp:
		default:
			f.logger.Warn("subscriber channel full, dropping event",
				"workspace_id", workspaceID,
				"sequence", seq,
				"subscriber_id", sub.id,
			)
		}
	}
}

// Subscribe returns a channel that receives events for the given workspace and
// an unsubscribe function. The caller must call the unsubscribe function when done.
func (f *FanOut) Subscribe(workspaceID string) (<-chan *dexdexv1.StreamWorkspaceEventsResponse, func()) {
	ch := make(chan *dexdexv1.StreamWorkspaceEventsResponse, 256)

	f.mu.Lock()
	f.nextSubID++
	subID := f.nextSubID

	entry := subscriberEntry{ch: ch, id: subID}
	f.subscribers[workspaceID] = append(f.subscribers[workspaceID], entry)
	f.mu.Unlock()

	f.logger.Info("subscriber added",
		"workspace_id", workspaceID,
		"subscriber_id", subID,
	)

	unsubscribe := func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		subs := f.subscribers[workspaceID]
		for i, s := range subs {
			if s.id == subID && s.ch == ch {
				f.subscribers[workspaceID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				f.logger.Info("subscriber removed",
					"workspace_id", workspaceID,
					"subscriber_id", subID,
				)
				return
			}
		}
	}

	return ch, unsubscribe
}

// Replay returns buffered events for the given workspace where sequence > fromSequence.
// Returns an error if fromSequence is older than the earliest retained event.
func (f *FanOut) Replay(workspaceID string, fromSequence uint64) ([]*dexdexv1.StreamWorkspaceEventsResponse, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Find workspace-scoped events in the buffer.
	var workspaceEvents []EventEnvelope
	for _, env := range f.buffer {
		if env.WorkspaceID == workspaceID {
			workspaceEvents = append(workspaceEvents, env)
		}
	}

	// No events means nothing to replay (not an error).
	if len(workspaceEvents) == 0 {
		return nil, nil
	}

	earliest := workspaceEvents[0].Response.Sequence

	// If fromSequence is before our retention window, return out of range.
	if fromSequence > 0 && fromSequence+1 < earliest {
		return nil, &ReplayOutOfRangeError{
			EarliestAvailableSequence: earliest,
			RequestedFromSequence:     fromSequence,
		}
	}

	var result []*dexdexv1.StreamWorkspaceEventsResponse
	for _, env := range workspaceEvents {
		if env.Response.Sequence > fromSequence {
			result = append(result, env.Response)
		}
	}

	f.logger.Debug("replay completed",
		"workspace_id", workspaceID,
		"from_sequence", fromSequence,
		"replayed_count", len(result),
	)

	return result, nil
}

// EarliestSequence returns the earliest sequence number for the given workspace,
// or 0 if no events are buffered.
func (f *FanOut) EarliestSequence(workspaceID string) uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, env := range f.buffer {
		if env.WorkspaceID == workspaceID {
			return env.Response.Sequence
		}
	}
	return 0
}

// ReplayOutOfRangeError is returned when the requested fromSequence is older than
// the earliest available event in the retention buffer.
type ReplayOutOfRangeError struct {
	EarliestAvailableSequence uint64
	RequestedFromSequence     uint64
}

func (e *ReplayOutOfRangeError) Error() string {
	return "requested sequence is out of retention range"
}

// isStreamWorkspaceEventsResponsePayload is an interface for setting typed payloads
// on StreamWorkspaceEventsResponse.
type isStreamWorkspaceEventsResponsePayload interface {
	setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse)
}

// TaskPayload wraps a UnitTask for publishing.
type TaskPayload struct {
	Task *dexdexv1.UnitTask
}

func (p *TaskPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_Task{Task: p.Task}
}

// SubTaskPayload wraps a SubTask for publishing.
type SubTaskPayload struct {
	SubTask *dexdexv1.SubTask
}

func (p *SubTaskPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_SubTask{SubTask: p.SubTask}
}

// SessionOutputPayload wraps a SessionOutputEvent for publishing.
type SessionOutputPayload struct {
	SessionOutput *dexdexv1.SessionOutputEvent
}

func (p *SessionOutputPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_SessionOutput{SessionOutput: p.SessionOutput}
}

// SessionStateChangedPayload wraps a SessionStateChangedEvent for publishing.
type SessionStateChangedPayload struct {
	SessionStateChanged *dexdexv1.SessionStateChangedEvent
}

func (p *SessionStateChangedPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_SessionStateChanged{SessionStateChanged: p.SessionStateChanged}
}

// NotificationCreatedPayload wraps a NotificationCreatedEvent for publishing.
type NotificationCreatedPayload struct {
	NotificationCreated *dexdexv1.NotificationCreatedEvent
}

func (p *NotificationCreatedPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_NotificationCreated{NotificationCreated: p.NotificationCreated}
}

// SessionForkUpdatedPayload wraps a SessionForkUpdatedEvent for publishing.
type SessionForkUpdatedPayload struct {
	SessionForkUpdated *dexdexv1.SessionForkUpdatedEvent
}

func (p *SessionForkUpdatedPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_SessionForkUpdated{SessionForkUpdated: p.SessionForkUpdated}
}

// PrUpdatedPayload wraps a PrUpdatedEvent for publishing.
type PrUpdatedPayload struct {
	PrUpdated *dexdexv1.PrUpdatedEvent
}

func (p *PrUpdatedPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_PrUpdated{PrUpdated: p.PrUpdated}
}

// WorkspaceWorkStatusUpdatedPayload wraps a WorkspaceWorkStatusUpdatedEvent for publishing.
type WorkspaceWorkStatusUpdatedPayload struct {
	WorkspaceWorkStatusUpdated *dexdexv1.WorkspaceWorkStatusUpdatedEvent
}

func (p *WorkspaceWorkStatusUpdatedPayload) setPayload(resp *dexdexv1.StreamWorkspaceEventsResponse) {
	resp.Payload = &dexdexv1.StreamWorkspaceEventsResponse_WorkspaceWorkStatusUpdated{WorkspaceWorkStatusUpdated: p.WorkspaceWorkStatusUpdated}
}
