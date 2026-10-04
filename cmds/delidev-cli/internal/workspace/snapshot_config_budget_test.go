// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotCopyReservesGeneratedGitConfigEntry(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, false)
	repo := input.Manifest.Repositories[0]
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	measured := snapshotCopyBudget{bytes: MaxSnapshotBytes, entries: MaxSnapshotEntries}
	if err := m.copySnapshotGitBudget(context.Background(), input.Preparation.SessionID, repo, target, &measured); err != nil {
		t.Fatal(err)
	}
	used := MaxSnapshotEntries - measured.entries
	git := m.Git
	git.OwnerID, git.readOnly, git.offline = input.Preparation.SessionID, true, true
	fields, err := git.revParseFields(context.Background(), repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(fields[0], "config")
	saved := filepath.Join(t.TempDir(), "source-config")
	if err := os.Rename(config, saved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(saved, config); err != nil {
			t.Error(err)
		}
	}()
	target, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	budget := snapshotCopyBudget{bytes: MaxSnapshotBytes, entries: used - 1}
	err = m.copySnapshotGitBudget(context.Background(), input.Preparation.SessionID, repo, target, &budget)
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("Git generated an entry outside the complete copy reservation", err)
	}
	if _, err := os.Lstat(filepath.Join(target, ".git", "config")); !os.IsNotExist(err) {
		t.Fatal("over-budget generated config was created", err)
	}
}
