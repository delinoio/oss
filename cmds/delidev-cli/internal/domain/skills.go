// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
)

const NativeSkillsV1 WorkerCapability = "native-skills-v1"
const MaxSelectedSkills = 16

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
type SkillReadRequest struct {
	ProjectID        ID             `json:"project_id,omitempty"`
	WorkerInstanceID ID             `json:"worker_instance_id"`
	WorkerDeviceID   ID             `json:"worker_device_id"`
	AgentRevision    uint64         `json:"agent_revision,string"`
	MachineID        ID             `json:"machine_id"`
	AgentID          ID             `json:"agent_id"`
	ActorID          ID             `json:"actor_id"`
	SessionID        ID             `json:"session_id,omitempty"`
	Selections       []SkillBinding `json:"selections,omitempty"`
}
type SkillReadResult struct {
	Scope   *SkillReadRequest `json:"scope,omitempty"`
	Entries []SkillEntry      `json:"entries"`
}
