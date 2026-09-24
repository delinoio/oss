package worker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// CodexExecutionCheckpoint is Worker-private continuation evidence, not an RPC
// document or execution grant. In particular its effective native directory and
// sandbox roots must never be promoted to a public response or ordinary log.
// A coordinator still has to prove current authority, terminal publication and
// cleanup independently; this file cannot clear a lost completion report.
type CodexExecutionCheckpoint struct {
	Version               uint32                       `json:"version"`
	JobID                 domain.ID                    `json:"job_id"`
	SessionID             domain.ID                    `json:"session_id"`
	MachineID             domain.ID                    `json:"machine_id"`
	HistoryExecutionID    domain.ID                    `json:"history_execution_id"`
	AssignmentInputDigest string                       `json:"assignment_input_digest"`
	ConfigurationDigest   string                       `json:"configuration_digest"`
	AccountID             domain.ID                    `json:"account_id"`
	ConnectionID          domain.ID                    `json:"connection_id"`
	Completion            domain.ExecutionCompletion   `json:"completion"`
	Native                codex.ContinuationCheckpoint `json:"native"`
}

// ExecutionCheckpointRef comes from the exact preceding immutable assignment
// and its accepted completion. It intentionally contains no prompt or token.
type ExecutionCheckpointRef struct {
	JobID                 domain.ID
	SessionID             domain.ID
	MachineID             domain.ID
	HistoryExecutionID    domain.ID
	AssignmentInputDigest string
	ConfigurationDigest   string
	AccountID             domain.ID
	ConnectionID          domain.ID
	Completion            domain.ExecutionCompletion
	InputMode             domain.SessionMode
	PromptDigest          [sha256.Size]byte
	AcceptedInputs        []domain.ExecutionInputBinding
}

const maxExecutionCheckpointBytes = 1 << 20

func executionCheckpointUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The retained native runtime checkpoint does not match its preceding execution.", "Preserve the original Worker runtime, assignment and cleanup report; never reconstruct missing history, replace its ownership or resend prior input.")
}

func executionInputDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (r ExecutionCheckpointRef) validate() error {
	for _, id := range []domain.ID{r.JobID, r.SessionID, r.MachineID, r.HistoryExecutionID, r.AccountID, r.ConnectionID} {
		if id.Validate() != nil {
			return executionCheckpointUncertain()
		}
	}
	for _, value := range []string{r.AssignmentInputDigest, r.ConfigurationDigest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return executionCheckpointUncertain()
		}
	}
	if r.Completion.Validate() != nil || !r.InputMode.Valid() {
		return executionCheckpointUncertain()
	}
	if _, err := r.nativeInputs(); err != nil {
		return err
	}
	return nil
}

func (r ExecutionCheckpointRef) nativeInputs() ([]codex.HistoricalInput, error) {
	bindings, err := domain.CheckedExecutionInputs(r.Completion.InputID, hex.EncodeToString(r.PromptDigest[:]), r.AcceptedInputs)
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	inputs := make([]codex.HistoricalInput, len(bindings))
	for i, binding := range bindings {
		digest, _ := hex.DecodeString(binding.PromptDigest)
		inputs[i].ID = binding.InputID
		copy(inputs[i].PromptDigest[:], digest)
	}
	return inputs, nil
}

func (p CodexExecutionCheckpoint) matches(ref ExecutionCheckpointRef) bool {
	// The file holds the version-1 terminal facts; the version-2 server report
	// adds the file digest after synchronization, avoiding a self-referential
	// hash while binding every byte of the original effective native settings.
	terminal := ref.Completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	inputs, err := ref.nativeInputs()
	if err != nil || ref.validate() != nil || p.Version != 1 || p.JobID != ref.JobID || p.SessionID != ref.SessionID || p.MachineID != ref.MachineID || p.HistoryExecutionID != ref.HistoryExecutionID || p.AssignmentInputDigest != ref.AssignmentInputDigest || p.ConfigurationDigest != ref.ConfigurationDigest || p.AccountID != ref.AccountID || p.ConnectionID != ref.ConnectionID || p.Completion != terminal || p.Native.ThreadID != ref.Completion.NativeThreadID || p.Native.SessionID != p.Native.ThreadID || p.Native.TurnID != ref.Completion.NativeTurnID || p.Native.Mode != ref.InputMode || !slices.Equal(p.Native.Inputs, inputs) {
		return false
	}
	status := map[domain.ExecutionOutcome]codex.TurnStatus{domain.ExecutionSucceeded: codex.TurnCompleted, domain.ExecutionFailed: codex.TurnFailed, domain.ExecutionStopped: codex.TurnInterrupted}[ref.Completion.Outcome]
	if p.Native.Status != status || p.Native.Effective.Provider != codex.APIProvider || p.Native.Effective.ApprovalsReviewer != "user" || domain.Text(p.Native.Effective.Model, "retained model", 256, true) != nil || !filepath.IsAbs(p.Native.Effective.Cwd) {
		return false
	}
	for _, value := range []*string{p.Native.Effective.Effort, p.Native.Effective.ServiceTier} {
		if value != nil && domain.Text(*value, "retained native option", 256, true) != nil {
			return false
		}
	}
	switch p.Native.Effective.ApprovalPolicy {
	case codex.ApprovalUntrusted, codex.ApprovalOnRequest, codex.ApprovalNever:
	default:
		return false
	}
	switch p.Native.Effective.Sandbox.Type {
	case codex.ReadOnly, codex.WorkspaceWrite, codex.FullAccess:
	default:
		return false
	}
	// Exact effective roots and input digests are rechecked against the resumed
	// native connection before a new send. This reader grants no native authority.
	return true
}

func executionCheckpointPath(root string, execution domain.ID) (string, error) {
	if execution.Validate() != nil || !filepath.IsAbs(root) {
		return "", executionCheckpointUncertain()
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", executionCheckpointUncertain()
	}
	for _, path := range []string{root, filepath.Join(root, "runtimes"), filepath.Join(root, "runtimes", string(execution))} {
		if security.CheckPrivateDir(path) != nil {
			return "", executionCheckpointUncertain()
		}
	}
	return filepath.Join(root, "runtimes", string(execution), "native-completion.json"), nil
}

// ReadCodexExecutionCheckpoint never creates directories, upgrades a missing
// legacy checkpoint or infers proof from native history alone. Call it only
// while holding the session's exclusive continuation workspace lease.
func ReadCodexExecutionCheckpoint(root string, ref ExecutionCheckpointRef) (CodexExecutionCheckpoint, error) {
	var checkpoint CodexExecutionCheckpoint
	if ref.validate() != nil || ref.Completion.Version != 2 {
		return checkpoint, executionCheckpointUncertain()
	}
	path, err := executionCheckpointPath(root, ref.Completion.ExecutionID)
	if err != nil {
		return checkpoint, err
	}
	raw, err := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != ref.Completion.NativeCheckpointDigest || domain.Decode(raw, &checkpoint) != nil || !checkpoint.matches(ref) {
		return CodexExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	// The only producer writes this exact representation. Reject partial fixed
	// arrays and other JSON forms that Go would otherwise normalize silently;
	// retained checkpoints are immutable machine evidence, not editable config.
	canonical, err := json.Marshal(checkpoint)
	if err != nil || !bytes.Equal(raw, canonical) {
		return CodexExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	// The history root is derived solely from a retained execution identity,
	// never from a caller-provided path. Every component must still be private.
	if _, err := executionCheckpointPath(root, ref.HistoryExecutionID); err != nil {
		return CodexExecutionCheckpoint{}, err
	}
	if err := security.CheckPrivateDir(filepath.Join(root, "runtimes", string(ref.HistoryExecutionID), "codex")); err != nil {
		return CodexExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	return checkpoint, nil
}

func retainCodexCompletion(root string, jobID domain.ID, job domain.Job, input domain.ExecutionJobInput, bound codex.ThreadResult, completion domain.ExecutionCompletion, acceptedInputs []domain.ExecutionInputBinding) (string, error) {
	var accepted domain.ExecutionJobInput
	if domain.Decode(job.Input, &accepted) != nil || input.Validate() != nil || completion.Validate() != nil || completion.Version != 1 || bound.Thread == nil || bound.Effective == nil || bound.RequestID != input.ThreadRequestID || bound.Thread.ID != completion.NativeThreadID || completion.ExecutionID != input.ExecutionID || completion.InputID != input.InputID || job.Type != domain.ExecuteSessionJob || job.MachineID != input.MachineID {
		return "", executionCheckpointUncertain()
	}
	actual, err := json.Marshal(input)
	if err != nil {
		return "", executionCheckpointUncertain()
	}
	expected, err := json.Marshal(accepted)
	if err != nil || !bytes.Equal(actual, expected) {
		return "", executionCheckpointUncertain()
	}
	ref := ExecutionCheckpointRef{JobID: jobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: input.ExecutionID, AssignmentInputDigest: executionInputDigest(job.Input), ConfigurationDigest: input.ConfigurationDigest, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Completion: completion, InputMode: input.Input.Mode, PromptDigest: sha256.Sum256([]byte(input.Input.Prompt))}
	ref.AcceptedInputs = acceptedInputs
	nativeInputs, err := ref.nativeInputs()
	if err != nil {
		return "", err
	}
	if input.Continuation != nil {
		ref.HistoryExecutionID = input.Continuation.HistoryExecutionID
	}
	status := map[domain.ExecutionOutcome]codex.TurnStatus{domain.ExecutionSucceeded: codex.TurnCompleted, domain.ExecutionFailed: codex.TurnFailed, domain.ExecutionStopped: codex.TurnInterrupted}[completion.Outcome]
	checkpoint := CodexExecutionCheckpoint{Version: 1, JobID: jobID, SessionID: ref.SessionID, MachineID: ref.MachineID, HistoryExecutionID: ref.HistoryExecutionID, AssignmentInputDigest: ref.AssignmentInputDigest, ConfigurationDigest: ref.ConfigurationDigest, AccountID: ref.AccountID, ConnectionID: ref.ConnectionID, Completion: completion, Native: codex.ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: completion.NativeTurnID, Status: status, Mode: input.Input.Mode, Inputs: nativeInputs, Effective: *bound.Effective}}
	if !checkpoint.matches(ref) {
		return "", executionCheckpointUncertain()
	}
	path, err := executionCheckpointPath(root, input.ExecutionID)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil || len(raw) > maxExecutionCheckpointBytes {
		return "", executionCheckpointUncertain()
	}
	old, err := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return "", executionCheckpointUncertain()
		}
		return executionInputDigest(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", executionCheckpointUncertain()
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		return "", executionCheckpointUncertain()
	}
	return executionInputDigest(raw), nil
}
