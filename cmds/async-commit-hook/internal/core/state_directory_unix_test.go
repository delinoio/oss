//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDirectoryRejectsExistingPermissionsWithoutMutation(t *testing.T) {
	for _, mode := range []os.FileMode{0755, 0770, 0750} {
		t.Run(mode.String(), func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "shared")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, mode); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "unrelated")
			if err := os.WriteFile(file, []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(root)
			if store != nil {
				store.Close()
			}
			if err == nil {
				t.Fatal("accepted nonprivate state directory")
			}
			info, err := os.Stat(root)
			if err != nil || info.Mode().Perm() != mode.Perm() {
				t.Fatal("directory permissions changed", info, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != "unrelated" {
				t.Fatal("state files created", entries, err)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "retained" {
				t.Fatal("unrelated file changed", err)
			}
		})
	}
}

func TestStateDirectoryRejectsNonprivateChildWithoutChmod(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "evidence")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0755); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(root); err == nil {
		store.Close()
		t.Fatal("accepted shared evidence")
	}
	info, err := os.Stat(child)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("child permissions changed", err)
	}
}
