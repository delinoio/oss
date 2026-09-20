//go:build !windows

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
)

func TestConfigValidateUsesSelectedWorktreeRoot(t *testing.T) {
	config, repo := setup(t, "on-demand")
	// Commit without invoking the installed asynchronous hook.
	git(t, repo, "add", ".")
	git(t, repo, "-c", "core.hooksPath="+os.DevNull, "commit", "--quiet", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "-b", "validation-linked", linked)
	for _, root := range []string{repo, linked} {
		nested := filepath.Join(root, "nested", "directory")
		if err := os.MkdirAll(filepath.Join(nested, ".config"), 0700); err != nil {
			t.Fatal(err)
		}
		// A misleading nested file must not override the worktree configuration.
		if err := os.WriteFile(filepath.Join(nested, core.ProjectFile), []byte("version=999\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if out, exit := invoke(t, config, nested, "config", "validate"); exit != 0 {
			t.Fatalf("nested validate: %+v", out)
		}
	}
	// Authoring edits belong to the selected linked worktree only.
	if err := os.WriteFile(filepath.Join(linked, core.ProjectFile), []byte("version=999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, exit := invoke(t, config, filepath.Join(linked, "nested"), "config", "validate"); exit != 2 {
		t.Fatalf("invalid linked configuration accepted: %+v", out)
	}
	if out, exit := invoke(t, config, repo, "config", "validate"); exit != 0 {
		t.Fatalf("primary configuration changed: %+v", out)
	}
}

func TestConfigValidateRejectsHugeWorkingFile(t *testing.T) {
	config, repo := setup(t, "on-demand")
	file, err := os.OpenFile(filepath.Join(repo, core.ProjectFile), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(1 << 30)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if out, exit := invoke(t, config, repo, "config", "validate"); exit != 2 {
		t.Fatalf("oversized working configuration: exit=%d result=%+v", exit, out)
	}
}
