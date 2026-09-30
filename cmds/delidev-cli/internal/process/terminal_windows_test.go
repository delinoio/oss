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
	if _, err := h.Write([]byte("echo CONPTY_READY\r\nexit\r\n")); err != nil {
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
