// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/hex"

const ManagedSkillsV1 WorkerCapability = "managed-skills-v1"

type ManagedSkillSelection struct {
	WorkerDeviceID  ID     `json:"worker_device_id"`
	PackageID       ID     `json:"package_id"`
	ContentRevision string `json:"content_revision"`
}
type ManagedSkillSelectionList struct {
	Selections []ManagedSkillSelection `json:"selections"`
}

func (v ManagedSkillSelectionList) Validate() error {
	if len(v.Selections) > MaxSelectedSkills {
		return Fail(ResourceExhausted, "Too many managed skills.", "Select at most sixteen packages.")
	}
	seen := map[ID]bool{}
	for _, s := range v.Selections {
		b, e := hex.DecodeString(s.ContentRevision)
		if s.PackageID.Validate() != nil || s.WorkerDeviceID.Validate() != nil || e != nil || len(b) != 32 || hex.EncodeToString(b) != s.ContentRevision || seen[s.PackageID] {
			return Fail(InvalidArgument, "Invalid managed skill selection.", "Select the current package revision on its original Runner.")
		}
		seen[s.PackageID] = true
	}
	return nil
}

type ManagedSkillResource struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Digest string `json:"digest"`
}
type ManagedSkillPackage struct {
	PackageID       ID                     `json:"package_id"`
	WorkerDeviceID  ID                     `json:"worker_device_id"`
	MachineID       ID                     `json:"machine_id"`
	Revision        uint64                 `json:"revision,string"`
	ContentRevision string                 `json:"content_revision"`
	LegacyRevision  string                 `json:"legacy_revision"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	Enabled         bool                   `json:"enabled"`
	Deleted         bool                   `json:"deleted,omitempty"`
	Deleting        bool                   `json:"deleting,omitempty"`
	Resources       []ManagedSkillResource `json:"resources,omitempty"`
}
type ManagedSkillAction string

const (
	ManagedSkillList         ManagedSkillAction = "list"
	ManagedSkillView         ManagedSkillAction = "view"
	ManagedSkillImport       ManagedSkillAction = "import"
	ManagedSkillEnable       ManagedSkillAction = "enable"
	ManagedSkillDelete       ManagedSkillAction = "delete"
	ManagedSkillCancelImport ManagedSkillAction = "cancel-import"
)

type ManagedSkillRequest struct {
	Action           ManagedSkillAction `json:"action"`
	ServerID         ID                 `json:"server_id"`
	ActorID          ID                 `json:"actor_id"`
	MachineID        ID                 `json:"machine_id"`
	WorkerDeviceID   ID                 `json:"worker_device_id"`
	WorkerInstanceID ID                 `json:"worker_instance_id"`
	RequestID        ID                 `json:"request_id"`
	PackageID        ID                 `json:"package_id,omitempty"`
	ExpectedRevision uint64             `json:"expected_revision,string"`
	Enabled          bool               `json:"enabled"`
	SourceDirectory  string             `json:"source_directory,omitempty"`
}
type ManagedSkillResult struct {
	Packages      []ManagedSkillPackage `json:"packages"`
	SkillMarkdown string                `json:"skill_markdown,omitempty"`
}
