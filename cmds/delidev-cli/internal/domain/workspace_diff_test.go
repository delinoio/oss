package domain

import (
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
