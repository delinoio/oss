// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func validCompactionDigest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == sha256.Size && hex.EncodeToString(b) == v
}

// Manual compaction is an action, never a conversation input or usage source.
type CompactionOutcome string

const (
	CompactionSucceeded CompactionOutcome = "success"
	CompactionFailed    CompactionOutcome = "failed"
)

type SessionCompactionRef struct {
	JobID            ID     `json:"job_id"`
	ActionID         ID     `json:"action_id"`
	ExecutionID      ID     `json:"execution_id"`
	CheckpointDigest string `json:"checkpoint_digest"`
	NativeDigest     string `json:"native_digest"`
	RequiresResume   bool   `json:"requires_resume"`
}

func (r SessionCompactionRef) Validate() error {
	if UniqueIDs([]ID{r.JobID, r.ActionID, r.ExecutionID}) != nil || !validCompactionDigest(r.CheckpointDigest) || !validCompactionDigest(r.NativeDigest) {
		return CompactionUncertain()
	}
	return nil
}

type SessionCompactionInput struct {
	Version     uint32                `json:"version"`
	ActionID    ID                    `json:"action_id"`
	SourceJobID ID                    `json:"source_job_id"`
	Restore     ExecutionJobInput     `json:"restore"`
	Assignment  ExecutionJobInput     `json:"assignment"`
	Completion  ExecutionCompletion   `json:"completion"`
	Previous    *SessionCompactionRef `json:"previous,omitempty"`
	Dispatch    DispatchState         `json:"dispatch"`
	Intent      ExecutionIntent       `json:"intent"`
}

func (i SessionCompactionInput) Validate() error {
	a, done := i.Assignment, i.Completion
	profile := i.Version == 1 && a.Configuration.Harness == ClaudeCode && a.Installation.Version == ClaudeProtocolVersion || i.Version == 2 && a.Configuration.Harness == Codex && CodexVersionAllowed(a.Installation.Version) || i.Version == 3 && a.Configuration.Harness == OpenCode && a.Installation.Version == OpenCodeProtocolVersion
	if !profile || UniqueIDs([]ID{i.ActionID, i.SourceJobID, a.ExecutionID, a.InputID, a.SessionID}) != nil || a.Validate() != nil || done.ValidateForHarness(a.Configuration.Harness) != nil || done.Version != 2 || done.ExecutionID != a.ExecutionID || done.InputID != a.InputID || done.Outcome != ExecutionSucceeded {
		return CompactionUncertain()
	}
	if i.Restore.Validate() != nil || i.Restore.ExecutionID != i.ActionID || i.Restore.Continuation == nil || i.Restore.Continuation.Previous.JobID != i.SourceJobID || i.Restore.Continuation.Completion != i.Completion || i.Restore.ConfigurationDigest != a.ConfigurationDigest || i.Restore.SessionID != a.SessionID || i.Restore.AccountID != a.AccountID || i.Restore.ConnectionID != a.ConnectionID {
		return CompactionUncertain()
	}
	if i.Previous != nil && (i.Previous.Validate() != nil || i.Previous.ExecutionID != a.ExecutionID) {
		return CompactionUncertain()
	}
	if (a.Configuration.Harness == Codex || a.Configuration.Harness == OpenCode) && (i.Dispatch != DispatchReady || i.Previous != nil && i.Previous.RequiresResume || len(i.Restore.Continuation.Previous.Subagents) != 0 || !i.Restore.Continuation.Previous.NativeCompactions.Closed()) {
		return CompactionUncertain()
	}
	if i.Dispatch != DispatchReady && i.Dispatch != DispatchPaused && i.Dispatch != DispatchBlocked {
		return CompactionUncertain()
	}
	// Every nested restore field remains the immutable original assignment;
	// only fresh operation identities and the verified predecessor may differ.
	c := i.Restore.Continuation
	want := a
	want.Version, want.ExecutionID, want.InputID = 2, i.ActionID, i.Restore.InputID
	want.ThreadRequestID, want.TurnRequestID, want.Continuation = i.Restore.ThreadRequestID, i.Restore.TurnRequestID, c
	expected, err := json.Marshal(want)
	actual, actualErr := json.Marshal(i.Restore)
	original, originalErr := json.Marshal(a)
	history := a.ExecutionID
	if a.Continuation != nil {
		history = a.Continuation.HistoryExecutionID
	}
	sameRef := i.Previous == nil && c.Compaction == nil || i.Previous != nil && c.Compaction != nil && *i.Previous == *c.Compaction
	if err != nil || actualErr != nil || originalErr != nil || !bytes.Equal(expected, actual) || !sameRef || c.HistoryExecutionID != history || c.Previous.ExecutionID != a.ExecutionID || c.Previous.InputID != a.InputID || c.AssignmentInputDigest != compactionDigest(original) || c.PromptDigest != compactionDigest([]byte(a.Input.Prompt)) || UniqueIDs([]ID{i.ActionID, i.Restore.InputID, i.Restore.ThreadRequestID, i.Restore.TurnRequestID, c.HistoryRequestID, a.InputID}) != nil || i.Intent != "" && i.Intent != ContinueAutomatically && i.Intent != ContinueExplicitly {
		return CompactionUncertain()
	}
	return nil
}

func compactionDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type SessionCompactionResult struct {
	Harness            Harness                   `json:"harness,omitempty"`
	Codex              *CodexCompactionResult    `json:"codex,omitempty"`
	OpenCode           *OpenCodeCompactionResult `json:"opencode,omitempty"`
	Version            uint32                    `json:"version"`
	ActionID           ID                        `json:"action_id"`
	ExecutionID        ID                        `json:"execution_id"`
	OuterKind          ClaudeResultKind          `json:"outer_kind"`
	OuterError         bool                      `json:"outer_error"`
	Outcome            CompactionOutcome         `json:"compact_result"`
	CommandEchoID      string                    `json:"command_echo_id"`
	ResultID           string                    `json:"result_id"`
	CommandCompletedID string                    `json:"command_completed_id"`
	IdleID             string                    `json:"idle_id"`
	BoundaryID         string                    `json:"boundary_id,omitempty"`
	SummaryID          string                    `json:"summary_id,omitempty"`
	Boundary           *ClaudeCompactionBoundary `json:"boundary,omitempty"`
	Summary            *SessionCompactionSummary `json:"summary,omitempty"`
	CleanupVerified    bool                      `json:"cleanup_verified"`
	Checkpoint         SessionCompactionRef      `json:"checkpoint"`
}

type CodexCompactionResult struct {
	NativeThreadID     NativeIdentity        `json:"native_thread_id"`
	SourceNativeTurnID NativeIdentity        `json:"source_native_turn_id"`
	NativeTurnID       NativeIdentity        `json:"native_turn_id"`
	LiveItemID         string                `json:"live_item_id"`
	HistoryItemID      string                `json:"history_item_id"`
	HistoryDigest      string                `json:"history_digest"`
	Actions            uint32                `json:"actions"`
	Acknowledged       bool                  `json:"acknowledged"`
	LifecycleCompleted bool                  `json:"lifecycle_completed"`
	ResponseUsages     []NativeResponseUsage `json:"response_usages"`
}

func (r SessionCompactionResult) Validate() error {
	if r.Version == 3 {
		return r.validateOpenCode()
	}
	if r.Version == 2 {
		if r.OpenCode != nil || r.Harness != Codex || r.Codex == nil || r.Outcome != CompactionSucceeded || r.OuterKind != "" || r.OuterError || r.CommandEchoID != "" || r.ResultID != "" || r.CommandCompletedID != "" || r.IdleID != "" || r.BoundaryID != "" || r.SummaryID != "" || r.Boundary != nil || r.Summary != nil || !r.CleanupVerified || r.Checkpoint.Validate() != nil || r.Checkpoint.ActionID != r.ActionID || r.Checkpoint.ExecutionID != r.ExecutionID || r.Checkpoint.RequiresResume {
			return CompactionUncertain()
		}
		p := r.Codex
		if p.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || p.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || p.SourceNativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || p.NativeTurnID == p.SourceNativeTurnID || Text(p.LiveItemID, "native context item", 1024, true) != nil || Text(p.HistoryItemID, "native durable context item", 1024, true) != nil || !validCompactionDigest(p.HistoryDigest) || p.Actions == 0 || p.Actions > 128 || !p.Acknowledged || !p.LifecycleCompleted || p.ResponseUsages == nil || len(p.ResponseUsages) > 128 {
			return CompactionUncertain()
		}
		seen := map[string]bool{}
		for _, usage := range p.ResponseUsages {
			if usage.Source != "" || usage.Validate() != nil || seen[usage.ResponseDigest] {
				return CompactionUncertain()
			}
			seen[usage.ResponseDigest] = true
		}
		return nil
	}
	if r.Harness != "" || r.Codex != nil || r.OpenCode != nil {
		return CompactionUncertain()
	}
	if r.OuterKind != ClaudeResultSuccess || r.OuterError || r.Version != 1 || r.Checkpoint.Validate() != nil || r.ActionID != r.Checkpoint.ActionID || r.ExecutionID != r.Checkpoint.ExecutionID || !r.CleanupVerified || r.CommandEchoID != string(r.ActionID) {
		return CompactionUncertain()
	}
	seen := map[string]bool{}
	for _, id := range []string{r.CommandEchoID, r.ResultID, r.CommandCompletedID, r.IdleID} {
		if !validClaudeContextID(id) || seen[id] {
			return CompactionUncertain()
		}
		seen[id] = true
	}
	switch r.Outcome {
	case CompactionSucceeded:
		if r.Checkpoint.RequiresResume || r.Boundary == nil || r.Summary == nil || r.Boundary.Validate() != nil || Text(r.Summary.Text, "native compaction summary", MaxMessageText, false) != nil || r.Boundary.Trigger != ClaudeManualCompaction || r.Summary.BoundaryID != r.BoundaryID || r.Boundary.SummaryAnchor() != r.SummaryID {
			return CompactionUncertain()
		}
		for _, id := range []string{r.BoundaryID, r.SummaryID} {
			if !validClaudeContextID(id) || seen[id] {
				return CompactionUncertain()
			}
			seen[id] = true
		}
	case CompactionFailed:
		if !r.Checkpoint.RequiresResume || r.Boundary != nil || r.Summary != nil || r.BoundaryID != "" || r.SummaryID != "" {
			return CompactionUncertain()
		}
	default:
		return CompactionUncertain()
	}
	return nil
}

type SessionCompactionSummary struct {
	BoundaryID string `json:"boundary_id"`
	Text       string `json:"text"`
}

func CompactionUncertain() *Error {
	return Fail(RecoveryRequired, "The native compaction action requires reconciliation.", "Preserve its original job, native checkpoint and cleanup evidence; never resend an uncertain command.")
}
