// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Only the original successful no-replace rename can promote its staging claim.
// Matching manifests and copied bytes cannot reconstruct missing publication.
func (m *Manager) retainSnapshotPublication(r StorageRequest, metadata SnapshotMetadata) error {
	claim, err := m.readStagingClaim(r.OperationID)
	if err != nil || claim.Reference != removalReference(r) || claim.MachineID != r.Preparation.MachineID || claim.PreparationDigest != r.Manifest.InputDigest || claim.RequestDigest != storageRequestDigest(r) || claim.PublishedSnapshotDigest != "" {
		return ResultUncertain()
	}
	identity, err := directoryPathIdentity(m.snapshotPath(r.SnapshotID))
	if err != nil || identity != claim.RootIdentity {
		return ResultUncertain()
	}
	claim.PublishedSnapshotDigest = metadata.SHA256
	raw, err := json.Marshal(claim)
	if err != nil || security.WriteAtomic(m.stagingClaimPath(r.OperationID), raw) != nil {
		return ResultUncertain()
	}
	return nil
}

func (m *Manager) verifySnapshotPublication(snapshot snapshotManifest, digest string) (storageStagingClaim, error) {
	claim, err := m.readStagingClaim(snapshot.OperationID)
	if err != nil || (claim.Reference.Action != StorageCreate && claim.Reference.Action != StorageCleanup) || claim.Reference.SnapshotID != snapshot.ID || claim.Reference.SessionID != snapshot.Workspace.SessionID || claim.MachineID != snapshot.Workspace.MachineID || claim.PreparationDigest != snapshot.Workspace.InputDigest || claim.PublishedSnapshotDigest != digest || !digestValid(digest) {
		return claim, ResultUncertain()
	}
	identity, err := directoryPathIdentity(m.snapshotPath(snapshot.ID))
	if err != nil || identity != claim.RootIdentity {
		return claim, ResultUncertain()
	}
	return claim, nil
}
