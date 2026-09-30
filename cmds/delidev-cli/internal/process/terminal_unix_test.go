//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package process

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const terminalOutputFixtureBytes = 512 << 10

func TestTerminalOutputHelper(t *testing.T) {
	if os.Getenv("DELIDEV_TERMINAL_OUTPUT_FIXTURE") != "1" {
		return
	}
	if _, err := os.Stdout.Write(bytes.Repeat([]byte{'x'}, terminalOutputFixtureBytes)); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type slowTerminalOutput struct{ size int }

func (b *slowTerminalOutput) Write(raw []byte) (int, error) {
	// Throttle bytes rather than native read count: PTY chunk sizes differ by
	// OS and load, while this fixture keeps a bounded four-second drain budget.
	time.Sleep(time.Duration(len(raw)) * time.Second / (128 << 10))
	b.size += len(raw)
	return len(raw), nil
}

func TestTerminalOutputDrainPreservesFinalBytes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	output := &slowTerminalOutput{}
	h, err := Start(ctx, Config{Directory: root, OwnerID: domain.NewID(), Executable: executable, Args: []string{"-test.run=^TestTerminalOutputHelper$"}, Env: []string{"DELIDEV_TERMINAL_OUTPUT_FIXTURE=1"}, Cwd: t.TempDir(), Terminal: &TerminalSize{Rows: 24, Columns: 80}, Stdout: output})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.Wait(); err != nil {
		t.Fatalf("terminal exit after %d of %d bytes: %v", output.size, terminalOutputFixtureBytes, err)
	}
	if output.size != terminalOutputFixtureBytes {
		t.Fatalf("normal terminal exit truncated output: %d of %d", output.size, terminalOutputFixtureBytes)
	}
}

type terminalBuffer struct {
	sync.Mutex
	value string
}

func (b *terminalBuffer) Write(raw []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	b.value += string(raw)
	return len(raw), nil
}
func (b *terminalBuffer) contains(value string) bool {
	b.Lock()
	defer b.Unlock()
	return strings.Contains(b.value, value)
}

func TestTerminalOwnsTTYResizeAndNaturalExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	output := &terminalBuffer{}
	h, err := Start(ctx, Config{Directory: root, OwnerID: domain.NewID(), Executable: "/bin/sh", Args: []string{"-i"}, Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color"}, Cwd: t.TempDir(), Terminal: &TerminalSize{Rows: 24, Columns: 80}, Stdout: output})
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
	if _, err := h.Write([]byte("test -t 0 && printf 'TTY_READY\\n'; stty size; printf '\\342\\202\\254'; exit\n")); err != nil {
		t.Fatal(err)
	}
	if err := h.Wait(); err != nil {
		t.Fatal(err)
	}
	if !output.contains("TTY_READY\r\n") || !output.contains("37 91") || !output.contains("€") {
		t.Fatal("missing native TTY, resize or multibyte output")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalCloseJoinsOwnedDescendants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, owner := t.TempDir(), domain.NewID()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	h, err := Start(ctx, Config{Directory: root, OwnerID: owner, Executable: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin"}, Args: []string{"-i"}, Cwd: t.TempDir(), Terminal: &TerminalSize{Rows: 24, Columns: 80}, Stdout: &terminalBuffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("sleep 300 &\n")); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOwner(root, owner); err != nil {
		t.Fatal(err)
	}
}
