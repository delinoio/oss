// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// MaxSessionDeletionBytes admits all 4,096 original ownership copies while
// bounding both the synchronized private plan and one Worker page.
const MaxSessionDeletionBytes = 4 << 20

// Deletion work contains only immutable ownership references. Native paths,
// prompts, credentials and transcript content never cross this boundary.
type SessionDeletionCopy struct {
	SidechatRetry             bool    `json:"sidechat_retry,omitempty"`
	JobID                     ID      `json:"job_id"`
	UnpublishedChildProcessID ID      `json:"unpublished_child_process_id,omitempty"`
	UnpublishedSidechatID     ID      `json:"unpublished_sidechat_id,omitempty"`
	Type                      JobType `json:"type"`
	Revision                  uint64  `json:"revision,string"`
	Digest                    string  `json:"digest"`
	InstanceID                ID      `json:"instance_id"`
	ExecutionID               ID      `json:"execution_id,omitempty"`
	SnapshotID                ID      `json:"snapshot_id,omitempty"`
	ActionID                  ID      `json:"action_id,omitempty"`
}

type SessionDeletionFork struct {
	JobID            ID     `json:"job_id"`
	RuntimeID        ID     `json:"runtime_id"`
	CheckpointDigest string `json:"checkpoint_digest"`
	JobInputDigest   string `json:"job_input_digest"`
}

type SessionDeletionWork struct {
	RetryForks         []SessionDeletionFork `json:"retry_forks,omitempty"`
	SkillSnapshots     []SkillBinding        `json:"skill_snapshots,omitempty"`
	Fork               *SessionDeletionFork  `json:"fork,omitempty"`
	Version            uint32                `json:"version"`
	DeletionID         ID                    `json:"deletion_id"`
	ServerID           ID                    `json:"server_id"`
	SessionID          ID                    `json:"session_id"`
	MachineID          ID                    `json:"machine_id"`
	DeviceID           ID                    `json:"device_id"`
	Copies             []SessionDeletionCopy `json:"copies"`
	Images             []ImageAttachment     `json:"images,omitempty"`
	PreparationDigests []string              `json:"preparation_digests"`
}

func (w SessionDeletionWork) Validate() error {
	if len(w.SkillSnapshots) > 4096 {
		return SessionDeletionPending()
	}
	for _, binding := range w.SkillSnapshots {
		if ValidateSkills([]SkillBinding{binding}) != nil || binding.WorkerDeviceID != w.DeviceID {
			return SessionDeletionPending()
		}
	}
	if w.Version != 1 || (len(w.Copies) == 0 && w.Fork == nil && len(w.SkillSnapshots) == 0 && len(w.Images) == 0 && len(w.RetryForks) == 0) || len(w.Copies) > 4096 || len(w.Images) > MaxSessionImageAttachments || len(w.PreparationDigests) > 4096 {
		return SessionDeletionPending()
	}
	for _, id := range []ID{w.DeletionID, w.ServerID, w.SessionID, w.MachineID, w.DeviceID} {
		if id.Validate() != nil {
			return SessionDeletionPending()
		}
	}
	if len(w.RetryForks) > 4096 {
		return SessionDeletionPending()
	}
	retryJobs, retryRuntimes := map[ID]bool{}, map[ID]bool{}
	for _, f := range w.RetryForks {
		if retryJobs[f.JobID] || retryRuntimes[f.RuntimeID] || w.Fork != nil && (w.Fork.JobID == f.JobID || w.Fork.RuntimeID == f.RuntimeID) {
			return SessionDeletionPending()
		}
		retryJobs[f.JobID], retryRuntimes[f.RuntimeID] = true, true
		if f.JobID.Validate() != nil || f.RuntimeID.Validate() != nil || !deletionHash(f.CheckpointDigest) || !deletionHash(f.JobInputDigest) || len(w.PreparationDigests) == 0 {
			return SessionDeletionPending()
		}
	}
	if w.Fork != nil && (w.Fork.JobID.Validate() != nil || w.Fork.RuntimeID.Validate() != nil || !deletionHash(w.Fork.CheckpointDigest) || !deletionHash(w.Fork.JobInputDigest) || len(w.PreparationDigests) == 0) {
		return SessionDeletionPending()
	}
	seen := make(map[ID]bool, len(w.Copies))
	for _, c := range w.Copies {
		if c.Revision == 0 || c.InstanceID.Validate() != nil || !deletionHash(c.Digest) || (c.ExecutionID != "" && c.ExecutionID.Validate() != nil) {
			return SessionDeletionPending()
		}
		switch c.Type {
		case PrepareWorkspaceJob, RecoverWorkspaceJob, ExecuteSessionJob, RecoverExecutionJob, GenerateSessionTitleJob, WorkspaceStorageJob, ForkSessionJob:
			if c.ActionID != "" {
				return SessionDeletionPending()
			}
		case CompactSessionJob:
			if c.ActionID.Validate() != nil || c.ExecutionID != "" {
				return SessionDeletionPending()
			}
		default:
			return SessionDeletionPending()
		}
		if c.SidechatRetry && (c.Type != ForkSessionJob || c.ExecutionID == "" || c.UnpublishedSidechatID != "" || c.UnpublishedChildProcessID != "") {
			return SessionDeletionPending()
		}
		if c.UnpublishedChildProcessID != "" && (c.Type != ForkSessionJob || c.UnpublishedChildProcessID.Validate() != nil || c.UnpublishedChildProcessID == w.SessionID || c.UnpublishedChildProcessID == c.JobID || c.UnpublishedChildProcessID == c.ExecutionID || c.ExecutionID == "" || c.UnpublishedSidechatID != "") {
			return SessionDeletionPending()
		}
		if c.UnpublishedSidechatID != "" && (c.Type != ForkSessionJob || c.UnpublishedSidechatID.Validate() != nil || c.UnpublishedSidechatID == w.SessionID) {
			return SessionDeletionPending()
		}
		if c.SnapshotID != "" && (c.Type != WorkspaceStorageJob || c.SnapshotID.Validate() != nil) {
			return SessionDeletionPending()
		}
		// Deletion plans have their own 4,096-copy bound; the general linked-ID
		// helper intentionally caps unrelated product links at 1,000.
		if c.JobID.Validate() != nil || seen[c.JobID] {
			return SessionDeletionPending()
		}
		seen[c.JobID] = true
	}
	images := make(map[ID]bool, len(w.Images))
	for _, image := range w.Images {
		if image.Validate() != nil || image.MachineID != w.MachineID || images[image.ID] {
			return SessionDeletionPending()
		}
		images[image.ID] = true
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
