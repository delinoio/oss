package security

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateAtomicRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "secret")
	for _, value := range []string{"one", "two"} {
		if err := WriteAtomic(path, []byte(value)); err != nil {
			t.Fatal(err)
		}
		got, err := ReadPrivate(path, 10)
		if err != nil || string(got) != value {
			t.Fatalf("got %q: %v", got, err)
		}
	}
	if _, err := ReadPrivate(path, 1); err == nil {
		t.Fatal("unbounded read")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files retained: %v %v", entries, err)
	}
}
func TestExclusiveLockReleasedByClose(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "lock")
	a, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := TryLock(path); err == nil {
		b.Close()
		t.Fatal("duplicate owner")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
func TestRejectSymlinkAndSharedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode and symlink test; Windows uses owner-only ACL validation")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(link); err == nil {
		t.Fatal("accepted symlink scope")
	}
	if err := os.Chmod(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(real); err == nil {
		t.Fatal("accepted shared scope")
	}
	info, err := os.Stat(real)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("mutated existing permissions")
	}
}
