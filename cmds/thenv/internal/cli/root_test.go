package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOutputFileConflictsWithoutForce(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(filePath, []byte("OLD=1\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	err := writeOutputFile(filePath, []byte("NEW=2\n"), false)
	if !errors.Is(err, ErrFileConflict) {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestWriteOutputFileOverwritesWithForceAndMode(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(filePath, []byte("OLD=1\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := writeOutputFile(filePath, []byte("NEW=2\n"), true); err != nil {
		t.Fatalf("write output: %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "NEW=2\n" {
		t.Fatalf("unexpected content: %q", string(content))
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected mode 0600, got %#o", info.Mode().Perm())
	}
}

func TestCollectPushFilesRequiresInput(t *testing.T) {
	t.Parallel()
	if _, err := collectPushFiles("", ""); err == nil {
		t.Fatal("expected an error when no input files are provided")
	}
}
