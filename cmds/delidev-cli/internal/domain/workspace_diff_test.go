package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkspaceDiffRejectsMixedAndChangedScope(t *testing.T) {
	query := WorkspaceReadQuery{Operation: WorkspaceGitDiff, RepositoryID: NewID(), Path: ".", Comparison: DiffWorkingTree}
	for _, mutation := range []string{"valid", "repository", "comparison", "path", "head", "digest", "mixed", "order", "unknown", "traversal", "empty-tree"} {
		t.Run(mutation, func(t *testing.T) {
			v := WorkspaceDiff{Comparison: query.Comparison, RepositoryID: query.RepositoryID, Path: query.Path, Base: DiffCommit, BaseObject: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40), Patch: "original\n", Untracked: []string{}}
			v.Revision = v.Digest()
			r := WorkspaceReadResult{Diff: &v}
			switch mutation {
			case "repository":
				v.RepositoryID = NewID()
			case "comparison":
				v.Comparison = DiffCreation
			case "path":
				v.Path = "other"
			case "head":
				v.HeadCommit = strings.Repeat("b", 40)
			case "digest":
				v.Patch = "changed"
			case "mixed":
				r.Text = "file data"
			case "order":
				v.Untracked = []string{"z", "a"}
			case "unknown":
				v.Base = "unknown"
			case "traversal":
				v.Untracked = []string{"../file"}
			case "empty-tree":
				v.Base, v.HeadCommit = DiffEmptyTree, ""
			}
			if mutation != "digest" {
				v.Revision = v.Digest()
			}
			if (r.Validate(query) == nil) != (mutation == "valid") {
				t.Fatal("diff accepted invalid scope/bytes")
			}
		})
	}
	for _, bad := range []WorkspaceReadQuery{{Operation: WorkspaceFile, Path: "file", Comparison: DiffWorkingTree}, {Operation: WorkspaceGitDiff, Path: ".", Comparison: DiffWorkingTree}, {Operation: WorkspaceGitDiff, RepositoryID: query.RepositoryID, Path: "../file", Comparison: DiffWorkingTree}} {
		if bad.Validate() == nil {
			t.Fatal("invalid query accepted")
		}
	}
}

func TestWorkspaceBranchIdentityAndLegacySerialization(t *testing.T) {
	q := WorkspaceReadQuery{Operation: WorkspaceGitDiff, RepositoryID: NewID(), Comparison: DiffWorkingTree, Path: "."}
	v := WorkspaceDiff{Comparison: q.Comparison, RepositoryID: q.RepositoryID, Path: q.Path, Base: DiffCommit, BaseObject: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40), Untracked: []string{}}
	// Reproduce the original field order and omission rules, not a new fixture
	// digest that could silently accept a compatibility regression.
	legacy := struct {
		Comparison   WorkspaceDiffComparison `json:"comparison"`
		RepositoryID ID                      `json:"repository_id"`
		Path         string                  `json:"path"`
		Base         WorkspaceDiffBase       `json:"base"`
		BaseObject   string                  `json:"base_object"`
		HeadCommit   string                  `json:"head_commit,omitempty"`
		Patch        string                  `json:"patch"`
		Untracked    []string                `json:"untracked"`
		Revision     string                  `json:"revision"`
	}{v.Comparison, v.RepositoryID, v.Path, v.Base, v.BaseObject, v.HeadCommit, v.Patch, v.Untracked, v.Revision}
	original, _ := json.Marshal(legacy)
	current, _ := json.Marshal(v)
	if string(original) != string(current) {
		t.Fatal("legacy diff wire bytes changed", string(current))
	}
	q.Comparison = DiffBranch
	q.BaseRef = Reference{Type: LocalBranch, Name: "base"}
	v.Comparison = DiffBranch
	v.BaseRef = q.BaseRef
	v.BaseCommit = strings.Repeat("b", 40)
	v.MergeBase = v.BaseObject
	v.Revision = v.Digest()
	if err := v.Validate(q); err != nil {
		t.Fatal(err)
	}
	changed := v
	changed.BaseRef.Name = "other"
	changed.Revision = changed.Digest()
	if changed.Revision == v.Revision || changed.Validate(q) == nil {
		t.Fatal("selected base was not bound")
	}
	q.Comparison = DiffWorkingTree
	if q.Validate() == nil {
		t.Fatal("legacy query accepted speculative base")
	}
}
