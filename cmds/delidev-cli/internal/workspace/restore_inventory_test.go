// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRestoreRechecksPublishedBytesBeforeOwnership(t *testing.T) {
	for _, mutation := range []string{"add", "modify", "remove", "git-worktree"} {
		t.Run(mutation, func(t *testing.T) {
			var m *Manager
			var input StorageRequest
			if mutation == "git-worktree" {
				m = manager(t)
				input, _ = snapshotRequest(t, m, false)
				preview := storageDo(t, m, input)
				input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
				cleaned := storageDo(t, m, input)
				input.Action, input.OperationID, input.PreviousState, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, cleaned.Snapshot.SHA256
			} else {
				m, input = storedGeneralChat(t)
			}
			root := filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))
			relative := filepath.Join("chat", "keep")
			if mutation == "git-worktree" {
				relative = filepath.Join(string(input.Manifest.Repositories[0].ID), "tracked.txt")
			}
			m.storageBeforeRestorePublish = func(staging string) {
				path := filepath.Join(staging, relative)
				var err error
				switch mutation {
				case "add":
					path = filepath.Join(staging, "chat", "late")
					err = os.WriteFile(path, []byte("uncaptured late bytes"), 0600)
				case "modify", "git-worktree":
					err = os.WriteFile(path, []byte("uncaptured changed bytes"), 0600)
				case "remove":
					err = os.Remove(path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := m.Storage(context.Background(), input)
			if domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
				t.Fatal("changed publication was reported exact", result, err)
			}
			raw, err := security.ReadPrivate(m.restoreBindingPath(input.Preparation.SessionID), 4096)
			var binding restoreBinding
			if err != nil || domain.Decode(raw, &binding) != nil || binding.Published {
				t.Fatal("changed publication acquired ownership", err)
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed publication was adopted by recovery", err)
			}
			path := filepath.Join(root, relative)
			if mutation == "add" {
				path = filepath.Join(root, "chat", "late")
			}
			if mutation == "remove" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("missing bytes were silently reconstructed", err)
				}
			} else if raw, err := os.ReadFile(path); err != nil || len(raw) == 0 || string(raw) == "original" {
				t.Fatal("raced bytes were lost", err)
			}
			if _, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID); err != nil {
				t.Fatal("original snapshot was changed", err)
			}
		})
	}
}
