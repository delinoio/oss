package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func retireFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tmp", "opencode", "data", "data/opencode", "cache", "cache/opencode", "snapshot-checkpoint", "snapshot-checkpoint/objects"} {
		if err := security.PrivateDir(filepath.Join(home, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func writeRetireFile(t *testing.T, home, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(name)), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
}

// Retiring a profile removes only the per-run DeliDev files and scratch; native
// history, caches and helper files stay.
func TestRetireOwnedRuntimeFilesKeepsNativeState(t *testing.T) {
	home := retireFixture(t)
	removed := []string{"instructions.txt", "opencode/opencode.json", "tmp/scratch.dylib", "snapshot-checkpoint/objects/pack"}
	kept := []string{"data/opencode/opencode.db", "cache/opencode/models", "opencode/.gitignore"}
	for _, name := range append(append([]string{}, removed...), kept...) {
		writeRetireFile(t, home, name)
	}
	if err := os.Symlink("data/opencode/opencode.db", filepath.Join(home, "cache", "helper-link")); err != nil {
		t.Fatal(err)
	}
	if err := retireOwnedRuntimeFiles(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	for _, name := range removed {
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Fatalf("%s survived retirement", name)
		}
	}
	for _, name := range kept {
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "cache", "helper-link")); err != nil {
		t.Fatal("a native helper link was removed")
	}
	if err := retireOwnedRuntimeFiles(context.Background(), home); err != nil {
		t.Fatalf("retirement is not idempotent: %v", err)
	}
}

// A symlinked per-run file is refused rather than followed.
func TestRetireOwnedRuntimeFilesRefusesLinks(t *testing.T) {
	home := retireFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "instructions.txt")); err != nil {
		t.Fatal(err)
	}
	if err := retireOwnedRuntimeFiles(context.Background(), home); err == nil {
		t.Fatal("a linked per-run file was retired")
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatal("the link target was removed")
	}
}

// A new native history never adopts a retained database; only an explicit reset
// of an unsent attempt makes the profile fresh again.
func TestPrepareFreshRuntimeRefusesRetainedHistory(t *testing.T) {
	home := retireFixture(t)
	writeRetireFile(t, home, "instructions.txt")
	if err := prepareFreshRuntime(context.Background(), home); err != nil {
		t.Fatalf("a profile without history rejected a new native history: %v", err)
	}
	writeRetireFile(t, home, "data/opencode/opencode.db")
	if err := prepareFreshRuntime(context.Background(), home); err == nil {
		t.Fatal("a new native history adopted retained history")
	}
	if err := ResetUnsentHistory(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	if err := prepareFreshRuntime(context.Background(), home); err != nil {
		t.Fatalf("a reset profile is not fresh: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "cache", "opencode")); err != nil {
		t.Fatal("the reset removed a retained cache")
	}
}
