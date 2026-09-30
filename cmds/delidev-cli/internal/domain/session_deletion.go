// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Deletion work contains only immutable ownership references. Native paths,
// prompts, credentials and transcript content never cross this boundary.
type SessionDeletionCopy struct {
	JobID       ID      `json:"job_id"`
	Type        JobType `json:"type"`
	Revision    uint64  `json:"revision,string"`
	Digest      string  `json:"digest"`
	InstanceID  ID      `json:"instance_id"`
	ExecutionID ID      `json:"execution_id,omitempty"`
}

type SessionDeletionWork struct {
	Version            uint32                `json:"version"`
	DeletionID         ID                    `json:"deletion_id"`
	ServerID           ID                    `json:"server_id"`
	SessionID          ID                    `json:"session_id"`
	MachineID          ID                    `json:"machine_id"`
	DeviceID           ID                    `json:"device_id"`
	Copies             []SessionDeletionCopy `json:"copies"`
	PreparationDigests []string              `json:"preparation_digests"`
}

func (w SessionDeletionWork) Validate() error {
	if w.Version != 1 || len(w.Copies) == 0 || len(w.Copies) > 4096 || len(w.PreparationDigests) > 4096 {
		return SessionDeletionPending()
	}
	for _, id := range []ID{w.DeletionID, w.ServerID, w.SessionID, w.MachineID, w.DeviceID} {
		if id.Validate() != nil {
			return SessionDeletionPending()
		}
	}
	ids := make([]ID, 0, len(w.Copies))
	for _, c := range w.Copies {
		if c.Revision == 0 || c.InstanceID.Validate() != nil || !deletionHash(c.Digest) || (c.ExecutionID != "" && c.ExecutionID.Validate() != nil) {
			return SessionDeletionPending()
		}
		switch c.Type {
		case PrepareWorkspaceJob, RecoverWorkspaceJob, ExecuteSessionJob, RecoverExecutionJob, GenerateSessionTitleJob:
		default:
			return SessionDeletionPending()
		}
		ids = append(ids, c.JobID)
	}
	if UniqueIDs(ids) != nil {
		return SessionDeletionPending()
	}
	for _, d := range w.PreparationDigests {
		if !deletionHash(d) {
			return SessionDeletionPending()
		}
	}
	return nil
}

func (w SessionDeletionWork) Digest() string {
	b, _ := json.Marshal(w)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func deletionHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func SessionDeletionPending() *Error {
	return Fail(RecoveryRequired, "Permanent session deletion is pending confirmed cleanup.", "Keep the original deletion request and reconnect its owning Worker; uncertain resources are preserved.")
}
