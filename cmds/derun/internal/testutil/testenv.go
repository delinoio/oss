package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TempStateRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stateRoot := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatalf("create state root: %v", err)
	}
	return stateRoot
}
