package cache

import (
	"database/sql"
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
		TaskKey:          "task-key-1",
		InputFingerprint: "input-hash",
		Deps:             []string{"dep-a", "dep-b"},
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
