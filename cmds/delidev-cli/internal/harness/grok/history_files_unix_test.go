//go:build !windows

package grok

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeHistoryRejectsFIFOWithoutBlocking(t *testing.T) {
	home := historyFileFixture(t, nil)
	if err := unix.Mkfifo(filepath.Join(home, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	scope, err := openHistoryFiles(context.Background(), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	if raw, err := scope.read(context.Background(), "pipe", 1024); err == nil || raw != nil {
		t.Fatal("special file entered history")
	}
}

func TestNativeReadableFilesRemainInsidePrivateOwnedHome(t *testing.T) {
	home := historyFileFixture(t, map[string][]byte{"native/first": []byte("original")})
	path := filepath.Join(home, "native", "first")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	scope, err := openHistoryFiles(context.Background(), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	if raw, err := scope.read(context.Background(), "native/first", 1024); err != nil || string(raw) != "original" {
		t.Fatal("pinned native file modes rejected", err)
	}
	for _, name := range []string{"../first", "/first", "native/../native/first", "native//first", "native/first/"} {
		if raw, err := scope.read(context.Background(), name, 1024); err == nil || raw != nil {
			t.Fatal("unsafe relative file accepted")
		}
	}
}
