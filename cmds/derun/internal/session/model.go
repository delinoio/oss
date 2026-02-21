package session

import (
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
)

type Meta struct {
	SchemaVersion    string                       `json:"schema_version"`
	SessionID        string                       `json:"session_id"`
	Command          []string                     `json:"command"`
	WorkingDirectory string                       `json:"working_directory"`
	StartedAt        time.Time                    `json:"started_at"`
	RetentionSeconds int64                        `json:"retention_seconds"`
	TransportMode    contracts.DerunTransportMode `json:"transport_mode"`
	TTYAttached      bool                         `json:"tty_attached"`
	PID              int                          `json:"pid"`
}

type Final struct {
	SchemaVersion string                      `json:"schema_version"`
	SessionID     string                      `json:"session_id"`
	State         contracts.DerunSessionState `json:"state"`
	EndedAt       time.Time                   `json:"ended_at"`
	ExitCode      *int                        `json:"exit_code,omitempty"`
	Signal        string                      `json:"signal,omitempty"`
	Error         string                      `json:"error,omitempty"`
}

type IndexEntry struct {
	Offset    uint64                       `json:"offset"`
	Length    uint64                       `json:"length"`
	Channel   contracts.DerunOutputChannel `json:"channel"`
	Timestamp time.Time                    `json:"timestamp"`
}

type OutputChunk struct {
	Channel     contracts.DerunOutputChannel `json:"channel"`
	StartCursor string                       `json:"start_cursor"`
	EndCursor   string                       `json:"end_cursor"`
	DataBase64  string                       `json:"data_base64"`
	Timestamp   time.Time                    `json:"timestamp"`
}

type Summary struct {
	SessionID        string                       `json:"session_id"`
	State            contracts.DerunSessionState  `json:"state"`
	StartedAt        time.Time                    `json:"started_at"`
	EndedAt          *time.Time                   `json:"ended_at,omitempty"`
	TransportMode    contracts.DerunTransportMode `json:"transport_mode"`
	TTYAttached      bool                         `json:"tty_attached"`
	RetentionSeconds int64                        `json:"retention_seconds"`
	PID              int                          `json:"pid"`
}

type Detail struct {
	Summary
	ExitCode    *int       `json:"exit_code,omitempty"`
	Signal      string     `json:"signal,omitempty"`
	Error       string     `json:"error,omitempty"`
	OutputBytes uint64     `json:"output_bytes"`
	ChunkCount  uint64     `json:"chunk_count"`
	LastChunkAt *time.Time `json:"last_chunk_at,omitempty"`
}
