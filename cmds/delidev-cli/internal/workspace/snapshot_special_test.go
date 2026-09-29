//go:build darwin || linux

package workspace

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func TestSnapshotRejectsSocketPipeAndDeviceWithoutOpening(t *testing.T) {
	for _, kind := range []string{"socket", "pipe"} {
		t.Run(kind, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "ds-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			path := filepath.Join(root, "special")
			if kind == "socket" {
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			} else if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := walkSnapshot(context.Background(), root, filepath.Join(t.TempDir(), "copy"), nil); domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("special file accepted", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("source disappeared", err)
			}
		})
	}
	// /dev/null is a real native device fixture. The copier must reject its mode
	// before opening it; no special-node creation privileges are needed.
	if _, err := walkSnapshot(context.Background(), "/dev", filepath.Join(t.TempDir(), "copy"), func(path string) bool { return path != "null" }); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("device accepted", err)
	}
}
