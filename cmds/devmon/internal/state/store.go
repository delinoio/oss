package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
	"github.com/delinoio/oss/cmds/devmon/internal/paths"
)

const SchemaVersionV1 = "v1"

type Store struct {
	path   string
	logger *slog.Logger
	nowFn  func() time.Time
	mu     sync.Mutex
}

type Snapshot struct {
	SchemaVersion   string    `json:"schema_version"`
	Running         bool      `json:"running"`
	PID             int       `json:"pid"`
	StartedAt       string    `json:"started_at,omitempty"`
	LastHeartbeatAt string    `json:"last_heartbeat_at,omitempty"`
	ActiveJobs      int       `json:"active_jobs"`
	LastRun         *LastRun  `json:"last_run,omitempty"`
	LastSkip        *LastSkip `json:"last_skip,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	UpdatedAt       string    `json:"updated_at,omitempty"`
}

type LastRun struct {
	Outcome    contracts.DevmonRunOutcome `json:"outcome"`
	FolderID   string                     `json:"folder_id"`
	JobID      string                     `json:"job_id"`
	DurationMS int64                      `json:"duration_ms"`
	Error      string                     `json:"error,omitempty"`
	Timestamp  string                     `json:"timestamp"`
}

type LastSkip struct {
	Outcome    contracts.DevmonRunOutcome `json:"outcome"`
	FolderID   string                     `json:"folder_id"`
	JobID      string                     `json:"job_id"`
	SkipReason string                     `json:"skip_reason"`
	Timestamp  string                     `json:"timestamp"`
}

type RunCompletedInput struct {
	Outcome    contracts.DevmonRunOutcome
	FolderID   string
	JobID      string
	DurationMS int64
	Error      string
	ActiveJobs int
}

type RunSkippedInput struct {
	Outcome    contracts.DevmonRunOutcome
	FolderID   string
	JobID      string
	SkipReason string
	ActiveJobs int
}

func NewStore(path string, logger *slog.Logger) (*Store, error) {
	resolvedPath := path
	if resolvedPath == "" {
		defaultPath, err := paths.StatePath()
		if err != nil {
			return nil, err
		}
		resolvedPath = defaultPath
	}

	return &Store{
		path:   resolvedPath,
		logger: logger,
		nowFn:  time.Now,
	}, nil
}

func (store *Store) Path() string {
	return store.path
}

func (store *Store) Read() (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadLocked()
}

func (store *Store) MarkDaemonStarted(pid int) error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		timestamp := now.UTC().Format(time.RFC3339Nano)
		if snapshot.StartedAt == "" {
			snapshot.StartedAt = timestamp
		}
		snapshot.PID = pid
		snapshot.Running = true
		snapshot.LastHeartbeatAt = timestamp
		snapshot.UpdatedAt = timestamp
	})
	return err
}

func (store *Store) MarkDaemonStopped() error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		snapshot.Running = false
		snapshot.ActiveJobs = 0
		snapshot.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	})
	return err
}

func (store *Store) MarkHeartbeat(pid int, activeJobs int) error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		timestamp := now.UTC().Format(time.RFC3339Nano)
		snapshot.Running = true
		snapshot.PID = pid
		snapshot.ActiveJobs = activeJobs
		if snapshot.StartedAt == "" {
			snapshot.StartedAt = timestamp
		}
		snapshot.LastHeartbeatAt = timestamp
		snapshot.UpdatedAt = timestamp
	})
	return err
}

func (store *Store) MarkRunStarted(folderID string, jobID string, activeJobs int) error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		snapshot.Running = true
		snapshot.ActiveJobs = activeJobs
		snapshot.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	})
	return err
}

func (store *Store) MarkRunCompleted(input RunCompletedInput) error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		timestamp := now.UTC().Format(time.RFC3339Nano)
		snapshot.LastRun = &LastRun{
			Outcome:    input.Outcome,
			FolderID:   input.FolderID,
			JobID:      input.JobID,
			DurationMS: input.DurationMS,
			Error:      input.Error,
			Timestamp:  timestamp,
		}
		snapshot.ActiveJobs = input.ActiveJobs
		snapshot.Running = true
		snapshot.UpdatedAt = timestamp

		if input.Error != "" {
			snapshot.LastError = input.Error
			return
		}

		switch input.Outcome {
		case contracts.DevmonRunOutcomeFailed, contracts.DevmonRunOutcomeTimeout:
			snapshot.LastError = string(input.Outcome)
		default:
			snapshot.LastError = ""
		}
	})
	return err
}

func (store *Store) MarkRunSkipped(input RunSkippedInput) error {
	_, err := store.mutate(func(snapshot *Snapshot, now time.Time) {
		timestamp := now.UTC().Format(time.RFC3339Nano)
		snapshot.LastSkip = &LastSkip{
			Outcome:    input.Outcome,
			FolderID:   input.FolderID,
			JobID:      input.JobID,
			SkipReason: input.SkipReason,
			Timestamp:  timestamp,
		}
		snapshot.ActiveJobs = input.ActiveJobs
		snapshot.Running = true
		snapshot.UpdatedAt = timestamp
	})
	return err
}

func IsHeartbeatFresh(snapshot Snapshot, now time.Time, staleThreshold time.Duration) bool {
	if staleThreshold <= 0 {
		return true
	}

	if snapshot.LastHeartbeatAt == "" {
		return false
	}

	heartbeatAt, err := time.Parse(time.RFC3339Nano, snapshot.LastHeartbeatAt)
	if err != nil {
		return false
	}

	return now.Sub(heartbeatAt) <= staleThreshold
}

func (store *Store) mutate(updateFn func(snapshot *Snapshot, now time.Time)) (Snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	snapshot, err := store.loadLocked()
	if err != nil {
		return Snapshot{}, err
	}

	now := store.nowFn()
	updateFn(&snapshot, now)

	if err := store.saveLocked(snapshot); err != nil {
		logging.Event(
			store.logger,
			slog.LevelError,
			"state_store_write_failed",
			slog.String("path", store.path),
			slog.String("error", err.Error()),
		)
		return Snapshot{}, err
	}

	return snapshot, nil
}

func (store *Store) loadLocked() (Snapshot, error) {
	snapshot := Snapshot{SchemaVersion: SchemaVersionV1}

	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read status file: %w", err)
	}
	if len(data) == 0 {
		return snapshot, nil
	}

	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode status file: %w", err)
	}
	if snapshot.SchemaVersion == "" {
		snapshot.SchemaVersion = SchemaVersionV1
	}

	return snapshot, nil
}

func (store *Store) saveLocked(snapshot Snapshot) error {
	if snapshot.SchemaVersion == "" {
		snapshot.SchemaVersion = SchemaVersionV1
	}

	directoryPath := filepath.Dir(store.path)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return fmt.Errorf("create status directory: %w", err)
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode status file: %w", err)
	}
	payload = append(payload, '\n')

	tempPath := store.path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return fmt.Errorf("write temp status file: %w", err)
	}

	if err := os.Rename(tempPath, store.path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename temp status file: %w", err)
	}

	return nil
}
