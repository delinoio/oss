package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHookUninstallPreservesConcurrentChanges(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	for _, edit := range []string{"contents", "replacement", "creation", "removal", "symlink"} {
		t.Run(edit, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "post-commit")
			original, concurrent := []byte("owned hook"), []byte("user hook")
			if edit != "creation" {
				if err := os.WriteFile(path, original, 0700); err != nil {
					t.Fatal(err)
				}
			}
			owned := Installation{ID: "hook:" + path, Path: path, Hash: Hash(original), Kind: "hook"}
			if err := s.saveInstallation(owned); err != nil {
				t.Fatal(err)
			}
			snapshot, err := readAgentFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch edit {
			case "contents", "creation":
				err = os.WriteFile(path, concurrent, 0700)
			case "replacement":
				// Keep the original inode allocated to distinguish a same-byte replacement.
				err = os.Rename(path, path+".old")
				if err == nil {
					err = os.WriteFile(path, original, 0700)
				}
				concurrent = original
			case "removal":
				err = os.Remove(path)
			case "symlink":
				if err = os.WriteFile(path+".target", concurrent, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(path+".target", path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.removeHook(owned, snapshot)
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != "hook-conflict" {
				t.Fatalf("concurrent hook change accepted: %v", err)
			}
			if _, err := s.installation(owned.ID); err != nil {
				t.Fatal("ownership lost after conflict", err)
			}
			data, err := os.ReadFile(path)
			if edit == "removal" {
				if !os.IsNotExist(err) {
					t.Fatal("deleted hook restored")
				}
			} else if err != nil || string(data) != string(concurrent) {
				t.Fatal("concurrent hook changed", err)
			}
			if edit == "symlink" {
				if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("symlink removed", err)
				}
			}
		})
	}
}
