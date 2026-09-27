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
