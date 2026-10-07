package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"
)

// SessionAccountChange is an explicit selection for future execution. It never
// rewrites a completed assignment, its usage, or the original candidate snapshot.
type SessionAccountChange struct {
	RequestID         ID        `json:"request_id"`
	Revision          uint64    `json:"revision,string"`
	AfterExecutionID  ID        `json:"after_execution_id"`
	PreviousAccountID ID        `json:"previous_account_id"`
	AccountID         ID        `json:"account_id"`
	ConnectionID      ID        `json:"connection_id"`
	ChangedAt         time.Time `json:"changed_at"`
}

type NativeHistoryMode string

const (
	FullNativeHistory   NativeHistoryMode = "full-history"
	AccountBoundHistory NativeHistoryMode = "account-bound"
)

func (s Session) ContinuationAccount() (ID, ID) {
	if len(s.AccountChanges) != 0 {
		change := s.AccountChanges[len(s.AccountChanges)-1]
		return change.AccountID, change.ConnectionID
	}
	selected := s.ExecutionSelection()
	return selected.AccountID, selected.ConnectionID
}

type ExecutionIntent string

const (
	ContinueAutomatically ExecutionIntent = "continue-automatically"
	ContinueExplicitly    ExecutionIntent = "explicit-resume"
)

// ExecutionSelection advances per turn without rewriting the first snapshot,
// routing observation or original account. Each turn owns a fresh grant/job.
type ExecutionSelection struct {
	ID           ID `json:"id"`
	InputID      ID `json:"input_id"`
	AccountID    ID `json:"account_id"`
	ConnectionID ID `json:"connection_id"`
}

func (s Session) ExecutionSelection() ExecutionSelection {
	if s.CurrentExecution != nil {
		return *s.CurrentExecution
	}
	if s.InitialExecution != nil {
		i := s.InitialExecution
		return ExecutionSelection{ID: i.ID, InputID: i.InputID, AccountID: i.InitialAccountID, ConnectionID: i.ConnectionID}
	}
	return ExecutionSelection{}
}

func (s Session) OwnsExecution(i ExecutionJobInput) bool {
	initial := s.InitialExecution
	selected := s.ExecutionSelection()
	if initial == nil || s.MachineID != i.MachineID || s.AgentID != i.Configuration.AgentID || initial.ConfigurationDigest != i.ConfigurationDigest || selected.ID != i.ExecutionID || selected.InputID != i.InputID || selected.AccountID != i.AccountID || selected.ConnectionID != i.ConnectionID {
		return false
	}
	if i.Retry != nil && i.Continuation == nil && i.Fork == nil {
		return s.CurrentExecution != nil && s.NativeExecutionRoot() == i.ExecutionID && initial.InitialAccountID == selected.AccountID && initial.ConnectionID == selected.ConnectionID
	}
	if i.Continuation == nil {
		if i.Fork != nil {
			return s.Fork != nil && s.CurrentExecution != nil && initial.ID == i.Fork.RuntimeID && s.Fork.JobID == i.Fork.JobID && s.Fork.CheckpointDigest == i.Fork.CheckpointDigest && initial.InitialAccountID == selected.AccountID && initial.ConnectionID == selected.ConnectionID
		}
		return s.CurrentExecution == nil && initial.ID == i.ExecutionID && initial.InputID == i.InputID
	}
	authorized := initial.InitialAccountID == selected.AccountID && initial.ConnectionID == selected.ConnectionID || slices.ContainsFunc(s.AccountChanges, func(change SessionAccountChange) bool {
		return change.AccountID == selected.AccountID && change.ConnectionID == selected.ConnectionID
	})
	return s.CurrentExecution != nil && i.Continuation.HistoryExecutionID == s.NativeExecutionRoot() && authorized
}

// ExecutionContinuation retains the preceding public progress before advancing
// Session.Execution. Native paths/defaults stay in the digest-bound Worker file.
type ExecutionContinuation struct {
	Compaction            *SessionCompactionRef `json:"compaction,omitempty"`
	HistoryExecutionID    ID                    `json:"history_execution_id"`
	HistoryRequestID      ID                    `json:"history_request_id"`
	Previous              ExecutionProgress     `json:"previous"`
	Completion            ExecutionCompletion   `json:"completion"`
	AssignmentInputDigest string                `json:"assignment_input_digest"`
	InputMode             SessionMode           `json:"input_mode"`
	PromptDigest          string                `json:"prompt_digest"`
	Intent                ExecutionIntent       `json:"intent"`
	// Only a switch carries predecessor account scope. The Worker reads the
	// original checkpoint under that scope, never under the new credential.
	PreviousAccountID    ID `json:"previous_account_id,omitempty"`
	PreviousConnectionID ID `json:"previous_connection_id,omitempty"`
}

func (c ExecutionContinuation) Validate(input ExecutionJobInput) error {
	if c.Compaction != nil && (input.Configuration.Harness == Codex || input.Configuration.Harness == OpenCode) && (c.Compaction.RequiresResume || len(c.Previous.Subagents) != 0 || c.Previous.Outcome != ExecutionSucceeded) {
		return CompactionUncertain()
	}
	if c.Compaction != nil && ((input.Configuration.Harness != ClaudeCode && input.Configuration.Harness != Codex && input.Configuration.Harness != OpenCode) || c.Compaction.Validate() != nil || c.Compaction.ExecutionID != c.Previous.ExecutionID || c.Compaction.RequiresResume && c.Intent != ContinueExplicitly) {
		return CompactionUncertain()
	}
	invalid := func() error {
		return Fail(RecoveryRequired, "Continuation does not match a verified preceding execution.", "Preserve the original assignment, terminal history and cleanup proof before sending new input.")
	}
	p, done := c.Previous, c.Completion
	if c.PreviousAccountID != "" || c.PreviousConnectionID != "" {
		if input.Configuration.Harness != Codex || c.PreviousAccountID.Validate() != nil || c.PreviousConnectionID.Validate() != nil || (c.PreviousAccountID == input.AccountID && c.PreviousConnectionID == input.ConnectionID) || p.NativeHistory != FullNativeHistory || c.Intent != ContinueExplicitly {
			return invalid()
		}
	}
	if input.Configuration.Harness == ClaudeCode && (!p.ClaudeContinuationBoundary(p.InputID) || c.InputMode != input.Input.Mode) {
		return invalid()
	}
	for _, id := range []ID{c.HistoryExecutionID, c.HistoryRequestID, p.JobID, p.ExecutionID, p.InputID} {
		if id.Validate() != nil {
			return invalid()
		}
	}
	for _, value := range []string{c.AssignmentInputDigest, c.PromptDigest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return invalid()
		}
	}
	if c.HistoryExecutionID == input.ExecutionID || p.ExecutionID == input.ExecutionID || p.InputID == input.InputID || c.HistoryRequestID == input.ThreadRequestID || c.HistoryRequestID == input.TurnRequestID || !c.InputMode.Valid() || done.Version != 2 || done.ValidateForHarness(input.Configuration.Harness) != nil || !p.NativeCompactions.Closed() || p.Waiting != (NativeWaiting{}) || p.UnconfirmedResponses != 0 || p.ExecutionID != done.ExecutionID || p.InputID != done.InputID || p.NativeThreadID != string(done.NativeThreadID) || p.NativeTurnID != string(done.NativeTurnID) || p.LastSequence != done.LastSequence || p.Outcome != done.Outcome || p.Observed.ValidateForInput(input.Configuration, c.InputMode) != nil {
		return invalid()
	}
	if c.Intent != ContinueExplicitly && (c.Intent != ContinueAutomatically || p.Outcome != ExecutionSucceeded) {
		return invalid()
	}
	bindings, err := CheckedExecutionInputs(p.InputID, c.PromptDigest, p.AcceptedInputs)
	if err != nil {
		return invalid()
	}
	for _, binding := range bindings {
		if binding.InputID == input.InputID {
			return invalid()
		}
	}
	return nil
}
