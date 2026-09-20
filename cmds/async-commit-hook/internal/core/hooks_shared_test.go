package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSharedHookDirectoryDoesNotInstallRepositoryPolicy(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	ctx := context.Background()
	other := t.TempDir()
	if _, err := Git(ctx, other, "init", "--quiet", "--template="); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "shared-hooks")
	for _, source := range []string{repo, other} {
		if _, err := Git(ctx, source, "config", "core.hooksPath", shared); err != nil {
			t.Fatal(err)
		}
	}
	for _, existing := range []bool{false, true} {
		var original []byte
		if existing {
			original = []byte("#!/bin/sh\n# unrelated manager\nexit 0\n")
			if err := os.MkdirAll(shared, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shared, "post-commit"), original, 0755); err != nil {
				t.Fatal(err)
			}
		}
		result, err := s.Hook(ctx, repo, true, false)
		if err != nil || len(result) != 2 {
			t.Fatalf("shared install: %+v %v", result, err)
		}
		for _, item := range result {
			if item.Installed || !strings.Contains(item.Example, "git rev-parse --show-toplevel") || !strings.Contains(item.Manual, "integrate manually") {
				t.Fatalf("unsafe guidance: %+v", item)
			}
		}
		if _, err := os.Lstat(filepath.Join(shared, "pre-push")); !os.IsNotExist(err) {
			t.Fatal("shared pre-push was published")
		}
		if existing {
			got, err := os.ReadFile(filepath.Join(shared, "post-commit"))
			if err != nil || string(got) != string(original) {
				t.Fatal("existing hook changed")
			}
		} else if _, err := os.Stat(shared); !os.IsNotExist(err) {
			t.Fatal("absent shared directory was created")
		}
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM installations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("claimed shared ownership: %d, %v", count, err)
	}
	if _, err := s.Hook(ctx, repo, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "post-commit")); err != nil {
		t.Fatal("uninstall removed unrelated hook")
	}
	// Execute the printed guard: stdin reaches the selected worktree unchanged,
	// and an unrelated repository skips a command that would reject its push.
	if runtime.GOOS != "windows" {
		_, root, _, err := Discover(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		input := "refs/heads/main exact-tip refs/heads/main old-tip\n"
		for _, source := range []string{repo, other} {
			command := scopedHookCommand(root, "cat; exit 7")
			shell := exec.Command("sh", "-c", command)
			shell.Dir = source
			shell.Stdin = strings.NewReader(input)
			got, err := shell.Output()
			if source == repo {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 || string(got) != input {
					t.Fatalf("selected hook: %q %v", got, err)
				}
			} else if err != nil || len(got) != 0 {
				t.Fatalf("unrelated repo gated: %q %v", got, err)
			}
		}
	}
}

func TestNativeHookDirectoryRejectsSymlinkRedirection(t *testing.T) {
	common := t.TempDir()
	native := filepath.Join(common, "hooks")
	// t.TempDir can have a symlinked ancestor on macOS; use canonical discovery.
	common, err := filepath.EvalSymlinks(common)
	if err != nil {
		t.Fatal(err)
	}
	native = filepath.Join(common, "hooks")
	if !repositoryHookDirectory(common, native) {
		t.Fatal("absent native hooks rejected")
	}
	shared := t.TempDir()
	if err := os.Symlink(shared, native); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if repositoryHookDirectory(common, native) {
		t.Fatal("shared hooks symlink accepted")
	}
	if err := os.Remove(native); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(shared, "absent"), native); err != nil {
		t.Fatal(err)
	}
	if repositoryHookDirectory(common, native) {
		t.Fatal("dangling hooks symlink accepted")
	}
}
