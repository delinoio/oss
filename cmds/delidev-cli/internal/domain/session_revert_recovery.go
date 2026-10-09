// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
)

// This closed variant observes one original context mutation. It grants no
// execution credentials and cannot change the target or issue another Revert.
type SessionRevertRecovery struct {
	Input SessionCompactionInput `json:"input"`
}

func (r ExecutionRecoveryRequest) validateRevertRecovery() error {
	p := r.Revert
	if p == nil || r.Version != 2 || r.Startup != nil || r.OpenCode != nil || r.Claude != nil || r.Harness != Codex || p.Input.Validate() != nil || p.Input.Version != 4 || p.Input.Revert == nil || r.JobID.Validate() != nil || r.AssignmentRevision == 0 || !validCompactionDigest(r.AssignmentDigest) || !validCompactionDigest(r.AssignmentInputDigest) || r.SessionID != p.Input.Assignment.SessionID || r.MachineID != p.Input.Assignment.MachineID || r.AccountID != p.Input.Assignment.AccountID || r.ConnectionID != p.Input.Assignment.ConnectionID || r.ContextRevision != p.Input.Revert.ContextRevision || r.Completion != (ExecutionCompletion{}) || r.ConfigurationDigest != "" || r.HistoryExecutionID != "" || r.InputMode != "" || r.PromptDigest != "" || r.AcceptedInputs != nil || !emptyRevertRecoveryJSON(r.Preparation) || !emptyRevertRecoveryJSON(r.Manifest) {
		return ExecutionRecoveryUncertain()
	}
	for _, id := range []ID{r.ServerID, r.DeviceID, r.InstanceID} {
		if id.Validate() != nil {
			return ExecutionRecoveryUncertain()
		}
	}
	return nil
}
func (e ExecutionRecoveryEvidence) validateRevertRecovery(r ExecutionRecoveryRequest) error {
	if r.Validate() != nil || e.Version != 2 || e.JobID != r.JobID || e.ReportID.Validate() != nil || e.Completion != (ExecutionCompletion{}) || e.Revert == nil || e.Revert.Validate() != nil || e.Revert.Version != 4 || e.Revert.Checkpoint.JobID != r.JobID || e.Revert.ActionID != r.Revert.Input.ActionID || e.Revert.ExecutionID != r.Revert.Input.Assignment.ExecutionID || e.Revert.Revert.NativeThreadID != r.Revert.Input.Completion.NativeThreadID {
		return ExecutionRecoveryUncertain()
	}
	a, _ := json.Marshal(e.Revert.Revert.Target)
	b, _ := json.Marshal(r.Revert.Input.Revert)
	if !bytes.Equal(a, b) {
		return ExecutionRecoveryUncertain()
	}
	return nil
}

func emptyRevertRecoveryJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
