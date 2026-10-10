// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"math"
)

// NativeCodeReviewTarget is independent of ordinary local feedback and approval
// review. Revisions identify the selected evidence; they grant no file writes.
type NativeCodeReviewTargetKind string

const (
	ReviewUncommitted NativeCodeReviewTargetKind = "uncommitted"
	ReviewBaseBranch  NativeCodeReviewTargetKind = "base-branch"
	ReviewCommit      NativeCodeReviewTargetKind = "commit"
	ReviewCustom      NativeCodeReviewTargetKind = "custom"
)

type NativeCodeReviewTarget struct {
	Kind         NativeCodeReviewTargetKind `json:"kind"`
	RepositoryID ID                         `json:"repository_id"`
	DiffRevision string                     `json:"diff_revision"`
	Reference    string                     `json:"reference,omitempty"`
	Instructions string                     `json:"instructions,omitempty"`
}

func NativeCodeReviewUnavailable() *Error {
	return Fail(Unsupported, "The original native code review cannot be verified.", "Retain the selected target and original review; do not submit ordinary feedback or retry an uncertain native review.")
}
func (t NativeCodeReviewTarget) Validate() error {
	if t.RepositoryID.Validate() != nil || !reviewDigest(t.DiffRevision) {
		return NativeCodeReviewUnavailable()
	}
	switch t.Kind {
	case ReviewUncommitted:
		if t.Reference != "" || t.Instructions != "" {
			return NativeCodeReviewUnavailable()
		}
	case ReviewBaseBranch:
		if Text(t.Reference, "review base reference", 256, true) != nil || t.Instructions != "" {
			return NativeCodeReviewUnavailable()
		}
	case ReviewCommit:
		raw, err := hex.DecodeString(t.Reference)
		if err != nil || len(raw) != 20 || hex.EncodeToString(raw) != t.Reference || t.Instructions != "" {
			return NativeCodeReviewUnavailable()
		}
	case ReviewCustom:
		if t.Reference != "" || Text(t.Instructions, "custom native review instructions", MaxPromptBytes, true) != nil {
			return NativeCodeReviewUnavailable()
		}
	default:
		return NativeCodeReviewUnavailable()
	}
	return nil
}

// The Worker derives immutable object/content evidence from the original
// prepared repository before claiming review/start. A later target drift cannot
// promote native findings into current evidence or authorize another native send.
type NativeCodeReviewSelection struct {
	Target        NativeCodeReviewTarget `json:"target"`
	HeadCommit    string                 `json:"head_commit"`
	BaseCommit    string                 `json:"base_commit,omitempty"`
	ContentDigest string                 `json:"content_digest"`
}

func (s NativeCodeReviewSelection) Validate() error {
	if s.Target.Validate() != nil || !reviewDigest(s.ContentDigest) || !reviewObjectID(s.HeadCommit) {
		return NativeCodeReviewUnavailable()
	}
	if s.Target.Kind == ReviewBaseBranch {
		if !reviewObjectID(s.BaseCommit) {
			return NativeCodeReviewUnavailable()
		}
	} else if s.BaseCommit != "" {
		return NativeCodeReviewUnavailable()
	}
	return nil
}
func reviewObjectID(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == 20 && hex.EncodeToString(raw) == s
}

type NativeCodeReviewFinding struct {
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Confidence float64 `json:"confidence"`
	Priority   int32   `json:"priority"`
	Path       string  `json:"path"`
	StartLine  uint32  `json:"start_line"`
	EndLine    uint32  `json:"end_line"`
}

func (f NativeCodeReviewFinding) Validate() error {
	if Text(f.Title, "native review finding title", 1024, true) != nil || Text(f.Body, "native review finding", 8192, true) != nil || !WorkspacePath(f.Path) || f.Path == "." || f.StartLine == 0 || f.EndLine < f.StartLine || f.EndLine-f.StartLine > 1000 || f.Priority < 0 || f.Priority > 3 || math.IsNaN(f.Confidence) || math.IsInf(f.Confidence, 0) || f.Confidence < 0 || f.Confidence > 1 {
		return NativeCodeReviewUnavailable()
	}
	return nil
}

// Each auxiliary review has its own action/job and immutable source assignment.
// It never replaces a conversation execution or its continuation predecessor.
type NativeCodeReviewInput struct {
	Version        uint32                 `json:"version"`
	ActionID       ID                     `json:"action_id"`
	SourceJobID    ID                     `json:"source_job_id"`
	SourceRevision uint64                 `json:"source_revision"`
	Source         ExecutionJobInput      `json:"source"`
	Target         NativeCodeReviewTarget `json:"target"`
	Actor          Principal              `json:"actor"`
}

func (i NativeCodeReviewInput) Validate() error {
	if i.Version != 1 || UniqueIDs([]ID{i.ActionID, i.SourceJobID, i.Source.ExecutionID, i.Source.InputID, i.Source.SessionID}) != nil || i.SourceRevision == 0 || i.SourceRevision >= 1<<63 || i.Source.Validate() != nil || i.Source.Configuration.Harness != Codex || i.Target.Validate() != nil || (i.Actor.Type != OwnerDevice && i.Actor.Type != ClientDevice) {
		return NativeCodeReviewUnavailable()
	}
	return nil
}

type NativeCodeReviewState string

const (
	NativeReviewQueued      NativeCodeReviewState = "queued"
	NativeReviewReady       NativeCodeReviewState = "ready"
	NativeReviewEntered     NativeCodeReviewState = "entered"
	NativeReviewExited      NativeCodeReviewState = "exited"
	NativeReviewCompleted   NativeCodeReviewState = "completed"
	NativeReviewInterrupted NativeCodeReviewState = "interrupted"
	NativeReviewUncertain   NativeCodeReviewState = "uncertain"
	NativeReviewStale       NativeCodeReviewState = "stale"
)

// No native path, raw rollout bytes, credentials or inferred workspace mutation
// enters this projection. Findings remain bound to their original selection.
type NativeCodeReviewResult struct {
	Version         uint32                    `json:"version"`
	ActionID        ID                        `json:"action_id"`
	Selection       NativeCodeReviewSelection `json:"selection"`
	ThreadID        NativeIdentity            `json:"thread_id"`
	TurnID          NativeIdentity            `json:"turn_id"`
	EnteredItemID   string                    `json:"entered_item_id"`
	ExitedItemID    string                    `json:"exited_item_id"`
	Findings        []NativeCodeReviewFinding `json:"findings"`
	Explanation     string                    `json:"explanation"`
	Correctness     string                    `json:"correctness"`
	Confidence      float64                   `json:"confidence"`
	RolloutDigest   string                    `json:"rollout_digest"`
	CleanupVerified bool                      `json:"cleanup_verified"`
}

func (r NativeCodeReviewResult) Validate() error {
	if r.Version != 1 || r.ActionID.Validate() != nil || r.Selection.Validate() != nil || r.ThreadID.Validate(Codex, NativeThreadIdentity) != nil || r.TurnID.Validate(Codex, NativeTurnIdentity) != nil || Text(r.EnteredItemID, "entered review item", 256, true) != nil || Text(r.ExitedItemID, "exited review item", 256, true) != nil || r.EnteredItemID == r.ExitedItemID || r.Findings == nil || len(r.Findings) > 64 || Text(r.Explanation, "native review explanation", 8192, false) != nil || Text(r.Correctness, "native review correctness", 256, false) != nil || math.IsNaN(r.Confidence) || math.IsInf(r.Confidence, 0) || r.Confidence < 0 || r.Confidence > 1 || !reviewDigest(r.RolloutDigest) || !r.CleanupVerified {
		return NativeCodeReviewUnavailable()
	}
	for _, f := range r.Findings {
		if f.Validate() != nil {
			return NativeCodeReviewUnavailable()
		}
	}
	return nil
}
