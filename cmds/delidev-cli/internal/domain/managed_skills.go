// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

const ManagedSkillManagementV1 WorkerCapability = "managed-skill-management-v1"
const ManagedNativeSkillsV1 WorkerCapability = "managed-native-skills-v1"

type ManagedSkillAction string

const (
	ManagedSkillList    ManagedSkillAction = "list"
	ManagedSkillImport  ManagedSkillAction = "import"
	ManagedSkillEnable  ManagedSkillAction = "enable"
	ManagedSkillDelete  ManagedSkillAction = "delete"
	ManagedSkillInspect ManagedSkillAction = "inspect-mutation"
	ManagedSkillView    ManagedSkillAction = "view"
)

type ManagedSkillSelection struct {
	WorkerDeviceID ID     `json:"worker_device_id"`
	PackageID      ID     `json:"package_id"`
	Name           string `json:"name,omitempty"`
	Unresolved     bool   `json:"unresolved,omitempty"`
}
type ManagedSkillSelectionList struct {
	Selections []ManagedSkillSelection `json:"selections"`
}
type ManagedSkillPackage struct {
	ID              ID                  `json:"id"`
	WorkerDeviceID  ID                  `json:"worker_device_id"`
	Revision        uint64              `json:"revision,string"`
	ContentRevision string              `json:"content_revision"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Provenance      string              `json:"provenance"`
	Enabled         bool                `json:"enabled"`
	Support         []MCPHarnessSupport `json:"support"`
	AgentReferences []MCPAgentReference `json:"agent_references,omitempty"`
}
type ManagedSkillResource struct {
	Path       string `json:"path"`
	Bytes      int    `json:"bytes"`
	Digest     string `json:"digest"`
	Executable bool   `json:"executable"`
}
type ManagedSkillMutation struct {
	Version          uint32             `json:"version"`
	Action           ManagedSkillAction `json:"action"`
	PackageID        ID                 `json:"package_id,omitempty"`
	ExpectedRevision uint64             `json:"expected_revision,string"`
	SourceDirectory  string             `json:"source_directory,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
	Confirmed        bool               `json:"confirmed,omitempty"`
}
type ManagedSkillRequest struct {
	ServerID         ID                   `json:"server_id"`
	ActorID          ID                   `json:"actor_id"`
	MachineID        ID                   `json:"machine_id"`
	WorkerDeviceID   ID                   `json:"worker_device_id"`
	WorkerInstanceID ID                   `json:"worker_instance_id"`
	RequestID        ID                   `json:"request_id"`
	Mutation         ManagedSkillMutation `json:"mutation"`
}
type ManagedSkillResult struct {
	WorkerDeviceID ID                     `json:"worker_device_id"`
	RequestID      ID                     `json:"request_id"`
	State          MCPMutationState       `json:"state"`
	Packages       []ManagedSkillPackage  `json:"packages"`
	Package        *ManagedSkillPackage   `json:"package,omitempty"`
	SkillMarkdown  string                 `json:"skill_markdown,omitempty"`
	Manifest       []ManagedSkillResource `json:"manifest,omitempty"`
	Problem        *Error                 `json:"problem,omitempty"`
}

func ManagedSkillUnavailable() error {
	return Fail(Unavailable, "Managed skills are unavailable on this Runner Device.", "Connect the owning Worker and inspect the original request.")
}
func ValidateManagedSkillSelections(list *ManagedSkillSelectionList) error {
	if list == nil {
		return nil
	}
	if len(list.Selections) > MaxSelectedSkills {
		return ManagedSkillUnavailable()
	}
	seen := map[ID]bool{}
	for _, v := range list.Selections {
		if v.PackageID.Validate() != nil || !v.Unresolved && v.WorkerDeviceID.Validate() != nil || Text(v.Name, "skill name", 128, false) != nil || seen[v.PackageID] {
			return ManagedSkillUnavailable()
		}
		seen[v.PackageID] = true
	}
	return nil
}
func (v ManagedSkillPackage) Validate() error {
	digest, err := hex.DecodeString(v.ContentRevision)
	if v.ID.Validate() != nil || v.WorkerDeviceID.Validate() != nil || v.Revision == 0 || v.Revision >= 1<<63 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != v.ContentRevision || Text(v.Name, "skill name", 128, true) != nil || strings.ContainsAny(v.Name, "/\\ \t\r\n$") || v.Name == "." || v.Name == ".." || Text(v.Description, "skill description", 4096, true) != nil || v.Provenance != "worker-import" || len(v.Support) > 4 || len(v.AgentReferences) > MaxInventoryManagedSkills {
		return ManagedSkillUnavailable()
	}
	seen := map[Harness]bool{}
	for _, s := range v.Support {
		if !s.Harness.Valid() || seen[s.Harness] || Text(s.Profile, "skill profile", 128, false) != nil || s.Verified && s.Profile == "" {
			return ManagedSkillUnavailable()
		}
		seen[s.Harness] = true
	}
	for _, a := range v.AgentReferences {
		if a.ID.Validate() != nil || Text(a.Name, "Agent name", 256, true) != nil {
			return ManagedSkillUnavailable()
		}
	}
	return nil
}

const MaxInventoryManagedSkills = 256

func (v ManagedSkillMutation) Validate() error {
	if v.Version != 1 || !slices.Contains([]ManagedSkillAction{ManagedSkillList, ManagedSkillImport, ManagedSkillEnable, ManagedSkillDelete, ManagedSkillInspect, ManagedSkillView}, v.Action) || v.ExpectedRevision >= 1<<63 {
		return ManagedSkillUnavailable()
	}
	if v.Action != ManagedSkillList && v.Action != ManagedSkillInspect && v.PackageID.Validate() != nil {
		return ManagedSkillUnavailable()
	}
	if v.Action == ManagedSkillImport && (Text(v.SourceDirectory, "selected package directory", 4096, true) != nil || v.ExpectedRevision != 0) || v.Action != ManagedSkillImport && v.SourceDirectory != "" || v.Action == ManagedSkillEnable && v.Enabled == nil || v.Action != ManagedSkillEnable && v.Enabled != nil || v.Action == ManagedSkillDelete && !v.Confirmed || v.Action != ManagedSkillDelete && v.Confirmed {
		return ManagedSkillUnavailable()
	}
	return nil
}
func (v ManagedSkillResult) Validate() error {
	if v.WorkerDeviceID.Validate() != nil || v.RequestID.Validate() != nil || !slices.Contains([]MCPMutationState{MCPPending, MCPSucceeded, MCPRecoveryRequired, MCPFailed}, v.State) || len(v.Packages) > MaxInventoryManagedSkills || len(v.SkillMarkdown) > 1<<20 || !utf8.ValidString(v.SkillMarkdown) || len(v.Manifest) > 1024 {
		return ManagedSkillUnavailable()
	}
	seen := map[ID]bool{}
	for _, p := range v.Packages {
		if p.Validate() != nil || p.WorkerDeviceID != v.WorkerDeviceID || seen[p.ID] {
			return ManagedSkillUnavailable()
		}
		seen[p.ID] = true
	}
	if v.Package != nil && (v.Package.Validate() != nil || v.Package.WorkerDeviceID != v.WorkerDeviceID) {
		return ManagedSkillUnavailable()
	}
	paths := map[string]bool{}
	for _, resource := range v.Manifest {
		digest, err := hex.DecodeString(resource.Digest)
		if Text(resource.Path, "package resource", 4096, true) != nil || path.IsAbs(resource.Path) || path.Clean(resource.Path) != resource.Path || resource.Path == "." || resource.Path == ".." || strings.HasPrefix(resource.Path, "../") || strings.Contains(resource.Path, "\\") || paths[resource.Path] || resource.Bytes < 0 || resource.Bytes > 16<<20 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != resource.Digest {
			return ManagedSkillUnavailable()
		}
		paths[resource.Path] = true
	}
	return nil
}
