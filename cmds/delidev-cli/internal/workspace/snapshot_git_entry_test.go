// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRejectsOriginalGitAdministrationSymlink(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, false)
	repo := input.Manifest.Repositories[0]
	git := m.Git
	git.OwnerID, git.readOnly, git.offline = input.Preparation.SessionID, true, true
	fields, err := git.revParseFields(context.Background(), repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(repo.Path, ".git")
	saved := original + ".fixture-original"
	if err := os.Rename(original, saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(original)
		if err := os.Rename(saved, original); err != nil {
			t.Error(err)
		}
	})
	if err := os.Symlink(fields[1], original); err != nil {
		// Windows ERROR_PRIVILEGE_NOT_HELD is 1314; preserve this explicit native limitation.
		if runtime.GOOS == "windows" && (os.IsPermission(err) || errors.Is(err, syscall.Errno(1314))) {
			t.Skip("native symlink creation permission unavailable")
		}
		t.Fatal(err)
	}
	// The OS may normalize the spelling accepted by Symlink (notably Windows).
	// Preserve the actual native link text rather than a Git-produced path spelling.
	originalTarget, err := os.Readlink(original)
	if err != nil {
		t.Fatal(err)
	}
	before, err := walkSnapshot(context.Background(), fields[1], "", nil)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := m.copySnapshotGit(context.Background(), input.Preparation.SessionID, repo, target); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("symlink administration was independently copied", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatal("unsupported topology retained copied output", err)
	}
	observation, err := m.storageObservation(context.Background(), input)
	if domain.SafeError(err).Code != domain.Unsupported || observation.Digest != "" {
		t.Fatal("symlink administration became preview authority", err)
	}
	after, err := walkSnapshot(context.Background(), fields[1], "", nil)
	if err != nil || inventoryDigest(before) != inventoryDigest(after) {
		t.Fatal("external administration changed", err)
	}
	if value, err := os.Readlink(original); err != nil || value != originalTarget {
		t.Fatal("unsupported source topology was rewritten", err)
	}
}
