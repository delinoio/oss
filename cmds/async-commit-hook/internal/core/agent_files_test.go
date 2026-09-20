package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentPublicationPreservesConcurrentSettings(t *testing.T) {
	for _, edit := range []string{"contents", "replacement", "creation", "removal", "symlink"} {
		t.Run(edit, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			old, concurrent := []byte("{\"initial\":true}"), []byte("{\"user\":true}")
			if edit != "creation" {
				if err := os.WriteFile(path, old, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := readAgentFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch edit {
			case "contents", "creation":
				err = os.WriteFile(path, concurrent, 0600)
			case "replacement":
				// Retain the old inode so the filesystem cannot reuse its identity.
				err = os.Rename(path, path+".old")
				if err == nil {
					err = os.WriteFile(path, old, 0600)
				}
				concurrent = old
			case "removal":
				err = os.Remove(path)
			case "symlink":
				if err = os.WriteFile(path+".target", concurrent, 0600); err != nil {
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
			for _, publish := range []func() error{func() error { _, err := before.write([]byte("ach merged settings")); return err }, before.remove} {
				err := publish()
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != "agent-conflict" {
					t.Fatalf("concurrent edit was not rejected: %v", err)
				}
			}
			got, err := os.ReadFile(path)
			if edit == "removal" {
				if !os.IsNotExist(err) {
					t.Fatal("deleted settings resurrected")
				}
			} else if err != nil || string(got) != string(concurrent) {
				t.Fatalf("concurrent settings changed: %q, %v", got, err)
			}
			if edit == "symlink" {
				if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("symlink replaced")
				}
			}
		})
	}
}

func TestAgentPublicationAndRollbackUseExactFileOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skill")
	previous, err := readAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	published, err := previous.write([]byte("owned"))
	if err != nil {
		t.Fatal(err)
	}
	if err := published.restore(previous); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("new skill was not rolled back")
	}
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	previous, err = readAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	published, err = previous.write([]byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	if err := published.restore(previous); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatalf("rollback: %q, %v", got, err)
	}
	previous, err = readAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	published, err = previous.write([]byte("owned"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := published.restore(previous); err == nil {
		t.Fatal("rollback erased concurrent skill edits")
	}
}
