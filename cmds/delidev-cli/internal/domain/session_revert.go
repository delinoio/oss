// SPDX-License-Identifier: Apache-2.0
package domain

import "slices"

const CodexSessionRevertV1 WorkerCapability = "codex-session-revert-v1"

// CodexSessionRevertTargetProofV1 is advertised by a Worker that decodes the
// retained checkpoint reference of a Revert target and resolves the original
// selected-skill proof from it. A Worker without it strictly rejects those
// fields, so the server never freezes them for such a Worker.
const CodexSessionRevertTargetProofV1 WorkerCapability = "codex-session-revert-target-proof-v1"

type SessionRevertTarget struct {
	MessageID       ID             `json:"message_id"`
	InputID         ID             `json:"input_id"`
	NativeTurnID    NativeIdentity `json:"native_turn_id"`
	Prompt          SessionInput   `json:"prompt"`
	ContextRevision uint64         `json:"context_revision"`
	// ExecutionID and CheckpointDigest identify the immutable retained native
	// checkpoint of the execution that accepted this target input. They let the
	// Worker resolve the original selected-skill proof for a target that predates
	// the latest source execution. Both are absent for text-only compatibility
	// and for jobs accepted before this proof was carried. They are frozen only
	// when a skilled target belongs to an earlier execution than the source, the
	// one case the source checkpoint cannot prove, so a Worker that predates them
	// still receives the original envelope in every other case.
	ExecutionID      ID     `json:"execution_id,omitempty"`
	CheckpointDigest string `json:"checkpoint_digest,omitempty"`
}

// CarriesProof reports whether the target freezes a retained checkpoint
// reference, which only a Worker with CodexSessionRevertTargetProofV1 decodes.
func (t SessionRevertTarget) CarriesProof() bool {
	return t.ExecutionID != ""
}

func (t SessionRevertTarget) Validate() error {
	if UniqueIDs([]ID{t.MessageID, t.InputID}) != nil || t.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || t.Prompt.Validate() != nil {
		return CompactionUncertain()
	}
	// The retained checkpoint reference is all-or-nothing and digest-shaped.
	if (t.ExecutionID == "") != (t.CheckpointDigest == "") || t.ExecutionID != "" && (t.ExecutionID.Validate() != nil || !validCompactionDigest(t.CheckpointDigest)) {
		return CompactionUncertain()
	}
	return nil
}

type SessionRevertResult struct {
	Target          SessionRevertTarget `json:"target"`
	NativeThreadID  NativeIdentity      `json:"native_thread_id"`
	RetainedTurnIDs []NativeIdentity    `json:"retained_turn_ids"`
	HistoryDigest   string              `json:"history_digest"`
	ContextRevision uint64              `json:"context_revision"`
}

func (r SessionCompactionResult) validateRevert() error {
	p := r.Revert
	if p == nil || p.Target.Validate() != nil || p.ContextRevision == 0 || p.Target.ContextRevision == ^uint64(0) || p.ContextRevision != p.Target.ContextRevision+1 || p.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || p.RetainedTurnIDs == nil || len(p.RetainedTurnIDs) > 128 || !validCompactionDigest(p.HistoryDigest) || r.Harness != Codex || r.Codex != nil || r.OpenCode != nil || r.Outcome != CompactionSucceeded || !r.CleanupVerified || r.Checkpoint.Validate() != nil || r.Checkpoint.ActionID != r.ActionID || r.Checkpoint.ExecutionID != r.ExecutionID || r.Checkpoint.RequiresResume || !r.Checkpoint.Revert || r.Checkpoint.ContextRevision != p.ContextRevision || r.OuterKind != "" || r.OuterError || r.CommandEchoID != "" || r.ResultID != "" || r.CommandCompletedID != "" || r.IdleID != "" || r.BoundaryID != "" || r.SummaryID != "" || r.Boundary != nil || r.Summary != nil {
		return CompactionUncertain()
	}
	seen := map[NativeIdentity]bool{}
	for _, id := range p.RetainedTurnIDs {
		if id.Validate(Codex, NativeTurnIdentity) != nil || id == p.Target.NativeTurnID || seen[id] {
			return CompactionUncertain()
		}
		seen[id] = true
	}
	return nil
}

type SessionRevertState struct {
	ActionID ID                  `json:"action_id"`
	JobID    ID                  `json:"job_id"`
	Result   SessionRevertResult `json:"result"`
}

func (s SessionRevertState) Contains(turn NativeIdentity) bool {
	return slices.Contains(s.Result.RetainedTurnIDs, turn)
}
