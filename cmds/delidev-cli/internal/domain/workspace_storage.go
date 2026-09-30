// SPDX-License-Identifier: Apache-2.0
package domain

// WorkspaceStorage is independent of Archive and native execution outcome.
// Reserving an operation closes dispatch until its original result is settled.
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
	return s.Storage == nil || s.Storage.State == WorkspacePresent
}
