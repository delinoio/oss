package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

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
	if i.Continuation == nil {
		return s.CurrentExecution == nil && initial.ID == i.ExecutionID && initial.InputID == i.InputID
	}
	return s.CurrentExecution != nil && i.Continuation.HistoryExecutionID == initial.ID && initial.InitialAccountID == selected.AccountID && initial.ConnectionID == selected.ConnectionID
}

// ExecutionContinuation retains the preceding public progress before advancing
// Session.Execution. Native paths/defaults stay in the digest-bound Worker file.
type ExecutionContinuation struct {
	HistoryExecutionID    ID                  `json:"history_execution_id"`
	HistoryRequestID      ID                  `json:"history_request_id"`
	Previous              ExecutionProgress   `json:"previous"`
	Completion            ExecutionCompletion `json:"completion"`
	AssignmentInputDigest string              `json:"assignment_input_digest"`
	InputMode             SessionMode         `json:"input_mode"`
	PromptDigest          string              `json:"prompt_digest"`
	Intent                ExecutionIntent     `json:"intent"`
}

func (c ExecutionContinuation) Validate(input ExecutionJobInput) error {
	invalid := func() error {
		return Fail(RecoveryRequired, "Continuation does not match a verified preceding execution.", "Preserve the original assignment, terminal history and cleanup proof before sending new input.")
	}
	p, done := c.Previous, c.Completion
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
	if c.HistoryExecutionID == input.ExecutionID || p.ExecutionID == input.ExecutionID || p.InputID == input.InputID || c.HistoryRequestID == input.ThreadRequestID || c.HistoryRequestID == input.TurnRequestID || !c.InputMode.Valid() || done.Version != 2 || done.Validate() != nil || !p.CleanupVerified || p.Waiting != (NativeWaiting{}) || p.UnconfirmedResponses != 0 || p.ExecutionID != done.ExecutionID || p.InputID != done.InputID || p.NativeThreadID != string(done.NativeThreadID) || p.NativeTurnID != string(done.NativeTurnID) || p.LastSequence != done.LastSequence || p.Outcome != done.Outcome || p.Observed.Validate(input.Configuration) != nil {
		return invalid()
	}
	if c.Intent != ContinueExplicitly && (c.Intent != ContinueAutomatically || p.Outcome != ExecutionSucceeded) {
		return invalid()
	}
	return nil
}
