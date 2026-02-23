package cli

import (
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

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

func TestApplyAuthHeadersSetsSubjectHeader(t *testing.T) {
	req := connect.NewRequest(&thenvv1.ListBundleVersionsRequest{})
	applyAuthHeaders(req, "token-123", "subject-abc")

	if got := req.Header().Get("Authorization"); got != "Bearer token-123" {
		t.Fatalf("unexpected authorization header: got=%q", got)
	}
	if got := req.Header().Get("X-Thenv-Subject"); got != "subject-abc" {
		t.Fatalf("unexpected subject header: got=%q", got)
	}
	if req.Header().Get("X-Request-Id") == "" {
		t.Fatal("expected request id header to be set")
	}
	if req.Header().Get("X-Trace-Id") == "" {
		t.Fatal("expected trace id header to be set")
	}
}

func TestResolvedSubjectFallsBackToToken(t *testing.T) {
	flags := commonFlags{token: " token-value "}
	if got, want := flags.resolvedSubject(), "token-value"; got != want {
		t.Fatalf("resolvedSubject fallback mismatch: got=%q want=%q", got, want)
	}

	flags.subject = " explicit-subject "
	if got, want := flags.resolvedSubject(), "explicit-subject"; got != want {
		t.Fatalf("resolvedSubject explicit mismatch: got=%q want=%q", got, want)
	}
}
