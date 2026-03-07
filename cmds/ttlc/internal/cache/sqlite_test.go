package cache

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesSchemaAndPersistsTask(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-1",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a", "dep-b"},
		Metadata: map[string]any{
			"module": "build",
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	hasTask, err := store.HasTask("task-key-1")
	if err != nil {
		t.Fatalf("has task: %v", err)
	}
	if !hasTask {
		t.Fatal("expected task to be present")
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()

	requiredTables := []string{"schema_meta", "task_cache", "task_deps", "cache_blobs"}
	for _, tableName := range requiredTables {
		row := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tableName)
		var count int
		if err := row.Scan(&count); err != nil {
			t.Fatalf("scan sqlite_master for %s: %v", tableName, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s to exist", tableName)
		}
	}

	state, found, err := store.GetTaskState("build", "Build")
	if err != nil {
		t.Fatalf("get task state: %v", err)
	}
	if !found {
		t.Fatal("expected task state to be found")
	}
	if state.TaskKey != "task-key-1" {
		t.Fatalf("unexpected task key: %s", state.TaskKey)
	}
	if state.InputContentHash != "input-content-hash" {
		t.Fatalf("unexpected input content hash: %s", state.InputContentHash)
	}
}

func TestOpenAppliesPermissionsOnPosix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions are not portable on windows")
	}

	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	directoryInfo, err := os.Stat(filepath.Dir(databasePath))
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("unexpected cache dir mode: got=%o want=%o", got, 0o700)
	}

	fileInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("stat cache db: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("unexpected cache db mode: got=%o want=%o", got, 0o600)
	}
}

func TestGetTaskStateByTaskKey(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-lookup",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a"},
		Metadata:                map[string]any{"module": "build"},
		UpdatedAt:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	state, found, err := store.GetTaskStateByTaskKey("task-key-lookup")
	if err != nil {
		t.Fatalf("get task state by task key: %v", err)
	}
	if !found {
		t.Fatal("expected task state to be found")
	}
	if state.TaskID != "Build" {
		t.Fatalf("unexpected task id: %s", state.TaskID)
	}
	if state.TaskKey != "task-key-lookup" {
		t.Fatalf("unexpected task key: %s", state.TaskKey)
	}
}

func TestOpenResetsCacheOnSchemaVersionMismatch(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-1",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a"},
		Metadata:                map[string]any{"module": "build"},
		UpdatedAt:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET version = 1`); err != nil {
		t.Fatalf("set old schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite db: %v", err)
	}

	reopenedStore, err := Open(databasePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopenedStore.Close()

	hasTask, err := reopenedStore.HasTask("task-key-1")
	if err != nil {
		t.Fatalf("check task presence after reopen: %v", err)
	}
	if hasTask {
		t.Fatal("expected stale task row to be removed after schema reset")
	}

	versionRow := reopenedStore.db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`)
	var version int
	if err := versionRow.Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", version, schemaVersion)
	}
}

func TestOpenResetsUnversionedLegacySchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		t.Fatalf("create cache directory: %v", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}

	legacyStatements := []string{
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
		`DELETE FROM schema_meta`,
		`INSERT INTO task_cache(task_key, input_fingerprint, output_blob_ref, deps, metadata, updated_at)
		 VALUES('legacy-task', 'legacy-input', '', '[]', '{}', '2026-03-02T00:00:00Z')`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("execute legacy schema statement: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite db: %v", err)
	}

	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store after legacy schema setup: %v", err)
	}
	defer store.Close()

	versionRow := store.db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`)
	var version int
	if err := versionRow.Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", version, schemaVersion)
	}

	hasTask, err := store.HasTask("legacy-task")
	if err != nil {
		t.Fatalf("check legacy task presence: %v", err)
	}
	if hasTask {
		t.Fatal("expected legacy task row to be removed by schema reset")
	}

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-1",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a"},
		Metadata:                map[string]any{"module": "build"},
		UpdatedAt:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task on migrated schema: %v", err)
	}
}

func TestGetTaskStateDetectsCorruption(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-1",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a"},
		Metadata:                map[string]any{"module": "build"},
		UpdatedAt:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	if _, err := store.db.Exec(`UPDATE task_cache SET metadata = '{' WHERE module = 'build' AND task_id = 'Build'`); err != nil {
		t.Fatalf("corrupt metadata json: %v", err)
	}

	_, found, err := store.GetTaskState("build", "Build")
	if !found {
		t.Fatal("expected corrupted state row to exist")
	}
	if err == nil {
		t.Fatal("expected corruption error")
	}
	var corruptionErr *CorruptionError
	if !errors.As(err, &corruptionErr) {
		t.Fatalf("expected CorruptionError, got=%T err=%v", err, err)
	}
}

func TestGetTaskStateRejectsTrailingMetadataTokens(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cache", "cache.sqlite3")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertTask(TaskRecord{
		TaskKey:                 "task-key-1",
		Module:                  "build",
		TaskID:                  "Build",
		InputContentHash:        "input-content-hash",
		ParameterHash:           "parameter-hash",
		EnvironmentSnapshotHash: "environment-hash",
		InputFingerprint:        "input-hash",
		Deps:                    []string{"dep-a"},
		Metadata:                map[string]any{"module": "build"},
		UpdatedAt:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	if _, err := store.db.Exec(`UPDATE task_cache SET metadata = '{"module":"build"} garbage' WHERE module = 'build' AND task_id = 'Build'`); err != nil {
		t.Fatalf("inject trailing metadata tokens: %v", err)
	}

	_, found, err := store.GetTaskState("build", "Build")
	if !found {
		t.Fatal("expected corrupted state row to exist")
	}
	if err == nil {
		t.Fatal("expected corruption error")
	}
	var corruptionErr *CorruptionError
	if !errors.As(err, &corruptionErr) {
		t.Fatalf("expected CorruptionError, got=%T err=%v", err, err)
	}
	if corruptionErr.Field != "metadata" {
		t.Fatalf("expected metadata corruption field, got=%s", corruptionErr.Field)
	}
}
