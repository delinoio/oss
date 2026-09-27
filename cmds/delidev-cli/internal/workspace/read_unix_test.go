//go:build darwin || linux

package workspace

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestWorkspaceReadFIFOIsRejectedWithoutBlocking(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	if err := syscall.Mkfifo(filepath.Join(manifest.PrimaryPath, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.ReadWorkspace(context.Background(), workspaceReadFixture(input, manifest, domain.WorkspaceFile, "pipe"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO open blocked")
	}
}
