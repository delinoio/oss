// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaimedRemovalPreservesUncapturedWritesDuringUnlink(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, mutation := range []string{"add-root", "add-child", "replace", "modify", "replace-after-claim", "last-root"} {
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
				// rename while keeping writes anchored to the original directory. Close
				// the parent immediately so the fixture retains only the writer handle.
				parent, err := os.OpenRoot(filepath.Dir(source))
				if err != nil {
					t.Fatal(err)
				}
				held, err := parent.OpenRoot(filepath.Base(source))
				if err != nil {
					t.Fatal(err)
				}
				if err := parent.Close(); err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				changed := prefix + "/keep"
				trigger := changed
				if mutation == "last-root" {
					trigger = "."
				}
				heldParent := held
				parentPath := filepath.ToSlash(filepath.Dir(changed))
				if parentPath != "." {
					for _, component := range strings.Split(parentPath, "/") {
						heldParent, err = heldParent.OpenRoot(component)
						if err != nil {
							t.Fatal(err)
						}
						defer heldParent.Close()
					}
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
						if err := heldParent.Remove(filepath.Base(changed)); err != nil {
							t.Fatal(err)
						}
					}
					writerParent := heldParent
					if mutation == "add-root" || mutation == "last-root" {
						writerParent = held
					}
					if err := writerParent.WriteFile(filepath.Base(changed), []byte("uncaptured writer bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				m.storageAfterRemovalClaim = func(relative string) {
					if mutation != "replace-after-claim" || raced || relative != trigger {
						return
					}
					raced = true
					if err := heldParent.WriteFile(filepath.Base(changed), []byte("uncaptured writer bytes"), 0600); err != nil {
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

func TestClaimedRemovalRecoveryReusesRenamedPendingEntry(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	m.storageAfterRemovalClaim = func(relative string) {
		if relative == "chat/keep" {
			cancel()
		}
	}
	if _, err := m.Storage(ctx, input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("interrupted claimed removal did not retain recovery", err)
	}
	cancel()
	m.storageAfterRemovalClaim = nil
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.WorkspaceState != domain.WorkspaceStored || !recovered.CleanupVerified {
		t.Fatal("recovery did not reuse the renamed pending entry", recovered)
	}
}

func TestClaimedRemovalRecoveryUsesPreUnlinkProofAfterShortClear(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	ctx, cancel := context.WithCancel(context.Background())
	m.storageAfterRemovalProof = func(relative string) {
		if relative == "chat/keep" {
			cancel()
		}
	}
	if _, err := m.Storage(ctx, input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("cancellation after durable pre-unlink proof was not retained", err)
	}
	m.storageAfterRemovalProof = nil
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	// The claimed entry itself is renamed within its original parent.
	privateEntries, err := filepath.Glob(filepath.Join(removal, "chat", ".removing-*"))
	if err != nil || len(privateEntries) != 1 {
		t.Fatalf("durable private removal entry missing: %v (%v)", privateEntries, err)
	}
	intentRaw, err := os.ReadFile(m.removalIntentPath(input.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	_, pending, cleared, err := m.readRemovalClaimPending(input, intentRaw)
	if err != nil || len(pending) < 1 {
		t.Fatalf("pre-unlink proof was not retained: pending=%d cleared=%d err=%v", len(pending), len(cleared), err)
	}
	if _, ok := cleared[removalClearedDigest("chat/keep")]; !ok {
		t.Fatal("pre-unlink proof for the interrupted entry was not retained")
	}
	if err := os.Remove(privateEntries[0]); err != nil {
		t.Fatal(err)
	}
	journal, err := os.OpenFile(m.removalClaimJournalPath(input.OperationID), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.WriteString(`{"state":"cleared"`); err != nil {
		journal.Close()
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.WorkspaceState != domain.WorkspaceStored || !recovered.CleanupVerified || recovered.RecoveredJobState != domain.JobSucceeded {
		t.Fatal("recovery did not use the pre-unlink proof after a short clear write", recovered)
	}
}
