// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type storageRemovalIntent struct {
	Version     uint32            `json:"version"`
	OperationID domain.ID         `json:"operation_id"`
	SessionID   domain.ID         `json:"session_id"`
	SnapshotID  domain.ID         `json:"snapshot_id"`
	Action      StorageAction     `json:"action"`
	Inventory   snapshotInventory `json:"inventory"`
}

func (m *Manager) removalIntentPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-intents", string(id)+".json")
}
func (m *Manager) retainRemovalIntent(ctx context.Context, r StorageRequest, path string) error {
	inventory, err := walkSnapshot(ctx, path, "", nil)
	if err != nil {
		return err
	}
	intent := storageRemovalIntent{Version: 1, OperationID: r.OperationID, SessionID: r.Preparation.SessionID, SnapshotID: r.SnapshotID, Action: r.Action, Inventory: inventory}
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > maxSnapshotManifest {
		return ResultUncertain()
	}
	if old, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest); err == nil {
		if string(old) != string(raw) {
			return ResultUncertain()
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	return security.WriteAtomic(m.removalIntentPath(r.OperationID), raw)
}
func (m *Manager) confirmRemoval(ctx context.Context, r StorageRequest, path string, partial bool) error {
	raw, err := security.ReadPrivate(m.removalIntentPath(r.OperationID), maxSnapshotManifest)
	if err != nil {
		return ResultUncertain()
	}
	var intent storageRemovalIntent
	if domain.Decode(raw, &intent) != nil || intent.Version != 1 || intent.OperationID != r.OperationID || intent.SessionID != r.Preparation.SessionID || intent.SnapshotID != r.SnapshotID || intent.Action != r.Action {
		return ResultUncertain()
	}
	current, err := walkSnapshot(ctx, path, "", nil)
	if err != nil {
		return err
	}
	if !partial && inventoryDigest(current) != inventoryDigest(intent.Inventory) {
		return ResultUncertain()
	}
	if partial {
		expected := map[string]snapshotEntry{}
		for _, entry := range intent.Inventory.Entries {
			expected[entry.Path] = entry
		}
		for _, entry := range current.Entries {
			old, ok := expected[entry.Path]
			if !ok {
				return ResultUncertain()
			}
			if os.FileMode(old.Mode).IsDir() && os.FileMode(entry.Mode).IsDir() {
				continue
			}
			if !reflect.DeepEqual(old, entry) {
				return ResultUncertain()
			}
		}
	}
	return nil
}
func storageExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, ResultUncertain()
	}
	return true, nil
}

// Explicit recovery inspects the original immutable operation and journal. It
// never repeats snapshot creation, source rename or restoration publication.
// Only already claimed, independently inventoried removals may be continued.
func (m *Manager) recoverStorage(ctx context.Context, r StorageRequest, result StorageResult) (StorageResult, error) {
	original := r.Recovery.Original
	result.RecoveredJobID = original.OperationID
	result.RecoveredJobState = domain.JobFailed
	result.WorkspaceState = original.PreviousState
	if result.WorkspaceState == "" {
		result.WorkspaceState = domain.WorkspacePresent
	}
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	removal := filepath.Join(m.Root, "workspace-removals", string(original.OperationID))
	staging := filepath.Join(m.Root, "snapshot-staging", string(original.OperationID))
	live, err := storageExists(root)
	if err != nil {
		return result, err
	}
	removed, err := storageExists(removal)
	if err != nil {
		return result, err
	}
	staged, err := storageExists(staging)
	if err != nil {
		return result, err
	}
	if original.Action == StorageCleanup && live && removed {
		return result, ResultUncertain()
	}
	snapshotExists := false
	var snapshot snapshotManifest
	var metadata SnapshotMetadata
	if original.SnapshotID != "" {
		snapshotExists, err = storageExists(m.snapshotPath(original.SnapshotID))
		if err != nil {
			return result, err
		}
		if snapshotExists {
			snapshot, metadata, err = m.inspectSnapshot(ctx, original.SnapshotID)
			if err != nil {
				return result, err
			}
			if metadata.SessionID != r.Preparation.SessionID || metadata.MachineID != r.Preparation.MachineID || manifestDigest(snapshot.Workspace) != manifestDigest(r.Manifest) || (original.SnapshotDigest != "" && metadata.SHA256 != original.SnapshotDigest) {
				return result, ResultUncertain()
			}
			if (original.Action == StorageCreate || original.Action == StorageCleanup) && snapshot.OperationID != original.OperationID {
				return result, ResultUncertain()
			}
			result.Snapshot = &metadata
		}
	}
	switch original.Action {
	case StoragePreview, StorageCreate, StorageCleanup:
		if live {
			manifest, err := m.Read(r.Preparation.SessionID)
			if err != nil || manifestDigest(manifest) != manifestDigest(r.Manifest) {
				return result, ResultUncertain()
			}
			if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, manifest, continuationIdentity); err != nil {
				return result, err
			}
			_, result.SourceBytes, result.PreviewDigest, err = m.storageObservation(ctx, original)
			if err != nil {
				return result, err
			}
			result.WorkspaceState = domain.WorkspacePresent
			if original.Action == StorageCreate && snapshotExists {
				result.RecoveredJobState = domain.JobSucceeded
			}
		} else {
			if original.Action != StorageCleanup || !snapshotExists {
				return result, ResultUncertain()
			}
			if removed {
				if err := m.confirmRemoval(ctx, original, removal, true); err != nil {
					return result, err
				}
				if err := removeSnapshotTree(ctx, removal); err != nil {
					return result, ResultUncertain()
				}
			}
			if err := security.SyncParent(root); err != nil {
				return result, ResultUncertain()
			}
			if err := security.SyncParent(removal); err != nil {
				return result, ResultUncertain()
			}
			result.WorkspaceState = domain.WorkspaceStored
			result.RecoveredJobState = domain.JobSucceeded
			result.SourceBytes = snapshot.SourceBytes
			result.RemovedSourceBytes = snapshot.SourceBytes
		}
		if staged {
			if err := removeSnapshotTree(ctx, staging); err != nil {
				return result, ResultUncertain()
			}
		}
	case StorageRestore:
		if !snapshotExists || removed {
			return result, ResultUncertain()
		}
		if live {
			if staged {
				return result, ResultUncertain()
			}
			current, err := walkSnapshot(ctx, root, "", nil)
			if err != nil || inventoryDigest(current) != inventoryDigest(snapshot.Inventory) {
				return result, ResultUncertain()
			}
			if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, r.Manifest, continuationIdentity); err != nil {
				return result, err
			}
			result.WorkspaceState = domain.WorkspacePresent
			result.RecoveredJobState = domain.JobSucceeded
		} else {
			if staged {
				current, err := walkSnapshot(ctx, staging, "", nil)
				if err != nil || inventoryDigest(current) != inventoryDigest(snapshot.Inventory) {
					return result, ResultUncertain()
				}
				if err := removeSnapshotTree(ctx, staging); err != nil {
					return result, ResultUncertain()
				}
			}
			result.WorkspaceState = domain.WorkspaceStored
		}
	case StorageInspect:
		if !snapshotExists {
			return result, ResultUncertain()
		}
		result.RecoveredJobState = domain.JobSucceeded
	case StorageDelete:
		if snapshotExists && removed {
			return result, ResultUncertain()
		}
		if !snapshotExists {
			if original.SnapshotMetadata == nil || original.SnapshotMetadata.ID != original.SnapshotID || original.SnapshotMetadata.SHA256 != original.SnapshotDigest {
				return result, ResultUncertain()
			}
			if removed {
				if err := m.confirmRemoval(ctx, original, removal, true); err != nil {
					return result, err
				}
				if err := removeSnapshotTree(ctx, removal); err != nil {
					return result, ResultUncertain()
				}
			}
			if err := security.SyncParent(m.snapshotPath(original.SnapshotID)); err != nil {
				return result, ResultUncertain()
			}
			metadata = *original.SnapshotMetadata
			metadata.Deleted = true
			result.Snapshot = &metadata
			result.RecoveredJobState = domain.JobSucceeded
		}
	default:
		return result, ResultUncertain()
	}
	retained, err := m.snapshotBytes(ctx, r.Preparation.SessionID)
	if err != nil {
		return result, err
	}
	result.RetainedSnapshotBytes = retained
	result.CapacityBytes, result.FreeBytesAfter = storageCapacity(m.Root)
	result.CleanupVerified = true
	return result, nil
}
