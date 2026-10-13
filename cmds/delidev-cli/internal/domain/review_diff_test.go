package domain

import (
	"strings"
	"testing"
)

func reviewDiffFixture(patch string) WorkspaceDiff {
	v := WorkspaceDiff{Comparison: DiffWorkingTree, RepositoryID: NewID(), Path: ".", Base: DiffCommit, BaseObject: strings.Repeat("a", 40), HeadCommit: strings.Repeat("a", 40), Patch: patch, Untracked: []string{}}
	v.Revision = v.Digest()
	return v
}

const ordinaryReviewPatch = "diff --git a/file.txt b/file.txt\nindex abcdef0..abcdef1 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1,3 +1,3 @@ section\n context\n-old\n+new\n tail\n"

func TestReviewAnchorsRetainExactSidesContextAndRevision(t *testing.T) {
	v := reviewDiffFixture(ordinaryReviewPatch)
	for _, side := range []ReviewSide{ReviewOldSide, ReviewNewSide} {
		a, err := v.ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewLineAnchor, Side: side, Start: 1, End: 3})
		middle := "old"
		if side == ReviewNewSide {
			middle = "new"
		}
		if err != nil || a.Context != "context\n"+middle+"\ntail\n" || !a.Matches(v) {
			t.Fatal(side, a, err)
		}
		changed := v
		changed.Untracked = []string{"untracked.txt"}
		changed.Revision = changed.Digest()
		if a.Matches(changed) {
			t.Fatal("changed comparison silently reanchored")
		}
		a.Context += "altered"
		if a.Matches(v) {
			t.Fatal("changed context accepted")
		}
	}
	for _, s := range []ReviewSelection{
		{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 4, End: 4},
		{Path: "missing", Kind: ReviewFileAnchor},
		{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 1, End: 21},
		{Path: "file.txt", Kind: ReviewFileAnchor, Start: 1},
		{Path: "../file.txt", Kind: ReviewFileAnchor},
	} {
		if _, err := v.ReviewAnchor(s); err == nil {
			t.Fatal("invalid selection", s)
		}
	}
}

func TestReviewRejectsIncompleteAmbiguousOrCombinedPatches(t *testing.T) {
	for name, patch := range map[string]string{
		"combined":         strings.Replace(ordinaryReviewPatch, "diff --git a/file.txt b/file.txt", "diff --cc file.txt", 1),
		"foreign header":   strings.Replace(ordinaryReviewPatch, "+++ b/file.txt", "+++ b/other.txt", 1),
		"rename":           strings.Replace(ordinaryReviewPatch, "b/file.txt", "b/other.txt", 1),
		"short count":      strings.Replace(ordinaryReviewPatch, "-1,3", "-1,2", 1),
		"long count":       strings.Replace(ordinaryReviewPatch, "-1,3", "-1,4", 1),
		"overflow":         strings.Replace(ordinaryReviewPatch, "-1,3", "-4294967295,3", 1),
		"zero start":       strings.Replace(ordinaryReviewPatch, "+1,3", "+0,3", 1),
		"unknown metadata": strings.Replace(ordinaryReviewPatch, "index abcdef0", "unknown abcdef0", 1),
		"duplicate file":   ordinaryReviewPatch + ordinaryReviewPatch,
		"missing newline":  strings.TrimSuffix(ordinaryReviewPatch, "\n"),
		"midfile EOF":      strings.Replace(ordinaryReviewPatch, " context\n", " context\n\\ No newline at end of file\n", 1),
		"extra line":       ordinaryReviewPatch + "+extra\n",
		"overlap":          ordinaryReviewPatch + "@@ -3 +3 @@\n-again\n+again\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := reviewDiffFixture(patch).ReviewFiles(); err == nil {
				t.Fatal("ambiguous patch accepted")
			}
		})
	}
}

func TestReviewNoInventedLinesForNonLineChanges(t *testing.T) {
	for _, patch := range []string{
		"diff --git a/file.txt b/file.txt\nindex abcdef0..abcdef1 100644\nBinary files a/file.txt and b/file.txt differ\n",
		"diff --git a/file.txt b/file.txt\nold mode 100644\nnew mode 100755\n",
		strings.Replace(ordinaryReviewPatch, "100644", "120000", 1),
		strings.Replace(ordinaryReviewPatch, "100644", "160000", 1),
		"diff --git a/file.txt b/file.txt\nnew file mode 100644\nindex 0000000..e69de29\n",
	} {
		v := reviewDiffFixture(patch)
		files, err := v.ReviewFiles()
		if err != nil || len(files) != 1 || files[0].Kind != ReviewNonLineFile || len(files[0].Lines) != 0 {
			t.Fatal(files, err)
		}
		if _, err := v.ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewFileAnchor}); err != nil {
			t.Fatal(err)
		}
		if _, err := v.ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 1, End: 1}); err == nil {
			t.Fatal("invented non-line location")
		}
	}
}

func TestReviewExactEOFAndBoundedContext(t *testing.T) {
	patch := "diff --git a/file.txt b/file.txt\nnew file mode 100644\nindex 0000000..abcdef1\n--- /dev/null\n+++ b/file.txt\n@@ -0,0 +1 @@\n+without newline\n\\ No newline at end of file\n"
	v := reviewDiffFixture(patch)
	s := ReviewSelection{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 1, End: 1}
	a, err := v.ReviewAnchor(s)
	if err != nil || a.Context != "without newline" {
		t.Fatal(a, err)
	}
	v = reviewDiffFixture(strings.Replace(patch, "without newline", strings.Repeat("a", 8193), 1))
	if _, err := v.ReviewAnchor(s); err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatal("context bound", err)
	}
}

func branchReviewDiffFixture(patch string, base Reference) WorkspaceDiff {
	v := WorkspaceDiff{Comparison: DiffBranch, RepositoryID: NewID(), Path: ".", Base: DiffCommit, BaseObject: strings.Repeat("c", 40), HeadCommit: strings.Repeat("a", 40), BaseCommit: strings.Repeat("b", 40), MergeBase: strings.Repeat("c", 40), BaseRef: base, Patch: patch, Untracked: []string{}}
	v.Revision = v.Digest()
	return v
}

func TestReviewFilesParseBranchObservationsForEveryBaseKind(t *testing.T) {
	for name, base := range map[string]Reference{
		"local branch":  {Type: LocalBranch, Name: "main"},
		"remote branch": {Type: RemoteBranch, Remote: "origin", Name: "main"},
		"commit":        {Type: CommitReference, Name: strings.Repeat("b", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			v := branchReviewDiffFixture(ordinaryReviewPatch, base)
			files, err := v.ReviewFiles()
			if err != nil || len(files) != 1 || files[0].Path != "file.txt" || files[0].Kind != ReviewTextFile || len(files[0].Lines) != 4 {
				t.Fatal(files, err)
			}
			if files[0].Lines[1].Old != 2 || files[0].Lines[1].New != 0 || files[0].Lines[2].New != 2 || files[0].Lines[2].Old != 0 {
				t.Fatal("line coordinates changed", files[0].Lines)
			}
			// The branch coordinates must equal those of an identical working-tree patch.
			working, err := reviewDiffFixture(ordinaryReviewPatch).ReviewFiles()
			if err != nil || working[0].Digest != files[0].Digest {
				t.Fatal("branch file digest differs from the ordinary patch digest", err)
			}
			selection := ReviewSelection{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 1, End: 3}
			anchor, err := v.ReviewAnchor(selection)
			if err != nil || anchor.BaseRef != base || anchor.DiffRevision != v.Revision || anchor.Comparison != DiffBranch || anchor.FileDigest != files[0].Digest || anchor.Context != "context\nnew\ntail\n" || !anchor.Matches(v) {
				t.Fatal(anchor, err)
			}
			changedBase := v
			changedBase.BaseRef.Name = "other"
			changedBase.Revision = changedBase.Digest()
			if anchor.Matches(changedBase) {
				t.Fatal("anchor matched a different base reference")
			}
			changedPatch := branchReviewDiffFixture(strings.Replace(ordinaryReviewPatch, "+new", "+newer", 1), base)
			changedPatch.RepositoryID = v.RepositoryID
			changedPatch.Revision = changedPatch.Digest()
			if anchor.Matches(changedPatch) {
				t.Fatal("anchor matched a changed revision")
			}
		})
	}
}

func TestReviewFilesBranchEmptyPatchIsEmptyNonNilSlice(t *testing.T) {
	files, err := branchReviewDiffFixture("", Reference{Type: LocalBranch, Name: "main"}).ReviewFiles()
	if err != nil || files == nil || len(files) != 0 {
		t.Fatal(files, err)
	}
}

func TestReviewFilesBranchRejectsInvalidObservations(t *testing.T) {
	base := Reference{Type: LocalBranch, Name: "main"}
	valid := func() WorkspaceDiff { return branchReviewDiffFixture(ordinaryReviewPatch, base) }
	for name, change := range map[string]func(*WorkspaceDiff){
		"missing base ref":    func(v *WorkspaceDiff) { v.BaseRef = Reference{}; v.Revision = v.Digest() },
		"unknown base type":   func(v *WorkspaceDiff) { v.BaseRef.Type = "tag"; v.Revision = v.Digest() },
		"remote on local ref": func(v *WorkspaceDiff) { v.BaseRef.Remote = "origin"; v.Revision = v.Digest() },
		"altered base ref":    func(v *WorkspaceDiff) { v.BaseRef.Name = "other" },
		"tampered patch":      func(v *WorkspaceDiff) { v.Patch = strings.Replace(v.Patch, "+new", "+newer", 1) },
		"merge base mismatch": func(v *WorkspaceDiff) { v.MergeBase = strings.Repeat("d", 40); v.Revision = v.Digest() },
		"missing base commit": func(v *WorkspaceDiff) { v.BaseCommit = ""; v.Revision = v.Digest() },
		"combined patch": func(v *WorkspaceDiff) {
			v.Patch = strings.Replace(ordinaryReviewPatch, "diff --git a/file.txt b/file.txt", "diff --cc file.txt", 1)
			v.Revision = v.Digest()
		},
		"incomplete patch": func(v *WorkspaceDiff) {
			v.Patch = strings.TrimSuffix(ordinaryReviewPatch, "\n")
			v.Revision = v.Digest()
		},
		"mismatched header": func(v *WorkspaceDiff) {
			v.Patch = strings.Replace(ordinaryReviewPatch, "+++ b/file.txt", "+++ b/other.txt", 1)
			v.Revision = v.Digest()
		},
		"short hunk count": func(v *WorkspaceDiff) {
			v.Patch = strings.Replace(ordinaryReviewPatch, "-1,3", "-1,2", 1)
			v.Revision = v.Digest()
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := valid()
			change(&v)
			if _, err := v.ReviewFiles(); err == nil {
				t.Fatal("invalid branch observation accepted")
			}
			if _, err := v.ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewFileAnchor}); err == nil {
				t.Fatal("anchor created for invalid branch observation")
			}
		})
	}
}

func TestReviewFilesNonBranchObservationsStayUnchanged(t *testing.T) {
	for _, comparison := range []WorkspaceDiffComparison{DiffWorkingTree, DiffStaged} {
		v := reviewDiffFixture(ordinaryReviewPatch)
		v.Comparison = comparison
		v.Revision = v.Digest()
		if files, err := v.ReviewFiles(); err != nil || len(files) != 1 {
			t.Fatal(comparison, files, err)
		}
		// A non-branch observation must not carry a speculative branch base.
		v.BaseRef = Reference{Type: LocalBranch, Name: "main"}
		v.Revision = v.Digest()
		if _, err := v.ReviewFiles(); err == nil {
			t.Fatal(comparison, "accepted branch base on non-branch observation")
		}
	}
}
