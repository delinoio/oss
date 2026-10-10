package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBranchDiffBindsOriginalBaseAndPreservesLegacySerialization(t *testing.T) {
	legacy := WorkspaceReadQuery{Operation: WorkspaceGitDiff, RepositoryID: NewID(), Comparison: DiffWorkingTree, Path: "."}
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "base_ref") {
		t.Fatal("legacy query changed")
	}
	value := reviewDiffFixture(ordinaryReviewPatch)
	original := value.Digest()
	wire, _ := json.Marshal(value)
	for _, field := range []string{"base_ref", "base_commit", "merge_base"} {
		if strings.Contains(string(wire), field) {
			t.Fatal("legacy diff changed")
		}
	}
	snapshot := value
	snapshot.Revision = ""
	legacyWire := fmt.Sprintf(`{"comparison":%q,"repository_id":%q,"path":%q,"base":%q,"base_object":%q,"head_commit":%q,"patch":%s,"untracked":[],"revision":""}`, snapshot.Comparison, snapshot.RepositoryID, snapshot.Path, snapshot.Base, snapshot.BaseObject, snapshot.HeadCommit, func() string { raw, _ := json.Marshal(snapshot.Patch); return string(raw) }())
	sum := sha256.Sum256([]byte(legacyWire))
	if original != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy digest changed")
	}
	ref := Reference{Type: LocalBranch, Name: "base-A"}
	value.Comparison, value.BaseRef, value.BaseCommit, value.MergeBase = DiffBranch, &ref, strings.Repeat("c", 40), value.BaseObject
	value.Revision = value.Digest()
	query := WorkspaceReadQuery{Operation: WorkspaceGitDiff, RepositoryID: value.RepositoryID, Comparison: DiffBranch, Path: value.Path, BaseRef: &ref}
	if value.Validate(query) != nil {
		t.Fatal("valid branch rejected")
	}
	anchor, err := value.ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewFileAnchor})
	if err != nil || anchor.Validate() != nil || !anchor.Matches(value) {
		t.Fatal("original review base lost", err)
	}
	same := ref
	query.BaseRef = &same
	if value.Validate(query) != nil {
		t.Fatal("pointer identity became authority")
	}
	other := Reference{Type: LocalBranch, Name: "base-B"}
	query.BaseRef = &other
	if value.Validate(query) == nil {
		t.Fatal("foreign base accepted")
	}
	moved := value
	moved.BaseRef = &other
	moved.Revision = moved.Digest()
	if anchor.Matches(moved) {
		t.Fatal("review reanchored")
	}
	query.BaseRef = nil
	if query.Validate() == nil {
		t.Fatal("branch without explicit base accepted")
	}
	legacy.BaseRef = &ref
	if legacy.Validate() == nil {
		t.Fatal("base fields allowed on legacy comparison")
	}
}
func TestDiffOptionsClosedInventoryBoundsAndMissingSavedBase(t *testing.T) {
	ref := Reference{Type: LocalBranch, Name: "missing-saved"}
	query := WorkspaceReadQuery{Operation: WorkspaceGitDiffOptions, RepositoryID: NewID(), Path: "."}
	value := WorkspaceDiffOptions{Version: 1, RepositoryID: query.RepositoryID, Path: query.Path, Choices: []DiffBaseChoice{{Reference: ref, Configured: true}}, Default: &ref}
	if value.Validate(query) != nil {
		t.Fatal("missing configured base disappeared")
	}
	value.DefaultAvailable = true
	if value.Validate(query) != nil {
		t.Fatal("available default rejected")
	}
	value.Choices = append(value.Choices, value.Choices[0])
	if value.Validate(query) == nil {
		t.Fatal("duplicate inventory accepted")
	}
	value.Choices = []DiffBaseChoice{}
	value.Default = nil
	if value.Validate(query) == nil {
		t.Fatal("available missing default accepted")
	}
	query.Comparison = DiffBranch
	query.BaseRef = &ref
	if query.Validate() == nil {
		t.Fatal("options sent speculative branch fields")
	}
}

func TestDiffOptionsRejectsOversizedCompleteInventories(t *testing.T) {
	q := WorkspaceReadQuery{Operation: WorkspaceGitDiffOptions, RepositoryID: NewID(), Path: "."}
	value := WorkspaceDiffOptions{Version: 1, RepositoryID: q.RepositoryID, Path: q.Path, Choices: []DiffBaseChoice{}}
	for i := 0; i < 1000; i++ {
		value.Choices = append(value.Choices, DiffBaseChoice{Reference: Reference{Type: LocalBranch, Name: fmt.Sprintf("ref-%04d", i)}})
	}
	if value.Validate(q) != nil {
		t.Fatal("bounded full inventory rejected")
	}
	value.Choices = append(value.Choices, DiffBaseChoice{Reference: Reference{Type: LocalBranch, Name: "overflow"}})
	if value.Validate(q) == nil {
		t.Fatal("oversized full inventory accepted")
	}
	value.Choices = value.Choices[:1000]
	for i := range value.Choices {
		value.Choices[i].Reference.Name += strings.Repeat("x", 200)
	}
	if value.Validate(q) == nil {
		t.Fatal("oversized complete inventory bytes accepted")
	}
}
