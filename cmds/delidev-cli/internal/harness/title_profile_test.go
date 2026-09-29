package harness

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// TestOptInManualCodexTitleProfile is the installed-harness evidence path. It
// uses only an explicitly selected binary and a private temporary runtime; it
// performs no login, workspace access, provider request or inference.
func TestOptInManualCodexTitleProfile(t *testing.T) {
	executable := os.Getenv("DELIDEV_CODEX_TITLE_EXECUTABLE")
	if executable == "" {
		t.Skip("set DELIDEV_CODEX_TITLE_EXECUTABLE to opt in to installed Codex profile verification")
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	verified, err := VerifyCodexTitleProfile(context.Background(), root, domain.NewID(), absolute, logger)
	if err != nil || !verified {
		t.Fatalf("installed Codex title profile was not verified: verified=%v err=%v", verified, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "probes"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("private Codex probe runtime was not removed: entries=%d err=%v", len(entries), err)
	}
}
