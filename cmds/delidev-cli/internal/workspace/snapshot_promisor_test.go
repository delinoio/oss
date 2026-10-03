// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRejectsPromisorStores(t *testing.T) {
	for _, key := range []string{"extensions.partialClone", "remote.origin.promisor", "remote.origin.partialCloneFilter", "worktree-config", "pack-marker"} {
		t.Run(key, func(t *testing.T) {
			m := manager(t)
			input, sources := snapshotRequest(t, m, false)
			switch key {
			case "pack-marker":
				if err := os.WriteFile(filepath.Join(sources[0], ".git", "objects", "pack", "orphan.promisor"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "worktree-config":
				admin := gitTest(t, input.Manifest.Repositories[0].Path, "rev-parse", "--path-format=absolute", "--absolute-git-dir")
				if err := os.WriteFile(filepath.Join(admin, "config.worktree"), []byte("[remote \"origin\"]\n promisor = true\n"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				gitTest(t, sources[0], "config", key, "true")
			}
			preview := storageDo(t, m, input)
			input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			before, err := m.storageObservation(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("promisor store accepted", err)
			}
			after, err := m.storageObservation(context.Background(), input)
			if err != nil || after.Digest != before.Digest {
				t.Fatal("rejection changed source data", err)
			}
			if _, err := os.Stat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
				t.Fatal("unsupported snapshot published", err)
			}
		})
	}
}

func TestSnapshotInspectionRejectsPromisorPolicy(t *testing.T) {
	m := manager(t)
	input, _ := snapshotRequest(t, m, false)
	input.Action, input.SnapshotID = StorageCreate, domain.NewID()
	created := storageDo(t, m, input)
	path := filepath.Join(m.snapshotPath(created.Snapshot.ID), "workspace", string(input.Manifest.Repositories[0].ID))
	gitTest(t, path, "config", "remote.origin.promisor", "true")
	// Exercise Git validation directly: ordinary inspection also refuses the changed
	// inventory, while legacy snapshots must not rely on promisor-aware fsck.
	if err := m.validateSnapshotGit(context.Background(), input.Preparation.SessionID, input.Manifest.Repositories[0], path); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("inspection accepted promisor policy", err)
	}
}
