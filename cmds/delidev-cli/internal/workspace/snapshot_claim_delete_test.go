// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRemoveClaimedStorageRemovalRejectsForeignNamespaceBytes(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	m.storageBeforeRemovalUnlink = func(relative string) {
		if relative == "chat/keep" {
			if err := os.WriteFile(filepath.Join(removal, "foreign"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign namespace bytes did not retain recovery", err)
	}
	m.storageBeforeRemovalUnlink = nil
	if err := m.RemoveClaimedStorageRemoval(context.Background(), input.OperationID, input.Preparation.SessionID, input.SnapshotID); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("generic namespace replacement was accepted", err)
	}
	if raw, err := os.ReadFile(filepath.Join(removal, "foreign")); err != nil || string(raw) != "preserve" {
		t.Fatal("foreign namespace bytes were removed", err)
	}
}

func TestRemoveClaimedStorageRemovalRejectsUnclaimedMissingEntry(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "missing"), []byte("must be claimed"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	m.storageBeforeRemovalUnlink = func(relative string) {
		if relative != "chat/keep" {
			return
		}
		matches, err := filepath.Glob(filepath.Join(removal, ".removing-*", "missing"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("missing private removal entry: %v (%v)", matches, err)
		}
		if err := os.Remove(matches[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unclaimed missing entry did not retain recovery", err)
	}
	m.storageBeforeRemovalUnlink = nil
	if err := m.RemoveClaimedStorageRemoval(context.Background(), input.OperationID, input.Preparation.SessionID, input.SnapshotID); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unclaimed missing entry was accepted as removed", err)
	}
}

func TestRemovalClaimCompactionPreservesClearedProofs(t *testing.T) {
	m := manager(t)
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "storage-removal-claims")); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StorageCleanup, Preparation: PrepareRequest{SessionID: domain.NewID()}, SnapshotID: domain.NewID()}
	intentRaw, err := json.Marshal(storageRemovalIntent{Version: 1, OperationID: input.OperationID, SessionID: input.Preparation.SessionID, SnapshotID: input.SnapshotID, Action: input.Action})
	if err != nil {
		t.Fatal(err)
	}
	intentDigest := sha256.Sum256(intentRaw)
	claimRaw, err := json.Marshal(storageRemovalClaim{Version: 2, RootIdentity: "root", Reference: removalReference(input), IntentDigest: hex.EncodeToString(intentDigest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(m.removalClaimPath(input.OperationID), claimRaw); err != nil {
		t.Fatal(err)
	}
	const proofCount = 4096
	journal := make([]byte, 0, maxStorageRemovalClaim/2+1)
	for i := 0; i < proofCount; i++ {
		record, err := json.Marshal(storageRemovalRenameRecord{
			Original: fmt.Sprintf("entry-%04d-%s", i, strings.Repeat("o", 520)),
			Private:  fmt.Sprintf("private-%04d-%s", i, strings.Repeat("p", 520)),
			State:    storageRemovalRenameCleared,
		})
		if err != nil {
			t.Fatal(err)
		}
		journal = append(journal, record...)
		journal = append(journal, '\n')
	}
	if len(journal) < maxStorageRemovalClaim/2 || len(journal) >= maxStorageRemovalClaim {
		t.Fatalf("fixture journal has unexpected size: %d", len(journal))
	}
	if err := security.WriteAtomic(m.removalClaimJournalPath(input.OperationID), journal); err != nil {
		t.Fatal(err)
	}
	if err := m.compactRemovalClaim(context.Background(), input, nil); err != nil {
		t.Fatal(err)
	}
	_, pending, cleared, err := m.readRemovalClaimPending(input, intentRaw)
	if err != nil || len(pending) != 0 || len(cleared) != proofCount {
		t.Fatalf("compaction lost cleared proof frontier: pending=%d cleared=%d err=%v", len(pending), len(cleared), err)
	}
}
