package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Store struct {
	db   *sql.DB
	path string
}

type TaskRecord struct {
	TaskKey          string
	InputFingerprint string
	OutputBlobRef    string
	Deps             []string
	Metadata         map[string]any
	UpdatedAt        time.Time
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

func (s *Store) UpsertTask(record TaskRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("cache store is not initialized")
	}
	if record.TaskKey == "" {
		return fmt.Errorf("task key is required")
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

	_, err = tx.Exec(`
		INSERT INTO task_cache(task_key, input_fingerprint, output_blob_ref, deps, metadata, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_key) DO UPDATE SET
			input_fingerprint = excluded.input_fingerprint,
			output_blob_ref = excluded.output_blob_ref,
			deps = excluded.deps,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at
	`,
		record.TaskKey,
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
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta(version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS task_cache(
			task_key TEXT PRIMARY KEY,
			input_fingerprint TEXT NOT NULL,
			output_blob_ref TEXT,
			deps TEXT NOT NULL,
			metadata TEXT NOT NULL,
			updated_at TEXT NOT NULL
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

	_, err := s.db.Exec(`DELETE FROM schema_meta`)
	if err != nil {
		return fmt.Errorf("reset schema_meta: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO schema_meta(version) VALUES(?)`, schemaVersion)
	if err != nil {
		return fmt.Errorf("write schema_meta: %w", err)
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
