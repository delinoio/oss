package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ExecutionRecoveryRequest contains comparison facts only. It cannot authorize
// native input, replace the original assignment, or mint execution credentials.
type ExecutionRecoveryRequest struct {
	Version               uint32                  `json:"version"`
	ServerID              ID                      `json:"server_id"`
	DeviceID              ID                      `json:"device_id"`
	InstanceID            ID                      `json:"instance_id"`
	JobID                 ID                      `json:"job_id"`
	SessionID             ID                      `json:"session_id"`
	MachineID             ID                      `json:"machine_id"`
	AssignmentRevision    uint64                  `json:"assignment_revision"`
	AssignmentDigest      string                  `json:"assignment_digest"`
	AssignmentInputDigest string                  `json:"assignment_input_digest"`
	ConfigurationDigest   string                  `json:"configuration_digest"`
	AccountID             ID                      `json:"account_id"`
	ConnectionID          ID                      `json:"connection_id"`
	HistoryExecutionID    ID                      `json:"history_execution_id"`
	Completion            ExecutionCompletion     `json:"completion"`
	InputMode             SessionMode             `json:"input_mode"`
	PromptDigest          string                  `json:"prompt_digest"`
	AcceptedInputs        []ExecutionInputBinding `json:"accepted_inputs"`
	Preparation           json.RawMessage         `json:"preparation"`
	Manifest              json.RawMessage         `json:"manifest"`
}

func ExecutionRecoveryUncertain() *Error {
	return Fail(RecoveryRequired, "The retained execution cannot yet be reconciled.", "Preserve its original Worker records and published input/response evidence; do not resend input or resume before recovery succeeds.")
}

func (r ExecutionRecoveryRequest) Validate() error {
	if r.Version != 1 || r.AssignmentRevision == 0 || r.Completion.Version != 1 || r.Completion.Validate() != nil || !r.InputMode.Valid() {
		return ExecutionRecoveryUncertain()
	}
	for _, id := range []ID{r.ServerID, r.DeviceID, r.InstanceID, r.JobID, r.SessionID, r.MachineID, r.AccountID, r.ConnectionID, r.HistoryExecutionID} {
		if id.Validate() != nil {
			return ExecutionRecoveryUncertain()
		}
	}
	for _, value := range []string{r.AssignmentDigest, r.AssignmentInputDigest, r.ConfigurationDigest, r.PromptDigest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return ExecutionRecoveryUncertain()
		}
	}
	if _, err := CheckedExecutionInputs(r.Completion.InputID, r.PromptDigest, r.AcceptedInputs); err != nil {
		return ExecutionRecoveryUncertain()
	}
	for _, raw := range []json.RawMessage{r.Preparation, r.Manifest} {
		if len(raw) == 0 || len(raw) > 1<<20 || !json.Valid(raw) {
			return ExecutionRecoveryUncertain()
		}
	}
	return nil
}

// ExecutionRecoveryEvidence binds the original durable report, without granting
// that old Worker instance any new publication or reporting authority.
type ExecutionRecoveryEvidence struct {
	Version    uint32              `json:"version"`
	JobID      ID                  `json:"job_id"`
	ReportID   ID                  `json:"report_id"`
	Completion ExecutionCompletion `json:"completion"`
}

func (e ExecutionRecoveryEvidence) Validate(expected ExecutionRecoveryRequest) error {
	terminal := e.Completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	if expected.Validate() != nil || e.Version != 1 || e.JobID != expected.JobID || e.ReportID.Validate() != nil || e.Completion.Version != 2 || e.Completion.Validate() != nil || terminal != expected.Completion {
		return ExecutionRecoveryUncertain()
	}
	return nil
}
