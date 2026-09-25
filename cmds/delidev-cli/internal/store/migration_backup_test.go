package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationBackupFailureDoesNotPublishPartialImage(t *testing.T) {
	s, _ := openTest(t)
	// SQLite refuses VACUUM inside a transaction, after the private target has
	// been created. This exercises failure between creation and publication.
	s.db.SetMaxOpenConns(1)
	if _, err := s.db.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	err := migrationBackup(context.Background(), s.db, s.root)
	if _, rollbackErr := s.db.Exec("ROLLBACK"); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err == nil {
		t.Fatal("backup unexpectedly succeeded inside a transaction")
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "backups"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed backup published files: %v %v", entries, err)
	}
	if err := migrationBackup(context.Background(), s.db, s.root); err != nil {
		t.Fatal(err)
	}
	images, err := filepath.Glob(filepath.Join(s.root, "backups", "*.sqlite"))
	if err != nil || len(images) != 1 {
		t.Fatalf("retry backup: %v %v", images, err)
	}
	if err := ValidateBackup(context.Background(), images[0]); err != nil {
		t.Fatal(err)
	}
}
