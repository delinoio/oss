package domain

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type WorkspaceDiffComparison string
type WorkspaceDiffBase string

const (
	DiffWorkingTree WorkspaceDiffComparison = "working-tree"
	DiffStaged      WorkspaceDiffComparison = "staged"
	DiffCreation    WorkspaceDiffComparison = "creation"
	DiffCommit      WorkspaceDiffBase       = "commit"
	DiffEmptyTree   WorkspaceDiffBase       = "empty-tree"
)

func (v WorkspaceDiffComparison) Valid() bool {
	return v == DiffWorkingTree || v == DiffStaged || v == DiffCreation
}

// Revision identifies these exact observed bytes, not an atomic filesystem
// snapshot, review approval, patch-application capability or session baseline.
type WorkspaceDiff struct {
	Comparison   WorkspaceDiffComparison `json:"comparison"`
	RepositoryID ID                      `json:"repository_id"`
	Path         string                  `json:"path"`
	Base         WorkspaceDiffBase       `json:"base"`
	BaseObject   string                  `json:"base_object"`
	HeadCommit   string                  `json:"head_commit,omitempty"`
	Patch        string                  `json:"patch"`
	Untracked    []string                `json:"untracked"`
	Revision     string                  `json:"revision"`
}

func (v WorkspaceDiff) Digest() string {
	v.Revision = ""
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func gitObjectID(v string) bool {
	raw, err := hex.DecodeString(v)
	return err == nil && (len(raw) == 20 || len(raw) == 32) && hex.EncodeToString(raw) == v
}

func (v WorkspaceDiff) Validate(q WorkspaceReadQuery) error {
	valid := q.Operation == WorkspaceGitDiff && q.Validate() == nil && v.Comparison == q.Comparison && v.RepositoryID == q.RepositoryID && v.Path == q.Path && gitObjectID(v.BaseObject) && Text(v.Patch, "Git diff", WorkspacePreviewLimit, false) == nil && v.Untracked != nil && len(v.Untracked) <= 100 && v.Revision == v.Digest()
	if v.Base == DiffEmptyTree {
		// Git object identity follows the repository format; this is not the
		// SHA-256 integrity digest used for the returned observation revision.
		legacy, modern := sha1.Sum([]byte("tree 0\x00")), sha256.Sum256([]byte("tree 0\x00"))
		valid = valid && v.HeadCommit == "" && v.Comparison != DiffCreation && (v.BaseObject == hex.EncodeToString(legacy[:]) || v.BaseObject == hex.EncodeToString(modern[:]))
	} else {
		valid = valid && v.Base == DiffCommit && gitObjectID(v.HeadCommit) && len(v.BaseObject) == len(v.HeadCommit) && (v.Comparison == DiffCreation || v.BaseObject == v.HeadCommit)
	}
	last, size := "", 0
	for _, path := range v.Untracked {
		size += len(path)
		valid = valid && WorkspacePath(path) && path != "." && path > last && size <= 16<<10
		last = path
	}
	if !valid {
		return Fail(Unavailable, "The Git diff observation is invalid.", "Refresh the selected repository comparison.")
	}
	return nil
}
