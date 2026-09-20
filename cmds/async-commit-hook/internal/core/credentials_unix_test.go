//go:build !windows

package core

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCredentialFilesRejectSpecialInputsWithoutReading(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, "/dev/zero", "/dev/null"} {
		requireCredentialUnavailable(t, path)
	}
	// Exercise the open/read boundary after a regular path could be replaced:
	// opening a FIFO must return without a writer so Stat can reject it.
	f, err := openCredentialFile(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().IsRegular() {
		t.Fatal("replacement FIFO was not identifiable", err)
	}
}
