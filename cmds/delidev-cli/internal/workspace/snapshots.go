// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type StorageAction string

const (
	StoragePreview StorageAction = "preview"
	StorageCreate  StorageAction = "create"
	StorageCleanup StorageAction = "cleanup"
	StorageInspect StorageAction = "inspect"
	StorageRestore StorageAction = "restore"
	StorageDelete  StorageAction = "delete"
	StorageRecover StorageAction = "recover"
)

func (a StorageAction) Valid() bool {
	switch a {
	case StoragePreview, StorageCreate, StorageCleanup, StorageInspect, StorageRestore, StorageDelete, StorageRecover:
		return true
	}
	return false
}

type StorageRequest struct {
	PreviousSnapshotID domain.ID                    `json:"previous_snapshot_id,omitempty"`
	PreviousState      domain.WorkspaceStorageState `json:"previous_state"`
	SnapshotMetadata   *SnapshotMetadata            `json:"snapshot_metadata,omitempty"`
	Recovery           *StorageRecovery             `json:"recovery,omitempty"`
	Version            uint32                       `json:"version"`
	OperationID        domain.ID                    `json:"operation_id"`
	Action             StorageAction                `json:"action"`
	Preparation        PrepareRequest               `json:"preparation"`
	Manifest           Manifest                     `json:"manifest"`
	SnapshotID         domain.ID                    `json:"snapshot_id,omitempty"`
	PreviewDigest      string                       `json:"preview_digest,omitempty"`
	SnapshotDigest     string                       `json:"snapshot_digest,omitempty"`
}

func (r StorageRequest) Validate() error {
	if r.Version != 1 || r.OperationID.Validate() != nil || !r.Action.Valid() || r.Preparation.validateStructure() != nil {
		return ResultUncertain()
	}
	if r.Preparation.Type == domain.Local {
		return domain.Fail(domain.PermissionDenied, "Original Local checkouts cannot be snapshotted or cleaned through managed storage.", "Use your own Git/backup workflow; DeliDev will never remove these checkouts.")
	}
	if ValidateResult(r.Preparation, r.Manifest, runtime.GOOS) != nil {
		return ResultUncertain()
	}
	if r.Action == StorageRecover {
		if r.Recovery == nil || r.Recovery.Original.Action == StorageRecover || r.Recovery.Original.Validate() != nil || r.Recovery.InstanceID.Validate() != nil || r.Recovery.Revision == 0 || !digestValid(r.Recovery.AssignmentDigest) || r.Recovery.Original.Preparation.SessionID != r.Preparation.SessionID || r.SnapshotID != r.Recovery.Original.SnapshotID {
			return ResultUncertain()
		}
		if len(r.Recovery.Claims) < 1 || len(r.Recovery.Claims) > 8 {
			return ResultUncertain()
		}
		first := r.Recovery.Claims[0]
		if first.InstanceID != r.Recovery.InstanceID || first.Revision != r.Recovery.Revision || first.AssignmentDigest != r.Recovery.AssignmentDigest {
			return ResultUncertain()
		}
		ids := []domain.ID{}
		for _, claim := range r.Recovery.Claims {
			if claim.JobID.Validate() != nil || claim.InstanceID.Validate() != nil || claim.Revision == 0 || !digestValid(claim.AssignmentDigest) {
				return ResultUncertain()
			}
			ids = append(ids, claim.JobID)
		}
		if domain.UniqueIDs(ids) != nil {
			return ResultUncertain()
		}
		return nil
	}
	if r.PreviousState != domain.WorkspacePresent && r.PreviousState != domain.WorkspaceStored {
		return ResultUncertain()
	}
	if r.Recovery != nil {
		return ResultUncertain()
	}
	if r.Action != StoragePreview && r.SnapshotID.Validate() != nil {
		return ResultUncertain()
	}
	if r.Action == StorageCleanup && !digestValid(r.PreviewDigest) {
		return ResultUncertain()
	}
	if (r.Action == StorageInspect || r.Action == StorageRestore || r.Action == StorageDelete) && !digestValid(r.SnapshotDigest) {
		return ResultUncertain()
	}
	return nil
}
func digestValid(s string) bool { return len(s) == 64 && canonicalCommit(s) }

type StorageJournalClaim struct {
	JobID            domain.ID `json:"job_id"`
	InstanceID       domain.ID `json:"instance_id"`
	Revision         uint64    `json:"revision,string"`
	AssignmentDigest string    `json:"assignment_digest"`
}

type StorageRecovery struct {
	Claims           []StorageJournalClaim `json:"claims"`
	Original         StorageRequest        `json:"original"`
	InstanceID       domain.ID             `json:"instance_id"`
	Revision         uint64                `json:"revision,string"`
	AssignmentDigest string                `json:"assignment_digest"`
}

type SnapshotMetadata struct {
	ID              domain.ID `json:"id"`
	SessionID       domain.ID `json:"session_id"`
	MachineID       domain.ID `json:"machine_id"`
	SHA256          string    `json:"sha256"`
	SizeBytes       uint64    `json:"size_bytes,string"`
	CreatedAt       time.Time `json:"created_at"`
	RepositoryCount uint32    `json:"repository_count"`
	Deleted         bool      `json:"deleted,omitempty"`
}

// Logical byte accounting explicitly includes the retained snapshot. Filesystem
// free observations are separate: concurrent allocations, compression and shared
// blocks make a logical byte subtraction insufficient proof of reclaimed space.
type StorageResult struct {
	WorkspaceState        domain.WorkspaceStorageState `json:"workspace_state"`
	RecoveredJobID        domain.ID                    `json:"recovered_job_id,omitempty"`
	RecoveredJobState     domain.JobState              `json:"recovered_job_state,omitempty"`
	Version               uint32                       `json:"version"`
	OperationID           domain.ID                    `json:"operation_id"`
	Action                StorageAction                `json:"action"`
	SessionID             domain.ID                    `json:"session_id"`
	MachineID             domain.ID                    `json:"machine_id"`
	SourceBytes           uint64                       `json:"source_bytes,string"`
	RetainedSnapshotBytes uint64                       `json:"retained_snapshot_bytes,string"`
	RemovedSourceBytes    uint64                       `json:"removed_source_bytes,string"`
	FreeBytesBefore       *uint64                      `json:"free_bytes_before,omitempty,string"`
	FreeBytesAfter        *uint64                      `json:"free_bytes_after,omitempty,string"`
	CapacityBytes         *uint64                      `json:"capacity_bytes,omitempty,string"`
	PreviewDigest         string                       `json:"preview_digest,omitempty"`
	Snapshot              *SnapshotMetadata            `json:"snapshot,omitempty"`
	CleanupVerified       bool                         `json:"cleanup_verified"`
}
type snapshotManifest struct {
	SourceInventory  snapshotInventory `json:"source_inventory"`
	SourceBytes      uint64            `json:"source_bytes,string"`
	SourceDigest     string            `json:"source_digest"`
	Version          uint32            `json:"version"`
	ID               domain.ID         `json:"id"`
	OperationID      domain.ID         `json:"operation_id"`
	Workspace        Manifest          `json:"workspace"`
	Preparation      PrepareRequest    `json:"preparation"`
	OriginalIdentity string            `json:"original_identity"`
	Inventory        snapshotInventory `json:"inventory"`
	CreatedAt        time.Time         `json:"created_at"`
}
type restoreBinding struct {
	DirectoryIdentity string    `json:"directory_identity,omitempty"`
	Version           uint32    `json:"version"`
	OperationID       domain.ID `json:"operation_id"`
	SessionID         domain.ID `json:"session_id"`
	SnapshotID        domain.ID `json:"snapshot_id"`
	SnapshotDigest    string    `json:"snapshot_digest"`
	Published         bool      `json:"published"`
	ManifestDigest    string    `json:"manifest_digest"`
	OriginalIdentity  string    `json:"original_identity"`
}

func manifestDigest(m Manifest) string {
	raw, _ := json.Marshal(m)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (m *Manager) snapshotPath(id domain.ID) string {
	return filepath.Join(m.Root, "snapshots", string(id))
}
func (m *Manager) restoreBindingPath(id domain.ID) string {
	return filepath.Join(m.Root, "workspace-restores", string(id)+".json")
}

func (m *Manager) Storage(ctx context.Context, r StorageRequest) (result StorageResult, returned error) {
	if err := r.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := m.initialize(); err != nil {
		return result, err
	}
	for _, dir := range []string{"snapshots", "snapshot-staging", "workspace-removals", "workspace-restores", "storage-removal-intents", "storage-removal-claims"} {
		if err := security.PrivateDir(filepath.Join(m.Root, dir)); err != nil {
			return result, err
		}
	}
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(r.Preparation.SessionID)+".lock"))
	if err != nil {
		return result, err
	}
	defer lock.Close()
	var restoreStaging string
	defer func() {
		if returned != nil {
			if restoreStaging != "" {
				// The operation created this private scratch namespace. Cleanup must
				// outlive caller cancellation; retained bytes keep recovery ownership.
				cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
				err := removeSnapshotTree(cleanup, restoreStaging)
				stop()
				if err != nil {
					m.Logger.Warn("restore_scratch_cleanup_pending", "operation_id", r.OperationID, "code", domain.SafeError(err).Code)
					returned = ResultUncertain()
				}
			}
			// Publication is already a native side effect, even before source
			// removal. Preserve recovery ownership so cancellation/failure cannot
			// orphan the verified snapshot outside server metadata.
			if r.Action == StorageCleanup && result.Snapshot != nil {
				returned = ResultUncertain()
			}
			if storageFull(returned) {
				returned = domain.Fail(domain.ResourceExhausted, "Worker storage capacity or quota is exhausted.", "Preserve the original operation and copies; free unrelated space before requesting new work.")
			}
			m.Logger.Warn("workspace_storage_failed", "session_id", r.Preparation.SessionID, "operation_id", r.OperationID, "action", r.Action, "code", domain.SafeError(returned).Code)
		}
	}()
	if err := m.noActiveExecutionClaim(r.Preparation.SessionID); err != nil {
		return result, err
	}
	if claim, err := m.readExecutionClaim(r.Preparation.SessionID); err == nil {
		if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, claim.JobID); err != nil {
			return result, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, ResultUncertain()
	}
	if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, r.Preparation.SessionID); err != nil {
		return result, err
	}
	result = StorageResult{WorkspaceState: r.PreviousState, Version: 1, OperationID: r.OperationID, Action: r.Action, SessionID: r.Preparation.SessionID, MachineID: r.Preparation.MachineID}
	result.CapacityBytes, result.FreeBytesBefore = storageCapacity(m.Root)
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	switch r.Action {
	case StorageRecover:
		return m.recoverStorage(ctx, r, result)
	case StoragePreview, StorageCreate, StorageCleanup:
		live, err := m.Read(r.Preparation.SessionID)
		if err != nil || manifestDigest(live) != manifestDigest(r.Manifest) {
			return result, ResultUncertain()
		}
		identity, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, live, continuationIdentity)
		if err != nil {
			return result, err
		}
		// Worktree root may contain only the accepted manifest and its complete owned
		// repository set. Extra resources are dependent cleanup, never implicit scope.
		if err := validateStorageRoot(root, live); err != nil {
			return result, err
		}
		observation, err := m.storageObservation(ctx, r)
		if err != nil {
			return result, err
		}
		for _, entry := range observation.Data.Entries {
			// Only declared repositories receive independent Git closure validation.
			// Nested administration, including filesystem case aliases, may hide external stores.
			if strings.EqualFold(filepath.Base(entry.Path), ".git") {
				return result, snapshotUnsupported()
			}
		}
		result.SourceBytes = observation.Whole.Bytes
		result.PreviewDigest = observation.Digest
		retained, err := m.snapshotBytes(ctx, r.Preparation.SessionID)
		if err != nil {
			return result, err
		}
		result.RetainedSnapshotBytes = retained
		if r.Action == StoragePreview {
			result.CleanupVerified = true
			return result, nil
		}
		if r.Action == StorageCleanup && result.PreviewDigest != r.PreviewDigest {
			return result, domain.Fail(domain.Conflict, "Workspace contents changed after the cleanup preview.", "Request a fresh preview before cleanup.")
		}
		snapshot, err := m.createSnapshot(ctx, r, identity, observation.Data, result.PreviewDigest)
		if err != nil {
			return result, err
		}
		result.Snapshot = &snapshot
		if m.storageAfterSnapshot != nil {
			m.storageAfterSnapshot()
		}
		result.RetainedSnapshotBytes += snapshot.SizeBytes
		if r.Action == StorageCleanup {
			// All repositories and independent object stores are synchronized and fully
			// re-read before this single source namespace transition. No per-repository
			// deletion can precede successful whole-workspace snapshot publication.
			observation, err := m.storageObservation(ctx, r)
			if err != nil || observation.Digest != result.PreviewDigest {
				return result, ResultUncertain()
			}
			if err := process.ReconcileOwnerContext(ctx, m.Git.ProcessRoot, r.Preparation.SessionID); err != nil {
				return result, err
			}
			removal := filepath.Join(m.Root, "workspace-removals", string(r.OperationID))
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := m.retainRemovalIntent(ctx, r, root, result.Snapshot.SHA256); err != nil {
				return result, err
			}
			if m.storageBeforeRemovalClaim != nil {
				m.storageBeforeRemovalClaim()
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := renameStorage(root, removal); err != nil {
				return result, err
			}
			if err := m.confirmRemoval(ctx, r, removal, false); err != nil {
				// No unlink occurred. Preserve raced user bytes at their original
				// name when possible; a foreign replacement keeps the claim private.
				if restoreErr := renameStorage(removal, root); restoreErr != nil {
					m.Logger.Warn("storage_claim_restore_pending", "operation_id", r.OperationID, "code", domain.SafeError(restoreErr).Code)
				}
				return result, ResultUncertain()
			}
			if err := removeSnapshotTree(ctx, removal); err != nil {
				return result, ResultUncertain()
			}
			result.RemovedSourceBytes = result.SourceBytes
			result.WorkspaceState = domain.WorkspaceStored
		}
		result.CleanupVerified = true
	case StorageInspect, StorageRestore, StorageDelete:
		snap, metadata, err := m.inspectSnapshot(ctx, r.SnapshotID)
		if err != nil {
			return result, err
		}
		if metadata.SessionID != r.Preparation.SessionID || metadata.MachineID != r.Preparation.MachineID || metadata.SHA256 != r.SnapshotDigest || manifestDigest(snap.Workspace) != manifestDigest(r.Manifest) {
			return result, ResultUncertain()
		}
		result.Snapshot = &metadata
		result.SourceBytes = snap.SourceBytes
		switch r.Action {
		case StorageRestore:
			result.WorkspaceState = domain.WorkspacePresent
			if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
				return result, domain.Fail(domain.Conflict, "The owned restoration destination is occupied.", "Preserve existing files; restore only after confirmed cleanup.")
			}
			staging := filepath.Join(m.Root, "snapshot-staging", string(r.OperationID))
			if err := os.Mkdir(staging, 0700); err != nil {
				return result, ResultUncertain()
			}
			restoreStaging = staging
			if m.storageRestoreCopyFault != nil {
				if err := m.storageRestoreCopyFault(staging); err != nil {
					return result, err
				}
			}
			if _, err := walkSnapshot(ctx, filepath.Join(m.snapshotPath(r.SnapshotID), "workspace"), staging, nil, true); err != nil {
				return result, err
			}
			inventory, err := walkSnapshot(ctx, staging, "", nil)
			if err != nil || inventoryDigest(inventory) != inventoryDigest(snap.Inventory) {
				return result, ResultUncertain()
			}
			binding := restoreBinding{Version: 2, OperationID: r.OperationID, SessionID: r.Preparation.SessionID, SnapshotID: r.SnapshotID, SnapshotDigest: metadata.SHA256, ManifestDigest: manifestDigest(snap.Workspace), OriginalIdentity: snap.OriginalIdentity}
			binding.DirectoryIdentity, err = restoredDirectoryIdentity(staging, snap.Workspace)
			if err != nil {
				return result, ResultUncertain()
			}
			// Pending comparison metadata grants no publication authority. Invalidate
			// an earlier restore before claiming this operation's destination.
			raw, _ := json.Marshal(binding)
			if err := security.WriteAtomic(m.restoreBindingPath(r.Preparation.SessionID), raw); err != nil {
				return result, err
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if m.storageBeforeRestorePublish != nil {
				m.storageBeforeRestorePublish(staging)
			}
			if err := renameStorage(staging, root); err != nil {
				return result, err
			}
			restoreStaging = ""
			// Only a synchronized successful no-replace publication may establish
			// ownership. Matching foreign bytes or a missing staging tree cannot.
			identity, err := restoredDirectoryIdentity(root, snap.Workspace)
			if err != nil || identity != binding.DirectoryIdentity {
				return result, ResultUncertain()
			}
			binding.Published = true
			raw, _ = json.Marshal(binding)
			if err := security.WriteAtomic(m.restoreBindingPath(r.Preparation.SessionID), raw); err != nil {
				m.Logger.Warn("restore_publication_proof_pending", "operation_id", r.OperationID, "code", domain.SafeError(err).Code)
				return result, ResultUncertain()
			}
			if _, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, r.Manifest, continuationIdentity); err != nil {
				return result, ResultUncertain()
			}
		case StorageDelete:
			removal := filepath.Join(m.Root, "workspace-removals", string(r.OperationID))
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := m.retainRemovalIntent(ctx, r, m.snapshotPath(r.SnapshotID)); err != nil {
				return result, err
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := renameStorage(m.snapshotPath(r.SnapshotID), removal); err != nil {
				return result, err
			}
			if err := m.confirmRemoval(ctx, r, removal, false); err != nil {
				return result, ResultUncertain()
			}
			if err := removeSnapshotTree(ctx, removal); err != nil {
				return result, ResultUncertain()
			}
			metadata.Deleted = true
			result.Snapshot = &metadata
		}
		retained, err := m.snapshotBytes(ctx, r.Preparation.SessionID)
		if err != nil {
			// Delete or restore may already have committed native effects. A
			// missing inventory cannot become a successful zero-cost result.
			return result, ResultUncertain()
		}
		result.RetainedSnapshotBytes = retained
		result.CleanupVerified = true
	}
	result.CapacityBytes, result.FreeBytesAfter = storageCapacity(m.Root)
	m.Logger.Info("workspace_storage_completed", "session_id", result.SessionID, "operation_id", r.OperationID, "action", r.Action)
	return result, nil
}
func validateStorageRoot(root string, manifest Manifest) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"manifest.json": true}
	if manifest.Type == domain.GeneralChat {
		allowed["chat"] = true
	}
	for _, repo := range manifest.Repositories {
		if !repo.Owned || repo.Path != filepath.Join(root, string(repo.ID)) {
			return ResultUncertain()
		}
		allowed[string(repo.ID)] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return domain.Fail(domain.Conflict, "Dependent workspace resources require cleanup first.", "Stop and clean dependent resources before parent workspace cleanup.")
		}
	}
	return nil
}

// The caller holds the session lock and both private namespaces are owned by this
// operation. Refuse occupied names; platform no-replace protects concurrent writers.
func renameStorage(from, to string) error {
	if err := storageRenameNoReplace(from, to); err != nil {
		return err
	}
	if err := security.SyncParent(from); err != nil {
		return ResultUncertain()
	}
	if err := security.SyncParent(to); err != nil {
		return ResultUncertain()
	}
	return nil
}
