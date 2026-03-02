package cache

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 2

type Store struct {
	db   *sql.DB
	path string
}

type CorruptionError struct {
	Module string
	TaskID string
	Field  string
	Err    error
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("cache record corruption detected for %s.%s (%s): %v", e.Module, e.TaskID, e.Field, e.Err)
}

func (e *CorruptionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type TaskState struct {
	TaskKey                 string
	Module                  string
	TaskID                  string
	InputContentHash        string
	ParameterHash           string
	EnvironmentSnapshotHash string
	InputFingerprint        string
	OutputBlobRef           string
	Deps                    []string
	Metadata                map[string]any
	UpdatedAt               time.Time
}

type TaskRecord struct {
	TaskKey                 string
	Module                  string
	TaskID                  string
	InputContentHash        string
	ParameterHash           string
	EnvironmentSnapshotHash string
	InputFingerprint        string
	OutputBlobRef           string
	Deps                    []string
	Metadata                map[string]any
	UpdatedAt               time.Time
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("cache db path is required")
	}
	cacheDir := filepath.Dir(path)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}
	if err := chmodDirectory(cacheDir); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite cache: %w", err)
	}

	store := &Store{db: db, path: path}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := chmodFile(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) HasTask(taskKey string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("cache store is not initialized")
	}
	row := s.db.QueryRow(`SELECT 1 FROM task_cache WHERE task_key = ? LIMIT 1`, taskKey)
	var marker int
	err := row.Scan(&marker)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query cache task key: %w", err)
	}
	return marker == 1, nil
}

func (s *Store) GetTaskState(module string, taskID string) (TaskState, bool, error) {
	if s == nil || s.db == nil {
		return TaskState{}, false, fmt.Errorf("cache store is not initialized")
	}
	if strings.TrimSpace(module) == "" {
		return TaskState{}, false, fmt.Errorf("module is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return TaskState{}, false, fmt.Errorf("task id is required")
	}

	row := s.db.QueryRow(`
		SELECT task_key, module, task_id, input_content_hash, parameter_hash, environment_snapshot_hash, input_fingerprint, output_blob_ref, deps, metadata, updated_at
		FROM task_cache
		WHERE module = ? AND task_id = ?
		LIMIT 1
	`, module, taskID)

	state := TaskState{}
	var depsJSON string
	var metadataJSON string
	var updatedAt string
	err := row.Scan(
		&state.TaskKey,
		&state.Module,
		&state.TaskID,
		&state.InputContentHash,
		&state.ParameterHash,
		&state.EnvironmentSnapshotHash,
		&state.InputFingerprint,
		&state.OutputBlobRef,
		&depsJSON,
		&metadataJSON,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return TaskState{}, false, nil
	}
	if err != nil {
		return TaskState{}, false, fmt.Errorf("query task cache state: %w", err)
	}

	if unmarshalErr := json.Unmarshal([]byte(depsJSON), &state.Deps); unmarshalErr != nil {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "deps",
			Err:    unmarshalErr,
		}
	}
	if unmarshalErr := json.Unmarshal([]byte(metadataJSON), &state.Metadata); unmarshalErr != nil {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "metadata",
			Err:    unmarshalErr,
		}
	}

	if strings.TrimSpace(state.InputContentHash) == "" {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "input_content_hash",
			Err:    errors.New("empty input content hash"),
		}
	}
	if strings.TrimSpace(state.ParameterHash) == "" {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "parameter_hash",
			Err:    errors.New("empty parameter hash"),
		}
	}
	if strings.TrimSpace(state.EnvironmentSnapshotHash) == "" {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "environment_snapshot_hash",
			Err:    errors.New("empty environment snapshot hash"),
		}
	}

	parsedUpdatedAt, parseErr := time.Parse(time.RFC3339Nano, updatedAt)
	if parseErr != nil {
		return TaskState{}, true, &CorruptionError{
			Module: module,
			TaskID: taskID,
			Field:  "updated_at",
			Err:    parseErr,
		}
	}
	state.UpdatedAt = parsedUpdatedAt
	return state, true, nil
}

func (s *Store) DeleteTaskState(module string, taskID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("cache store is not initialized")
	}
	if strings.TrimSpace(module) == "" {
		return fmt.Errorf("module is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task id is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete task transaction: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRow(`SELECT task_key FROM task_cache WHERE module = ? AND task_id = ? LIMIT 1`, module, taskID)
	existingTaskKey := ""
	if scanErr := row.Scan(&existingTaskKey); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			if commitErr := tx.Commit(); commitErr != nil {
				return fmt.Errorf("commit delete task transaction: %w", commitErr)
			}
			tx = nil
			return nil
		}
		return fmt.Errorf("lookup task key before delete: %w", scanErr)
	}

	if _, execErr := tx.Exec(`DELETE FROM task_deps WHERE task_key = ?`, existingTaskKey); execErr != nil {
		return fmt.Errorf("delete task deps: %w", execErr)
	}
	if _, execErr := tx.Exec(`DELETE FROM task_cache WHERE module = ? AND task_id = ?`, module, taskID); execErr != nil {
		return fmt.Errorf("delete task cache row: %w", execErr)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit delete task transaction: %w", commitErr)
	}
	tx = nil
	return nil
}

func (s *Store) UpsertTask(record TaskRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("cache store is not initialized")
	}
	if record.TaskKey == "" {
		return fmt.Errorf("task key is required")
	}
	if strings.TrimSpace(record.Module) == "" {
		return fmt.Errorf("module is required")
	}
	if strings.TrimSpace(record.TaskID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(record.InputContentHash) == "" {
		return fmt.Errorf("input content hash is required")
	}
	if strings.TrimSpace(record.ParameterHash) == "" {
		return fmt.Errorf("parameter hash is required")
	}
	if strings.TrimSpace(record.EnvironmentSnapshotHash) == "" {
		return fmt.Errorf("environment snapshot hash is required")
	}
	if strings.TrimSpace(record.InputFingerprint) == "" {
		return fmt.Errorf("input fingerprint is required")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}

	depsJSON, err := json.Marshal(record.Deps)
	if err != nil {
		return fmt.Errorf("marshal deps: %w", err)
	}
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin cache transaction: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	existingTaskKey := ""
	row := tx.QueryRow(`SELECT task_key FROM task_cache WHERE module = ? AND task_id = ? LIMIT 1`, record.Module, record.TaskID)
	scanErr := row.Scan(&existingTaskKey)
	if scanErr != nil && scanErr != sql.ErrNoRows {
		return fmt.Errorf("lookup cache row by module and task id: %w", scanErr)
	}
	if scanErr == nil && existingTaskKey != record.TaskKey {
		_, err = tx.Exec(`DELETE FROM task_deps WHERE task_key = ?`, existingTaskKey)
		if err != nil {
			return fmt.Errorf("clear previous task deps: %w", err)
		}
		_, err = tx.Exec(`DELETE FROM task_cache WHERE task_key = ?`, existingTaskKey)
		if err != nil {
			return fmt.Errorf("delete previous task cache row: %w", err)
		}
	}

	_, err = tx.Exec(`
		INSERT INTO task_cache(
			task_key, module, task_id, input_content_hash, parameter_hash, environment_snapshot_hash, input_fingerprint, output_blob_ref, deps, metadata, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_key) DO UPDATE SET
			module = excluded.module,
			task_id = excluded.task_id,
			input_content_hash = excluded.input_content_hash,
			parameter_hash = excluded.parameter_hash,
			environment_snapshot_hash = excluded.environment_snapshot_hash,
			input_fingerprint = excluded.input_fingerprint,
			output_blob_ref = excluded.output_blob_ref,
			deps = excluded.deps,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at
	`,
		record.TaskKey,
		record.Module,
		record.TaskID,
		record.InputContentHash,
		record.ParameterHash,
		record.EnvironmentSnapshotHash,
		record.InputFingerprint,
		record.OutputBlobRef,
		string(depsJSON),
		string(metadataJSON),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert task_cache row: %w", err)
	}

	_, err = tx.Exec(`DELETE FROM task_deps WHERE task_key = ?`, record.TaskKey)
	if err != nil {
		return fmt.Errorf("clear task deps: %w", err)
	}
	for _, dependency := range record.Deps {
		_, err := tx.Exec(`INSERT INTO task_deps(task_key, dep_task_key) VALUES(?, ?)`, record.TaskKey, dependency)
		if err != nil {
			return fmt.Errorf("insert task dependency: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache transaction: %w", err)
	}
	tx = nil
	return nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_meta(version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("ensure schema_meta table: %w", err)
	}

	currentVersion, hasVersion, err := s.readSchemaVersion()
	if err != nil {
		return err
	}

	if hasVersion && currentVersion != schemaVersion {
		if err := s.resetSchema(); err != nil {
			return err
		}
	}

	if err := s.ensureSchemaTables(); err != nil {
		return err
	}
	if err := s.writeSchemaVersion(schemaVersion); err != nil {
		return err
	}
	return nil
}

func (s *Store) readSchemaVersion() (int, bool, error) {
	row := s.db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`)
	version := 0
	if err := row.Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	return version, true, nil
}

func (s *Store) writeSchemaVersion(version int) error {
	_, err := s.db.Exec(`DELETE FROM schema_meta`)
	if err != nil {
		return fmt.Errorf("reset schema_meta: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO schema_meta(version) VALUES(?)`, version)
	if err != nil {
		return fmt.Errorf("write schema_meta: %w", err)
	}
	return nil
}

func (s *Store) resetSchema() error {
	statements := []string{
		`DROP TABLE IF EXISTS task_deps`,
		`DROP TABLE IF EXISTS cache_blobs`,
		`DROP TABLE IF EXISTS task_cache`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("reset schema tables: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureSchemaTables() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS task_cache(
			task_key TEXT PRIMARY KEY,
			module TEXT NOT NULL,
			task_id TEXT NOT NULL,
			input_content_hash TEXT NOT NULL,
			parameter_hash TEXT NOT NULL,
			environment_snapshot_hash TEXT NOT NULL,
			input_fingerprint TEXT NOT NULL,
			output_blob_ref TEXT,
			deps TEXT NOT NULL,
			metadata TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(module, task_id)
		)`,
		`CREATE TABLE IF NOT EXISTS task_deps(
			task_key TEXT NOT NULL,
			dep_task_key TEXT NOT NULL,
			PRIMARY KEY(task_key, dep_task_key)
		)`,
		`CREATE TABLE IF NOT EXISTS cache_blobs(
			blob_ref TEXT PRIMARY KEY,
			codec TEXT NOT NULL,
			bytes BLOB NOT NULL,
			size_bytes INTEGER NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("run migration statement: %w", err)
		}
	}
	return nil
}

func chmodDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("chmod cache directory: %w", err)
	}
	return nil
}

func chmodFile(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod cache file: %w", err)
	}
	return nil
}
