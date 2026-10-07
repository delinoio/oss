package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ExecutionRecoveryRequest contains comparison facts only. It cannot authorize
// native input, replace the original assignment, or mint execution credentials.
type ExecutionRecoveryRequest struct {
	Startup               *PRStartupRecoveryReference `json:"startup,omitempty"`
	Version               uint32                      `json:"version"`
	Harness               Harness                     `json:"harness,omitempty"`
	OpenCode              *OpenCodeRecoveryReference  `json:"opencode,omitempty"`
	Claude                *ClaudeRecoveryReference    `json:"claude,omitempty"`
	ServerID              ID                          `json:"server_id"`
	DeviceID              ID                          `json:"device_id"`
	InstanceID            ID                          `json:"instance_id"`
	JobID                 ID                          `json:"job_id"`
	SessionID             ID                          `json:"session_id"`
	MachineID             ID                          `json:"machine_id"`
	AssignmentRevision    uint64                      `json:"assignment_revision"`
	AssignmentDigest      string                      `json:"assignment_digest"`
	AssignmentInputDigest string                      `json:"assignment_input_digest"`
	ConfigurationDigest   string                      `json:"configuration_digest"`
	AccountID             ID                          `json:"account_id"`
	ConnectionID          ID                          `json:"connection_id"`
	HistoryExecutionID    ID                          `json:"history_execution_id"`
	Completion            ExecutionCompletion         `json:"completion"`
	InputMode             SessionMode                 `json:"input_mode"`
	PromptDigest          string                      `json:"prompt_digest"`
	AcceptedInputs        []ExecutionInputBinding     `json:"accepted_inputs"`
	Preparation           json.RawMessage             `json:"preparation"`
	Manifest              json.RawMessage             `json:"manifest"`
}

func ExecutionRecoveryUncertain() *Error {
	return Fail(RecoveryRequired, "The retained execution cannot yet be reconciled.", "Preserve its original Worker records and published input/response evidence; do not resend input or resume before recovery succeeds.")
}

func (r ExecutionRecoveryRequest) Validate() error {
	if r.Startup != nil {
		return r.validatePRStartupRecovery()
	}
	if (r.NativeHarness() != Codex && r.NativeHarness() != OpenCode && r.NativeHarness() != ClaudeCode) || (r.NativeHarness() == OpenCode) != (r.OpenCode != nil) || (r.NativeHarness() == ClaudeCode) != (r.Claude != nil) || r.OpenCode != nil && r.OpenCode.Validate() != nil || r.Claude != nil && r.Claude.Validate() != nil {
		return ExecutionRecoveryUncertain()
	}
	if r.Version != 1 || r.AssignmentRevision == 0 || r.Completion.Version != 1 || r.Completion.ValidateForHarness(r.NativeHarness()) != nil || !r.InputMode.Valid() {
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
	bindings, err := CheckedExecutionInputs(r.Completion.InputID, r.PromptDigest, r.AcceptedInputs)
	if err != nil || r.OpenCode != nil && (len(bindings) != 1 || (r.OpenCode.ClaimVersion == 1) != (r.HistoryExecutionID == r.Completion.ExecutionID)) {
		return ExecutionRecoveryUncertain()
	}
	if r.Claude != nil && (len(bindings) != 1 || (r.Claude.ClaimVersion == 1) != (r.HistoryExecutionID == r.Completion.ExecutionID) || string(r.Completion.NativeThreadID) != string(r.SessionID) || (r.Completion.Outcome != ExecutionSucceeded && r.Completion.Outcome != ExecutionFailed)) {
		return ExecutionRecoveryUncertain()
	}
	for _, raw := range []json.RawMessage{r.Preparation, r.Manifest} {
		if len(raw) == 0 || len(raw) > 1<<20 || !json.Valid(raw) {
			return ExecutionRecoveryUncertain()
		}
	}
	return nil
}

// Claude inspection compares immutable launch settings without receiving the
// instruction body, prompt, answer, credential or authority for a new process.
type ClaudeRecoveryReference struct {
	Startup            *ExecutionStartupSelection `json:"startup,omitempty"`
	ClaimVersion       uint32                     `json:"claim_version"`
	Version            string                     `json:"version"`
	Executable         string                     `json:"executable"`
	Model              string                     `json:"model"`
	Effort             string                     `json:"effort"`
	Permission         ClaudePermissionMode       `json:"permission"`
	InstructionsDigest string                     `json:"instructions_sha256"`
	BindingRequestID   ID                         `json:"binding_request_id"`
	InputRequestID     ID                         `json:"input_request_id"`
}

func (r ClaudeRecoveryReference) Validate() error {
	if (r.ClaimVersion != 1 && r.ClaimVersion != 2) || (r.Version != "" && !ValidNativeVersionMetadata(r.Version)) || UniqueIDs([]ID{r.BindingRequestID, r.InputRequestID}) != nil || (r.Startup == nil && Text(r.Executable, "native executable", 4096, true) != nil || r.Startup != nil && (r.Startup.Validate(ClaudeCode) != nil || r.Startup.ExecutableSHA256 == "" || r.Executable != "")) || Text(r.Model, "native model", 1024, true) != nil || r.Effort != "" && !validClaudeEffort(r.Effort) || !r.Permission.Valid() {
		return ExecutionRecoveryUncertain()
	}
	digest, err := hex.DecodeString(r.InstructionsDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != r.InstructionsDigest {
		return ExecutionRecoveryUncertain()
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
	if expected.Validate() != nil || e.Version != 1 || e.JobID != expected.JobID || e.ReportID.Validate() != nil || e.Completion.Version != 2 || e.Completion.ValidateForHarness(expected.NativeHarness()) != nil || terminal != expected.Completion {
		return ExecutionRecoveryUncertain()
	}
	return nil
}

// Omitted harness is the historical Codex-only wire profile, never inferred
// from native ID spelling or a Worker-supplied completion.
func (r ExecutionRecoveryRequest) NativeHarness() Harness {
	if r.Harness == "" {
		return Codex
	}
	return r.Harness
}

// OpenCode keeps its original creation marker separately from the current
// first/resumed binding request and once-only native input request.
type OpenCodeRecoveryReference struct {
	ClaimVersion      uint32 `json:"claim_version"`
	CreationRequestID ID     `json:"creation_request_id"`
	BindingRequestID  ID     `json:"binding_request_id"`
	InputRequestID    ID     `json:"input_request_id"`
}

func (r OpenCodeRecoveryReference) Validate() error {
	if UniqueIDs([]ID{r.BindingRequestID, r.InputRequestID}) != nil || r.CreationRequestID.Validate() != nil || r.CreationRequestID == r.InputRequestID || (r.ClaimVersion != 1 && r.ClaimVersion != 2) || (r.ClaimVersion == 1) != (r.CreationRequestID == r.BindingRequestID) {
		return ExecutionRecoveryUncertain()
	}
	return nil
}
