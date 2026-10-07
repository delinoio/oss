// SPDX-License-Identifier: Apache-2.0
package domain

// WorkspaceStorage is independent of Archive and native execution outcome.
// Unconfirmed storage observations retain metadata without closing admission.
type WorkspaceStorageState string

const (
	WorkspacePresent          WorkspaceStorageState = "present"
	WorkspaceStored           WorkspaceStorageState = "stored"
	WorkspaceStoragePending   WorkspaceStorageState = "pending"
	WorkspaceStorageUncertain WorkspaceStorageState = "uncertain"
)

type WorkspaceStorage struct {
	State      WorkspaceStorageState `json:"state"`
	JobID      ID                    `json:"job_id"`
	SnapshotID ID                    `json:"snapshot_id,omitempty"`
}

func (s Session) WorkspaceAvailable() bool {
	if s.Storage == nil || s.Storage.State == WorkspacePresent {
		return true
	}
	if s.Storage.State == WorkspaceStoragePending || s.Storage.State == WorkspaceStorageUncertain {
		ObserveOwnership(OwnershipCleanup, s.Storage.JobID)
		return true
	}
	return false
}
