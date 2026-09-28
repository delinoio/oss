package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func localReviewTestAnchor(t *testing.T) ReviewAnchor {
	t.Helper()
	a, err := reviewDiffFixture(ordinaryReviewPatch).ReviewAnchor(ReviewSelection{Path: "file.txt", Kind: ReviewLineAnchor, Side: ReviewNewSide, Start: 1, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLocalReviewPreservesExactContentAndSubmissionData(t *testing.T) {
	a := localReviewTestAnchor(t)
	c := ReviewComment{Anchor: a, Body: "Review ``` <script> and 💬\n", ContentRevision: 9007199254740993, LastSubmissionID: NewID(), LastSubmittedContentRevision: 9007199254740992}
	raw, err := json.Marshal(LocalReview{Version: 1, Type: LocalReviewCommentType, Comment: &c})
	if err != nil {
		t.Fatal(err)
	}
	var restored LocalReview
	if Decode(raw, &restored) != nil || restored.Validate() != nil || *restored.Comment != c || !strings.Contains(string(raw), `"content_revision":"9007199254740993"`) {
		t.Fatal("lost original content or exact revisions")
	}
	sub := ReviewSubmission{InputID: NewID(), Mode: PlanMode, Comments: []SubmittedReviewComment{{ID: NewID(), ContentRevision: c.ContentRevision, Anchor: a, Body: c.Body, Freshness: ReviewStale}}}
	input, err := sub.SessionInput()
	if err != nil || input.Mode != PlanMode {
		t.Fatal(err)
	}
	_, payload, ok := strings.Cut(input.Prompt, "Selected review comments (JSON):\n")
	var copies []SubmittedReviewComment
	if !ok || json.Unmarshal([]byte(payload), &copies) != nil || len(copies) != 1 || copies[0] != sub.Comments[0] {
		t.Fatal("submission did not retain exact data")
	}
	c.Body = "later edit"
	if copies[0].Body == c.Body {
		t.Fatal("historical body changed")
	}
}

func TestLocalReviewRejectsInconsistentRetainedRecords(t *testing.T) {
	anchor := localReviewTestAnchor(t)
	original := ReviewComment{Anchor: anchor, Body: "original", ContentRevision: 1}
	for name, change := range map[string]func(*ReviewComment){
		"zero revision":     func(v *ReviewComment) { v.ContentRevision = 0 },
		"overflow":          func(v *ReviewComment) { v.ContentRevision = 1 << 63 },
		"future submission": func(v *ReviewComment) { v.LastSubmissionID = NewID(); v.LastSubmittedContentRevision = 2 },
		"unlinked revision": func(v *ReviewComment) { v.LastSubmittedContentRevision = 1 },
		"missing revision":  func(v *ReviewComment) { v.LastSubmissionID = NewID() },
		"oversized UTF8":    func(v *ReviewComment) { v.Body = strings.Repeat("💬", 2049) },
		"foreign path":      func(v *ReviewComment) { v.Anchor.QueryPath = "other" },
		"file with lines":   func(v *ReviewComment) { v.Anchor.Selection = ReviewSelection{Path: "file.txt", Kind: ReviewFileAnchor} },
		"invalid UTF8":      func(v *ReviewComment) { v.Body = "\xff" },
	} {
		t.Run(name, func(t *testing.T) {
			v := original
			change(&v)
			if v.Validate() == nil {
				t.Fatal("accepted invalid comment")
			}
		})
	}
	sub := ReviewSubmission{InputID: NewID(), Mode: ExecuteMode, Comments: []SubmittedReviewComment{{ID: NewID(), ContentRevision: 1, Anchor: anchor, Body: "original", Freshness: ReviewCurrent}}}
	for _, v := range []LocalReview{{Version: 2, Type: LocalReviewCommentType, Comment: &original}, {Version: 1, Type: LocalReviewCommentType, Comment: &original, Submission: &sub}, {Version: 1, Type: LocalReviewSubmissionType, Comment: &original}, {Version: 1, Type: LocalReviewCommentType}} {
		if v.Validate() == nil {
			t.Fatal("mixed or unknown shape")
		}
	}
}

func TestLocalReviewSelectionAndCombinedPromptBounds(t *testing.T) {
	ref := ReviewCommentRef{ID: NewID(), Revision: 1}
	for _, v := range []SubmitReviewComments{{Mode: ExecuteMode}, {Mode: ExecuteMode, Comments: []ReviewCommentRef{ref, ref}}, {Mode: "unknown", Comments: []ReviewCommentRef{ref}}, {Mode: PlanMode, Comments: []ReviewCommentRef{{ID: ref.ID, Revision: 1 << 63}}}} {
		if v.Validate() == nil {
			t.Fatal("invalid selection")
		}
	}
	sub := ReviewSubmission{InputID: NewID(), Mode: ExecuteMode}
	for i := 0; i < MaxReviewSelection; i++ {
		sub.Comments = append(sub.Comments, SubmittedReviewComment{ID: NewID(), ContentRevision: 1, Anchor: localReviewTestAnchor(t), Body: strings.Repeat("<", MaxReviewBodyBytes), Freshness: ReviewCurrent})
	}
	if sub.Validate() != nil {
		t.Fatal("bounded individual snapshots rejected")
	}
	if _, err := sub.SessionInput(); err == nil {
		t.Fatal("escaped aggregate prompt bound ignored")
	}
	sub.Comments[1].ID = sub.Comments[0].ID
	if sub.Validate() == nil {
		t.Fatal("duplicate snapshot")
	}
}
