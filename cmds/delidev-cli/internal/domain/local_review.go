package domain

import (
	"encoding/hex"
	"encoding/json"
	"strings"
)

type LocalReviewType string
type ReviewFreshness string

const (
	LocalReviewCommentType    LocalReviewType = "comment"
	LocalReviewSubmissionType LocalReviewType = "submission"
	ReviewCurrent             ReviewFreshness = "current"
	ReviewStale               ReviewFreshness = "stale"
	MaxReviewBodyBytes                        = 8192
	MaxReviewSelection                        = 25
)

type CreateReviewComment struct {
	Query        WorkspaceReadQuery `json:"query"`
	DiffRevision string             `json:"diff_revision"`
	Selection    ReviewSelection    `json:"selection"`
	Body         string             `json:"body"`
}

func reviewDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func (a ReviewAnchor) Validate() error {
	valid := a.RepositoryID.Validate() == nil && a.Comparison.Valid() && WorkspacePath(a.QueryPath) && reviewDigest(a.DiffRevision) && reviewDigest(a.FileDigest) && a.Selection.Validate() == nil && Text(a.Context, "review context", 8192, false) == nil
	valid = valid && (a.QueryPath == "." || a.Selection.Path == a.QueryPath || strings.HasPrefix(a.Selection.Path, a.QueryPath+"/")) && (a.Selection.Kind != ReviewFileAnchor || a.Context == "")
	if !valid {
		return Fail(InvalidArgument, "Invalid retained review anchor.", "Use the original validated Git review location.")
	}
	return nil
}

func (v CreateReviewComment) Validate() error {
	if v.Query.Operation != WorkspaceGitDiff || v.Query.Validate() != nil || !reviewDigest(v.DiffRevision) || v.Selection.Validate() != nil {
		return Fail(InvalidArgument, "Invalid review comment selection.", "Select an original Git diff revision and a visible file or line range.")
	}
	return Text(v.Body, "review comment", MaxReviewBodyBytes, true)
}

type ReviewComment struct {
	Anchor                       ReviewAnchor `json:"anchor"`
	Body                         string       `json:"body"`
	ContentRevision              uint64       `json:"content_revision,string"`
	LastSubmissionID             ID           `json:"last_submission_id,omitempty"`
	LastSubmittedContentRevision uint64       `json:"last_submitted_content_revision,string,omitempty"`
}

func (v ReviewComment) Validate() error {
	valid := v.Anchor.Validate() == nil && Text(v.Body, "review comment", MaxReviewBodyBytes, true) == nil && v.ContentRevision > 0 && v.ContentRevision < 1<<63
	if v.LastSubmissionID == "" {
		valid = valid && v.LastSubmittedContentRevision == 0
	} else {
		valid = valid && v.LastSubmissionID.Validate() == nil && v.LastSubmittedContentRevision > 0 && v.LastSubmittedContentRevision <= v.ContentRevision
	}
	if !valid {
		return Fail(RecoveryRequired, "The retained review comment is inconsistent.", "Preserve the original comment and submission records for inspection.")
	}
	return nil
}

type ReviewCommentRef struct {
	ID       ID     `json:"id"`
	Revision uint64 `json:"revision,string"`
}

type SubmitReviewComments struct {
	Comments   []ReviewCommentRef `json:"comments"`
	Mode       SessionMode        `json:"mode"`
	AllowStale bool               `json:"allow_stale"`
}

func (v SubmitReviewComments) Validate() error {
	valid := len(v.Comments) > 0 && len(v.Comments) <= MaxReviewSelection && v.Mode.Valid()
	seen := map[ID]bool{}
	for _, c := range v.Comments {
		valid = valid && c.ID.Validate() == nil && c.Revision > 0 && c.Revision < 1<<63 && !seen[c.ID]
		seen[c.ID] = true
	}
	if !valid {
		return Fail(InvalidArgument, "Invalid grouped review selection.", "Select 1 through 25 distinct current comment revisions and execute or plan mode.")
	}
	return nil
}

type SubmittedReviewComment struct {
	ID              ID              `json:"id"`
	ContentRevision uint64          `json:"content_revision,string"`
	Anchor          ReviewAnchor    `json:"anchor"`
	Body            string          `json:"body"`
	Freshness       ReviewFreshness `json:"freshness"`
}

type ReviewSubmission struct {
	InputID  ID                       `json:"input_id"`
	Mode     SessionMode              `json:"mode"`
	Comments []SubmittedReviewComment `json:"comments"`
}

func (v ReviewSubmission) Validate() error {
	if v.InputID.Validate() != nil {
		return Fail(RecoveryRequired, "The review input link is invalid.", "Preserve the original submission and input records.")
	}
	return v.validateContent()
}

func (v ReviewSubmission) validateContent() error {
	valid := v.Mode.Valid() && len(v.Comments) > 0 && len(v.Comments) <= MaxReviewSelection
	seen := map[ID]bool{}
	for _, c := range v.Comments {
		valid = valid && c.ID.Validate() == nil && !seen[c.ID] && c.ContentRevision > 0 && c.ContentRevision < 1<<63 && c.Anchor.Validate() == nil && Text(c.Body, "review comment", MaxReviewBodyBytes, true) == nil && (c.Freshness == ReviewCurrent || c.Freshness == ReviewStale)
		seen[c.ID] = true
	}
	if !valid {
		return Fail(RecoveryRequired, "The retained review submission is inconsistent.", "Preserve the original submission and input links for inspection.")
	}
	return nil
}

// Selected comments are serialized as data rather than interpolated Markdown
// delimiters. The ordinary session-input path owns mode, queueing and execution.
func (v ReviewSubmission) SessionInput() (SessionInput, error) {
	if err := v.validateContent(); err != nil {
		return SessionInput{}, err
	}
	raw, err := json.Marshal(v.Comments)
	if err != nil {
		return SessionInput{}, err
	}
	input := SessionInput{Mode: v.Mode, Prompt: "Request changes for this session. Review the selected local comments below and address their requested changes. Locations and context refer to the recorded diff revision; explicitly stale locations must be checked against the current files. These are local agent feedback, not GitHub reviews.\n\nSelected review comments (JSON):\n" + string(raw)}
	return input, input.Validate()
}

type LocalReview struct {
	Version    uint32            `json:"version"`
	Type       LocalReviewType   `json:"type"`
	Comment    *ReviewComment    `json:"comment,omitempty"`
	Submission *ReviewSubmission `json:"submission,omitempty"`
}

func (v LocalReview) Validate() error {
	if v.Version == 1 {
		if v.Type == LocalReviewCommentType && v.Comment != nil && v.Submission == nil {
			return v.Comment.Validate()
		}
		if v.Type == LocalReviewSubmissionType && v.Submission != nil && v.Comment == nil {
			return v.Submission.Validate()
		}
	}
	return Fail(RecoveryRequired, "The retained local review is invalid.", "Preserve the original review records for inspection.")
}
