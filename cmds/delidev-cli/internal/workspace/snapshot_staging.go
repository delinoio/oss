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
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Persist outside scratch so copy inventories and restore payloads remain exact.
// Missing/legacy proof cannot be reconstructed from a currently occupied name.
type storageStagingClaim struct {
	PublishedSnapshotDigest string                  `json:"published_snapshot_digest,omitempty"`
	Version                 uint32                  `json:"version"`
	Reference               StorageRemovalReference `json:"reference"`
	MachineID               domain.ID               `json:"machine_id"`
	PreparationDigest       string                  `json:"preparation_digest"`
	RequestDigest           string                  `json:"request_digest"`
	RootIdentity            string                  `json:"root_identity"`
}

func stagingAction(action StorageAction) bool {
	return action == StorageCreate || action == StorageCleanup || action == StorageRestore
}
func storageRequestDigest(r StorageRequest) string {
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (m *Manager) stagingClaimPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-staging-claims", string(id)+".json")
}
func (m *Manager) stagingPath(id domain.ID) string {
	return filepath.Join(m.Root, "snapshot-staging", string(id))
}
func (m *Manager) createStorageStaging(r StorageRequest) (string, error) {
	if !stagingAction(r.Action) {
		return "", ResultUncertain()
	}
	if _, err := os.Lstat(m.stagingClaimPath(r.OperationID)); !errors.Is(err, os.ErrNotExist) {
		return "", ResultUncertain()
	}
	path := m.stagingPath(r.OperationID)
	if err := os.Mkdir(path, 0700); err != nil {
		return "", ResultUncertain()
	}
	identity, err := directoryPathIdentity(path)
	if err != nil {
		return "", ResultUncertain()
	}
	// A crash before proof persistence leaves scratch protected, never adoptable.
	if err := security.SyncParent(path); err != nil {
		return "", ResultUncertain()
	}
	claim := storageStagingClaim{Version: 1, Reference: removalReference(r), MachineID: r.Preparation.MachineID, PreparationDigest: r.Manifest.InputDigest, RequestDigest: storageRequestDigest(r), RootIdentity: identity}
	raw, err := json.Marshal(claim)
	if err != nil || security.WriteAtomicOwned(m.stagingClaimPath(r.OperationID), raw) != nil {
		return "", ResultUncertain()
	}
	return path, nil
}
func (m *Manager) readStagingClaim(id domain.ID) (storageStagingClaim, error) {
	raw, err := security.ReadPrivate(m.stagingClaimPath(id), 4096)
	var claim storageStagingClaim
	if err != nil || domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.Reference.OperationID != id || !stagingAction(claim.Reference.Action) || claim.Reference.SessionID.Validate() != nil || claim.Reference.SnapshotID.Validate() != nil || claim.MachineID.Validate() != nil || !digestValid(claim.PreparationDigest) || !digestValid(claim.RequestDigest) || claim.RootIdentity == "" || claim.PublishedSnapshotDigest != "" && !digestValid(claim.PublishedSnapshotDigest) {
		return claim, ResultUncertain()
	}
	return claim, nil
}
func (m *Manager) cleanupStorageStaging(ctx context.Context, r StorageRequest) error {
	if !stagingAction(r.Action) {
		return ResultUncertain()
	}
	claim, err := m.readStagingClaim(r.OperationID)
	if err != nil || claim.Reference != removalReference(r) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(claim.MachineID), claim.MachineID != r.Preparation.MachineID) ||
		claim.PreparationDigest != r.Manifest.InputDigest || claim.RequestDigest != storageRequestDigest(r) {
		return ResultUncertain()
	}
	// The opened remover checks native identity before touching any entry. A
	// renamed/replaced directory cannot borrow this original operation's proof.
	if err := removeSnapshotTree(ctx, m.stagingPath(r.OperationID), claim.RootIdentity); err != nil {
		return ResultUncertain()
	}
	return security.SyncParent(m.stagingPath(r.OperationID))
}

// Permanent deletion joins original job owners before this call. Retain proof
// until the Worker verifies scratch absence and removes its metadata copies.
func (m *Manager) cleanupDeletionStaging(ctx context.Context, w domain.SessionDeletionWork) error {
	for _, copy := range w.Copies {
		if copy.Type != domain.WorkspaceStorageJob {
			continue
		}
		path := m.stagingPath(copy.JobID)
		exists, err := storageExists(path)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		if !exists {
			continue
		}
		claim, err := m.readStagingClaim(copy.JobID)
		if err != nil ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(claim.Reference.SessionID), claim.Reference.SessionID != w.SessionID) ||
			claim.Reference.SnapshotID != copy.SnapshotID ||
			domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(claim.MachineID), claim.MachineID != w.MachineID) ||
			!slices.Contains(w.PreparationDigests, claim.PreparationDigest) {
			return domain.SessionDeletionPending()
		}
		if err := removeSnapshotTree(ctx, path, claim.RootIdentity); err != nil {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
