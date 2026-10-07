package store

import (
	"bytes"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// VerifiedStartupRejection compares an already accepted server rejection with
// the exact original assignment and retained input. It cannot accept a fresh
// Worker report, infer rejection from missing events, or authorize another send.
func (t *Tx) VerifiedStartupRejection(sessionID domain.ID) (Record, domain.ExecutionStartupRejection, bool, error) {
	var empty domain.ExecutionStartupRejection
	sr, err := t.Get(domain.SessionKind, sessionID)
	if err != nil {
		return Record{}, empty, false, err
	}
	s, err := Decode[domain.Session](sr)
	if err != nil {
		return Record{}, empty, false, err
	}
	if s.StartupRejection == nil {
		return Record{}, empty, false, nil
	}
	r := *s.StartupRejection
	if r.Validate() != nil || r.Workspace.SessionID != sr.ID || s.Execution != nil || s.CurrentExecution != nil || s.ActiveExecutionID != "" || s.Recovery != domain.NoRecovery || s.Outcome != domain.ExecutionNotStarted || s.Dispatch != domain.DispatchPaused || s.PendingSteerID != "" || s.NextExecutionIntent != "" {
		return Record{}, empty, false, domain.StartupRejectionUncertain()
	}
	jr, err := t.Get(domain.JobKind, r.Workspace.JobID)
	if err != nil {
		return Record{}, empty, false, err
	}
	j, err := Decode[domain.Job](jr)
	if err != nil {
		return Record{}, empty, false, err
	}
	a, err := t.JobAssignment(jr.ID)
	if err != nil {
		return Record{}, empty, false, err
	}
	var input domain.ExecutionJobInput
	var original domain.Job
	var result domain.ExecutionStartupRejection
	state := j.State == domain.JobFailed && r.Workspace.Reason != domain.Canceled || j.State == domain.JobCanceled && r.Workspace.Reason == domain.Canceled
	if !state || j.Type != domain.ExecuteSessionJob || j.FinishedAt == nil || j.Problem == nil || j.Problem.Code != r.Workspace.Reason || jr.SessionID != sr.ID || jr.ProjectID != sr.ProjectID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(sessionID), j.MachineID != s.MachineID) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(sessionID), j.InstanceID != r.InstanceID) ||
		a.SessionID != sr.ID || a.ProjectID != sr.ProjectID || r.ValidateAssignment(a.ID, a.Revision, a.Data) != nil || domain.Decode(a.Data, &original) != nil || !bytes.Equal(j.Input, original.Input) || domain.Decode(j.Input, &input) != nil || !s.OwnsExecution(input) || domain.Decode(j.Output, &result) != nil {
		return Record{}, empty, false, domain.StartupRejectionUncertain()
	}
	expected, _ := json.Marshal(r)
	actual, err := json.Marshal(result)
	if err != nil || !bytes.Equal(expected, actual) {
		return Record{}, empty, false, domain.StartupRejectionUncertain()
	}
	ir, err := t.Get(domain.QueueKind, r.InputID)
	if err != nil {
		return Record{}, empty, false, err
	}
	i, err := Decode[domain.QueuedInput](ir)
	if err != nil {
		return Record{}, empty, false, err
	}
	if ir.SessionID != sr.ID || ir.ProjectID != sr.ProjectID || i.Delivery != domain.InputRejected || i.ExecutionID != r.Workspace.ExecutionID || i.NativeRequestID != input.TurnRequestID || i.Prompt != input.Input.Prompt || i.Mode != input.Input.Mode {
		return Record{}, empty, false, domain.StartupRejectionUncertain()
	}
	return jr, r, true, nil
}
