// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode"
)

const MaxRepositoryBranches = 10000
const MaxRepositoryBranchesBytes = 8 << 20

// The bounded complete inventory needs independent envelope headroom. Other
// job/document limits remain unchanged.
const MaxRepositoryBranchesJobBytes = 9 << 20

type RepositoryBranchesInput struct {
	ProjectID          ID     `json:"project_id"`
	ProjectRevision    uint64 `json:"project_revision"`
	RepositoryID       ID     `json:"repository_id"`
	RepositoryRevision uint64 `json:"repository_revision"`
	MachineID          ID     `json:"machine_id"`
	MachineRevision    uint64 `json:"machine_revision"`
	Source             string `json:"source"`
	SourceIdentity     string `json:"source_identity"`
	Remote             string `json:"remote"`
}

func (v RepositoryBranchesInput) Validate() error {
	identity, err := RepositoryCloneSourceIdentity(v.Source)
	if err != nil || identity != v.SourceIdentity || v.ProjectID.Validate() != nil || v.RepositoryID.Validate() != nil || v.MachineID.Validate() != nil || v.ProjectRevision == 0 || v.RepositoryRevision == 0 || v.MachineRevision == 0 || (Reference{Type: RemoteBranch, Remote: v.Remote, Name: "branch"}).Validate(false) != nil {
		return Fail(InvalidArgument, "Repository branch authority is invalid.", "Read current project, repository and Worker configuration.")
	}
	return nil
}

type RepositoryBranchesResult struct {
	ProjectID          ID        `json:"project_id"`
	ProjectRevision    uint64    `json:"project_revision"`
	RepositoryID       ID        `json:"repository_id"`
	RepositoryRevision uint64    `json:"repository_revision"`
	MachineID          ID        `json:"machine_id"`
	MachineRevision    uint64    `json:"machine_revision"`
	Remote             string    `json:"remote"`
	Branches           []string  `json:"branches"`
	ObservedAt         time.Time `json:"observed_at"`
}

func (v RepositoryBranchesResult) Validate(input RepositoryBranchesInput) error {
	if v.ProjectID != input.ProjectID || v.ProjectRevision != input.ProjectRevision || v.RepositoryID != input.RepositoryID || v.RepositoryRevision != input.RepositoryRevision || v.MachineID != input.MachineID || v.MachineRevision != input.MachineRevision || v.Remote != input.Remote || v.ObservedAt.IsZero() || v.Branches == nil || len(v.Branches) > MaxRepositoryBranches || !slices.IsSorted(v.Branches) {
		return Fail(InvalidArgument, "Repository branch inventory is invalid.", "Refresh the original branch selector.")
	}
	for i, name := range v.Branches {
		if (Reference{Type: RemoteBranch, Remote: v.Remote, Name: name}).Validate(false) != nil || !ValidRepositoryBranch(name) || i > 0 && name == v.Branches[i-1] {
			return Fail(InvalidArgument, "Repository branch inventory contains invalid references.", "Refresh the original branch selector.")
		}
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > MaxRepositoryBranchesBytes {
		return Fail(ResourceExhausted, "Repository branch inventory exceeds its bound.", "Use the saved or manual starting reference.")
	}
	return nil
}
func DecodeRepositoryBranchesJob(raw []byte, target *Job) error {
	if err := DecodeWithLimit(raw, target, MaxRepositoryBranchesJobBytes); err != nil {
		return err
	}
	var input RepositoryBranchesInput
	if target.Type != DiscoverRepositoryBranchesJob || target.Validate() != nil || Decode(target.Input, &input) != nil || input.Validate() != nil {
		return Fail(InvalidArgument, "Repository branch job is invalid.", "Preserve the original job.")
	}
	return nil
}

// Validate Git refs/heads syntax without invoking Git once per branch.
func ValidRepositoryBranch(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.ContainsAny(name, " ~^:?*[\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return Text(name, "branch", 1024, true) == nil
}
