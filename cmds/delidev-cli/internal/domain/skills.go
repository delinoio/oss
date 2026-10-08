// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const NativeSkillsV1 WorkerCapability = "native-skills-v1"
const MaxSelectedSkills = 16
const MaxRetainedSkillSnapshots = 4096

// Skill bindings contain opaque ownership and digest evidence, never paths.
type SkillBinding struct {
	WorkerDeviceID  ID     `json:"worker_device_id"`
	InventoryID     ID     `json:"inventory_id"`
	SkillID         ID     `json:"skill_id"`
	ContentRevision string `json:"content_revision"`
	SnapshotID      ID     `json:"snapshot_id"`
}

func ValidateSkills(values []SkillBinding) error {
	if len(values) > MaxSelectedSkills {
		return Fail(ResourceExhausted, "Too many selected skills.", "Select at most sixteen skills.")
	}
	seen := map[ID]bool{}
	for _, v := range values {
		h, e := hex.DecodeString(v.ContentRevision)
		if v.WorkerDeviceID.Validate() != nil || v.InventoryID.Validate() != nil || v.SkillID.Validate() != nil || v.SnapshotID.Validate() != nil || e != nil || len(h) != 32 || hex.EncodeToString(h) != v.ContentRevision || seen[v.SkillID] {
			return Fail(InvalidArgument, "Invalid skill selection.", "Refresh the skill inventory and select the exact package.")
		}
		seen[v.SkillID] = true
	}
	return nil
}

type SkillEntry struct {
	WorkerDeviceID  ID     `json:"worker_device_id"`
	InventoryID     ID     `json:"inventory_id"`
	SkillID         ID     `json:"skill_id"`
	ContentRevision string `json:"content_revision"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Provenance      string `json:"provenance"`
}
type SkillReadAction string

const CleanupSkillPreparation SkillReadAction = "cleanup-preparation"

type SkillPreparationProof struct {
	ServerID           ID     `json:"server_id"`
	RequestID          ID     `json:"request_id"`
	OriginalInstanceID ID     `json:"original_instance_id"`
	ScopeDigest        string `json:"scope_digest"`
}

type SkillReadRequest struct {
	Action           SkillReadAction        `json:"action,omitempty"`
	Preparation      *SkillPreparationProof `json:"preparation,omitempty"`
	ProjectID        ID                     `json:"project_id,omitempty"`
	WorkerInstanceID ID                     `json:"worker_instance_id"`
	WorkerDeviceID   ID                     `json:"worker_device_id"`
	AgentRevision    uint64                 `json:"agent_revision,string"`
	MachineID        ID                     `json:"machine_id"`
	AgentID          ID                     `json:"agent_id"`
	ActorID          ID                     `json:"actor_id"`
	SessionID        ID                     `json:"session_id,omitempty"`
	Selections       []SkillBinding         `json:"selections,omitempty"`
}
type SkillReadResult struct {
	Scope   *SkillReadRequest `json:"scope,omitempty"`
	Entries []SkillEntry      `json:"entries"`
}

// The current delivery instance can change only for cleanup/reinspection. The
// preparation digest retains its original actor, revision, selection and instance.
func SkillPreparationDigest(scope SkillReadRequest) string {
	if scope.Preparation != nil {
		scope.WorkerInstanceID = scope.Preparation.OriginalInstanceID
	}
	scope.Preparation = nil
	scope.Action = ""
	raw, _ := json.Marshal(scope)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func ValidateSkillPreparation(scope SkillReadRequest) error {
	proof := scope.Preparation
	if proof == nil || (scope.Action != "" && scope.Action != CleanupSkillPreparation) || proof.ServerID.Validate() != nil || proof.RequestID.Validate() != nil || proof.OriginalInstanceID.Validate() != nil || scope.WorkerInstanceID.Validate() != nil || scope.WorkerDeviceID.Validate() != nil || scope.MachineID.Validate() != nil || scope.AgentID.Validate() != nil || scope.ActorID.Validate() != nil || scope.AgentRevision == 0 || scope.ProjectID != "" && scope.ProjectID.Validate() != nil || scope.SessionID != "" && scope.SessionID.Validate() != nil || len(scope.Selections) == 0 || ValidateSkills(scope.Selections) != nil || proof.ScopeDigest != SkillPreparationDigest(scope) {
		return Fail(RecoveryRequired, "The skill preparation ownership is unavailable.", "Retain the original request and Worker; retry only its original operation.")
	}
	for _, binding := range scope.Selections {
		if binding.SnapshotID != proof.RequestID || binding.WorkerDeviceID != scope.WorkerDeviceID {
			return Fail(RecoveryRequired, "The skill preparation ownership is unavailable.", "Retain the original request and Worker; retry only its original operation.")
		}
	}
	return nil
}
