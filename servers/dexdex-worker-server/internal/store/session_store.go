package store

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

// SessionMetadata tracks session lineage and fork state.
type SessionMetadata struct {
	SessionID          string
	ParentSessionID    string
	RootSessionID      string
	ForkStatus         dexdexv1.SessionForkStatus
	ForkedFromSequence uint64
	AgentCliType       dexdexv1.AgentCliType
	CreatedAt          time.Time
}

// SessionStore provides in-memory session output storage keyed by session ID.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]*dexdexv1.SessionOutputEvent
	metadata map[string]*SessionMetadata
	logger   *slog.Logger
}

// NewSessionStore creates a new empty SessionStore.
func NewSessionStore(logger *slog.Logger) *SessionStore {
	return &SessionStore{
		sessions: make(map[string][]*dexdexv1.SessionOutputEvent),
		metadata: make(map[string]*SessionMetadata),
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

// CreateSession stores session metadata.
func (s *SessionStore) CreateSession(meta SessionMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metadata[meta.SessionID] = &meta
	s.logger.Debug("created session metadata",
		"session_id", meta.SessionID,
		"parent_session_id", meta.ParentSessionID,
		"agent_cli_type", meta.AgentCliType.String(),
	)
}

// GetSessionMetadata returns session metadata for the given session ID.
// Returns an error if the session is not found.
func (s *SessionStore) GetSessionMetadata(sessionID string) (*SessionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, ok := s.metadata[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Return a copy to prevent external mutation.
	copied := *meta
	return &copied, nil
}

// ListChildSessions returns all sessions whose parent is the given session ID.
func (s *SessionStore) ListChildSessions(parentSessionID string) []*SessionMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var children []*SessionMetadata
	for _, meta := range s.metadata {
		if meta.ParentSessionID == parentSessionID {
			copied := *meta
			children = append(children, &copied)
		}
	}
	return children
}

// ArchiveSession sets the fork status of the given session to ARCHIVED.
// Returns an error if the session is not found.
func (s *SessionStore) ArchiveSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, ok := s.metadata[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	meta.ForkStatus = dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ARCHIVED
	s.logger.Debug("archived session",
		"session_id", sessionID,
	)
	return nil
}
