// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaimedRemovalPreservesUncapturedWritesDuringUnlink(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, mutation := range []string{"add-root", "add-child", "replace", "modify", "last-root"} {
			t.Run(string(action)+"/"+mutation, func(t *testing.T) {
				m, prepare, manifest := chatExecutionFixture(t)
				if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
				preview := storageDo(t, m, input)
				input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = action, domain.NewID(), domain.NewID(), preview.PreviewDigest
				source := filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
				prefix := "chat"
				if action == StorageDelete {
					input.Action = StorageCreate
					created := storageDo(t, m, input)
					input.Action, input.OperationID, input.SnapshotDigest, input.SnapshotMetadata = StorageDelete, domain.NewID(), created.Snapshot.SHA256, created.Snapshot
					source = m.snapshotPath(input.SnapshotID)
					prefix = "workspace/chat"
				}
				// Go's Windows OpenRoot(path) omits delete sharing and blocks the
				// namespace rename. A child opened through its stable parent permits
				// rename while keeping writes anchored to the original directory.
				parent, err := os.OpenRoot(filepath.Dir(source))
				if err != nil {
					t.Fatal(err)
				}
				defer parent.Close()
				held, err := parent.OpenRoot(filepath.Base(source))
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				changed := prefix + "/keep"
				trigger := changed
				if mutation == "last-root" {
					trigger = "."
				}
				raced := false
				m.storageBeforeRemovalUnlink = func(relative string) {
					if raced || relative != trigger {
						return
					}
					raced = true
					switch mutation {
					case "add-root", "last-root":
						changed = "late"
					case "add-child":
						changed = prefix + "/late"
					case "replace":
						if err := held.Remove(filepath.FromSlash(changed)); err != nil {
							t.Fatal(err)
						}
					}
					if err := held.WriteFile(filepath.FromSlash(changed), []byte("uncaptured writer bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				result, err := m.Storage(context.Background(), input)
				if !raced || domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified || result.RemovedSourceBytes != 0 {
					t.Fatal("uncaptured mutation completed removal", result, err)
				}
				removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
				if raw, err := os.ReadFile(filepath.Join(removal, filepath.FromSlash(changed))); err != nil || string(raw) != "uncaptured writer bytes" {
					t.Fatal("uncaptured bytes were removed", err)
				}
				if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("recovery adopted uncaptured bytes", err)
				}
				if action == StorageCleanup {
					if _, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID); err != nil {
						t.Fatal("recoverable original snapshot was changed", err)
					}
				} else if raw, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "keep")); err != nil || string(raw) != "original" {
					t.Fatal("live workspace was changed by snapshot deletion", err)
				}
			})
		}
	}
}
