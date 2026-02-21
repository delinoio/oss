package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTerminalRejectsNilFile(t *testing.T) {
	if isTerminal(nil) {
		t.Fatalf("nil file should not be treated as terminal")
	}
}

func TestIsTerminalRejectsDevNull(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Open DevNull returned error: %v", err)
	}
	defer devNull.Close()

	if isTerminal(devNull) {
		t.Fatalf("dev null should not be treated as terminal")
	}
}

func TestIsTerminalRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer file.Close()

	if isTerminal(file) {
		t.Fatalf("regular file should not be treated as terminal")
	}
}
