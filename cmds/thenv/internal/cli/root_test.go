package cli

import (
	"os"
	"path/filepath"
	"testing"

	thenvv1 "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1"
)

func TestHasConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	conflict, err := hasConflict(path, []byte("A=1\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if conflict {
		t.Fatal("expected no conflict for missing file")
	}

	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	conflict, err = hasConflict(path, []byte("A=1\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if conflict {
		t.Fatal("expected no conflict for equal content")
	}

	conflict, err = hasConflict(path, []byte("A=2\n"))
	if err != nil {
		t.Fatalf("hasConflict returned error: %v", err)
	}
	if !conflict {
		t.Fatal("expected conflict for different content")
	}
}

func TestResolveOutputPath(t *testing.T) {
	envPath, err := resolveOutputPath(thenvv1.FileType_FILE_TYPE_ENV, "./.env", "./.dev.vars")
	if err != nil {
		t.Fatalf("resolveOutputPath returned error: %v", err)
	}
	if envPath == "" {
		t.Fatal("expected env path")
	}

	if _, err := resolveOutputPath(thenvv1.FileType_FILE_TYPE_UNSPECIFIED, "./.env", "./.dev.vars"); err == nil {
		t.Fatal("expected error for unsupported file type")
	}
}
