//go:build !windows

package terminal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestExplicitTerminalShellNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-executable")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	for _, path := range []string{"sh", filepath.Join(root, "missing"), root, file} {
		shell, err := ResolveShell(context.Background(), root, domain.NewID(), path, logger)
		if err == nil || shell != "" || domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatalf("invalid override selected a shell: %q %v", shell, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "terminal-processes")); !os.IsNotExist(err) {
		t.Fatal("invalid override started account-shell discovery")
	}
	shell, err := ResolveShell(context.Background(), root, domain.NewID(), "/bin/sh", logger)
	expected, _ := filepath.EvalSymlinks("/bin/sh")
	if err != nil || shell != expected {
		t.Fatal("valid explicit shell was not preserved", err)
	}
}
