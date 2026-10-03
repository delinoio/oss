// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotReadRestoreAndDeleteAccounting(t *testing.T) {
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "payload"), []byte("retained original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
	first := storageDo(t, m, input)
	input.OperationID, input.SnapshotID = domain.NewID(), domain.NewID()
	second := storageDo(t, m, input)
	if first.SourceBytes == 0 || second.RetainedSnapshotBytes != first.Snapshot.SizeBytes+second.Snapshot.SizeBytes {
		t.Fatal("fixture has no retained source cost")
	}
	// A different session's snapshot must not enter this session's accounting.
	other := PrepareRequest{SessionID: domain.NewID(), MachineID: prepare.MachineID, Type: domain.GeneralChat}
	otherManifest, err := m.Prepare(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	storageDo(t, m, StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: other, Manifest: otherManifest, SnapshotID: domain.NewID()})
	// Inspection describes the pinned source, even if live data has since grown.
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "later"), []byte("new live contents"), 0600); err != nil {
		t.Fatal(err)
	}
	input.Action, input.OperationID, input.SnapshotID, input.SnapshotDigest = StorageInspect, domain.NewID(), first.Snapshot.ID, first.Snapshot.SHA256
	inspected := storageDo(t, m, input)
	if inspected.SourceBytes != first.SourceBytes || inspected.RetainedSnapshotBytes != second.RetainedSnapshotBytes || inspected.RemovedSourceBytes != 0 {
		t.Fatal("inspection accounting lost the pinned source or retained inventory", inspected)
	}
	input.Action, input.OperationID = StoragePreview, domain.NewID()
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	total := first.Snapshot.SizeBytes + second.Snapshot.SizeBytes + cleaned.Snapshot.SizeBytes
	input.Action, input.OperationID, input.PreviousState, input.SnapshotID, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, first.Snapshot.ID, first.Snapshot.SHA256
	restored := storageDo(t, m, input)
	if restored.SourceBytes != first.SourceBytes || restored.RetainedSnapshotBytes != total || restored.RemovedSourceBytes != 0 {
		t.Fatal("restoration accounting lost retained costs", restored)
	}
	input.Action, input.OperationID, input.PreviousState, input.SnapshotID, input.SnapshotDigest, input.SnapshotMetadata = StorageDelete, domain.NewID(), domain.WorkspacePresent, second.Snapshot.ID, second.Snapshot.SHA256, second.Snapshot
	deleted := storageDo(t, m, input)
	if deleted.SourceBytes != second.SourceBytes || deleted.RetainedSnapshotBytes != total-second.Snapshot.SizeBytes || deleted.RemovedSourceBytes != 0 || !deleted.Snapshot.Deleted {
		t.Fatal("deletion did not report post-action retained costs", deleted)
	}
	// Deleting the final two snapshots reaches zero without removing live files
	// or charging the unrelated session's retained copy.
	for _, snapshot := range []*SnapshotMetadata{cleaned.Snapshot, first.Snapshot} {
		input.OperationID, input.SnapshotID, input.SnapshotDigest, input.SnapshotMetadata = domain.NewID(), snapshot.ID, snapshot.SHA256, snapshot
		deleted = storageDo(t, m, input)
	}
	if deleted.SourceBytes != first.SourceBytes || deleted.RetainedSnapshotBytes != 0 || deleted.RemovedSourceBytes != 0 {
		t.Fatal("final deletion accounting is wrong", deleted)
	}
	if _, err := os.Stat(filepath.Join(manifest.PrimaryPath, "payload")); err != nil {
		t.Fatal("snapshot deletion removed the restored source", err)
	}
}
