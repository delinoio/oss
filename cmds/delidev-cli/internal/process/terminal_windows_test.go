// SPDX-License-Identifier: Apache-2.0
package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
)

type windowsTerminalBuffer struct {
	sync.Mutex
	value string
}

func (b *windowsTerminalBuffer) Write(raw []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	b.value += string(raw)
	return len(raw), nil
}

func TestConPTYResizeOutputAndOwnedExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	output := &windowsTerminalBuffer{}
	h, err := Start(ctx, Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: filepath.Join(directory, "cmd.exe"), Args: []string{"/D", "/Q"}, Env: []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + directory}, Cwd: t.TempDir(), Terminal: &TerminalSize{Rows: 24, Columns: 80}, Stdout: output})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.Resize(TerminalSize{Rows: 37, Columns: 91}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("echo CONPTY_READY\rexit\r")); err != nil {
		t.Fatal(err)
	}
	if err := h.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	output.Lock()
	defer output.Unlock()
	if !strings.Contains(output.value, "CONPTY_READY") {
		t.Fatal("missing native ConPTY output")
	}
}

func TestConPTYDeliveryFailureJoinsOwnedCleanup(t *testing.T) {
	for _, test := range []struct {
		name     string
		canceled bool
	}{
		{name: "writer failure"},
		{name: "writer cancellation", canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory, err := windows.GetSystemDirectory()
			if err != nil {
				t.Fatal(err)
			}
			writerContext, cancelWriter := context.WithCancel(context.Background())
			defer cancelWriter()
			entered := make(chan struct{})
			writer := outputWriterFunc(func([]byte) (int, error) {
				close(entered)
				if test.canceled {
					<-writerContext.Done()
					return 0, writerContext.Err()
				}
				return 0, os.ErrClosed
			})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h, err := Start(ctx, Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: filepath.Join(directory, "cmd.exe"), Args: []string{"/D", "/Q"}, Env: []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + directory}, Cwd: t.TempDir(), Terminal: &TerminalSize{Rows: 24, Columns: 80}, Stdout: writer})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Resume(); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Write([]byte("echo CONPTY_DELIVERY\r")); err != nil {
				t.Fatal(err)
			}
			awaitOutput(t, entered)
			if test.canceled {
				cancelWriter()
			}
			select {
			case <-h.Done():
			case <-time.After(20 * time.Second):
				t.Fatal("ConPTY cleanup did not settle after delivery failure")
			}
			if code := domain.SafeError(h.Wait()).Code; code != domain.Unavailable {
				t.Fatalf("terminal result code = %v, want Unavailable", code)
			}
			if err := h.Stop(); err != nil {
				t.Fatalf("owned Stop: %v", err)
			}
			if err := h.Close(); err != nil {
				t.Fatalf("owned Close: %v", err)
			}
			scope, err := readWindowsScope(h.Identity().ScopeDir)
			if err != nil || !scope.Complete {
				t.Fatalf("owned Job completion not persisted: complete=%v err=%v", scope.Complete, err)
			}
		})
	}
}
