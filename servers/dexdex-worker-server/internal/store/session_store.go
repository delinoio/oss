package store

import (
	"log/slog"
	"sync"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// SessionStore provides in-memory session output storage keyed by session ID.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]*dexdexv1.SessionOutputEvent
	logger   *slog.Logger
}

// NewSessionStore creates a new empty SessionStore.
func NewSessionStore(logger *slog.Logger) *SessionStore {
	return &SessionStore{
		sessions: make(map[string][]*dexdexv1.SessionOutputEvent),
		logger:   logger,
	}
}

// AppendOutput appends a session output event to the store for the given session ID.
func (s *SessionStore) AppendOutput(sessionID string, event *dexdexv1.SessionOutputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sessionID] = append(s.sessions[sessionID], event)
	s.logger.Debug("appended session output",
		"session_id", sessionID,
		"kind", event.Kind.String(),
		"event_count", len(s.sessions[sessionID]),
	)
}

// GetOutputs returns all session output events for the given session ID.
// Returns nil if no events exist for the session.
func (s *SessionStore) GetOutputs(sessionID string) []*dexdexv1.SessionOutputEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.sessions[sessionID]
	if events == nil {
		return nil
	}

	// Return a copy to prevent external mutation.
	result := make([]*dexdexv1.SessionOutputEvent, len(events))
	copy(result, events)
	return result
}
