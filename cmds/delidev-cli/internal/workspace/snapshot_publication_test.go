// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func storedGeneralChat(t *testing.T) (*Manager, StorageRequest) {
	t.Helper()
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	input.Action, input.OperationID, input.PreviousState = StorageRestore, domain.NewID(), domain.WorkspaceStored
	input.PreviousSnapshotID, input.SnapshotDigest = input.SnapshotID, cleaned.Snapshot.SHA256
	return m, input
}

func TestSnapshotRestoreRecoveryRejectsMatchingForeignPublication(t *testing.T) {
	m, input := storedGeneralChat(t)
	root := filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))
	m.storageBeforeRestorePublish = func(string) {
		// Another writer publishes identical bytes after destination inspection.
		// The restore's no-replace rename must fail without adopting this tree.
		if _, err := walkSnapshot(context.Background(), filepath.Join(m.snapshotPath(input.SnapshotID), "workspace"), root, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Storage(context.Background(), input); err == nil {
		t.Fatal("foreign destination did not block publication")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "snapshot-staging", string(input.OperationID))); !os.IsNotExist(err) {
		t.Fatal("unpublished scratch was not cleaned", err)
	}
	if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("matching foreign tree was adopted by recovery", err)
	}
	if _, err := m.verifyWorkspaceIdentity(context.Background(), input.Preparation, input.Manifest, continuationIdentity); err == nil {
		t.Fatal("pending restore binding granted execution identity")
	}
	work := domain.SessionDeletionWork{SessionID: input.Preparation.SessionID, MachineID: input.Preparation.MachineID, Copies: []domain.SessionDeletionCopy{{JobID: input.OperationID, SnapshotID: input.SnapshotID, Type: domain.WorkspaceStorageJob}}}
	if _, err := m.deletionRestoredWorkspace(context.Background(), work, input.Manifest); err == nil {
		t.Fatal("pending restore binding granted deletion authority")
	}
	raw, err := os.ReadFile(filepath.Join(input.Manifest.PrimaryPath, "keep"))
	if err != nil || string(raw) != "original" {
		t.Fatal("foreign workspace changed", err)
	}
	if _, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID); err != nil {
		t.Fatal("recoverable snapshot changed", err)
	}
}

func TestSnapshotRestoreRecoveryRequiresOriginalPublicationProof(t *testing.T) {
	for _, mutation := range []string{"missing", "pending", "operation", "snapshot", "digest"} {
		t.Run(mutation, func(t *testing.T) {
			m, input := storedGeneralChat(t)
			storageDo(t, m, input)
			path := m.restoreBindingPath(input.Preparation.SessionID)
			raw, err := security.ReadPrivate(path, 4096)
			var binding restoreBinding
			if err != nil || domain.Decode(raw, &binding) != nil || !binding.Published {
				t.Fatal("successful restore omitted publication proof", err)
			}
			switch mutation {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "pending":
				binding.Published = false
			case "operation":
				binding.OperationID = domain.NewID()
			case "snapshot":
				binding.SnapshotID = domain.NewID()
			case "digest":
				binding.SnapshotDigest = strings.Repeat("0", 64)
			}
			if mutation != "missing" {
				raw, _ = json.Marshal(binding)
				if err := security.WriteAtomic(path, raw); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("incomplete or foreign proof authorized recovery", err)
			}
		})
	}
}
