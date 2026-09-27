package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// This private file joins the native adapter snapshot to immutable Worker and
// account ownership. It is never an RPC payload or a replacement execution
// grant. The public Claude runner/publication path is a separate integration.
type claudeExecutionCheckpoint struct {
	Version               uint32                     `json:"version"`
	JobID                 domain.ID                  `json:"job_id"`
	SessionID             domain.ID                  `json:"session_id"`
	MachineID             domain.ID                  `json:"machine_id"`
	HistoryExecutionID    domain.ID                  `json:"history_execution_id"`
	AssignmentInputDigest string                     `json:"assignment_input_digest"`
	ConfigurationDigest   string                     `json:"configuration_digest"`
	AccountID             domain.ID                  `json:"account_id"`
	ConnectionID          domain.ID                  `json:"connection_id"`
	Completion            domain.ExecutionCompletion `json:"completion"`
	WorkspaceRoots        []string                   `json:"workspace_roots"`
	NativeReference       claude.CheckpointReference `json:"native_reference"`
	Native                json.RawMessage            `json:"native"`
}

const maxClaudeExecutionCheckpointBytes = 9 << 20

func claudeCheckpointOutcome(closed *claude.ClosedAPISession) (domain.ExecutionOutcome, error) {
	r, err := closed.OriginalResult()
	if err != nil {
		return "", err
	}
	if r.Successful() {
		return domain.ExecutionSucceeded, nil
	}
	if r.Kind == claude.ResultSuccess && !r.Error && (r.Reason == claude.AbortedStreaming || r.Reason == claude.AbortedTools) {
		return domain.ExecutionStopped, nil
	}
	return domain.ExecutionFailed, nil
}

func (p claudeExecutionCheckpoint) matches(ref ExecutionCheckpointRef) bool {
	terminal := ref.Completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	native := p.NativeReference
	inputs, err := domain.CheckedExecutionInputs(ref.Completion.InputID, hex.EncodeToString(ref.PromptDigest[:]), ref.AcceptedInputs)
	// Claude's current native checkpoint has root input boundaries, not Codex
	// same-turn Steer or independently proved multi-root native authority.
	if err != nil || len(inputs) != 1 || len(ref.WorkspaceRoots) > 1 || ref.validateForHarness(domain.ClaudeCode) != nil || p.Version != 1 || p.JobID != ref.JobID || p.SessionID != ref.SessionID || p.MachineID != ref.MachineID || p.HistoryExecutionID != ref.HistoryExecutionID || p.AssignmentInputDigest != ref.AssignmentInputDigest || p.ConfigurationDigest != ref.ConfigurationDigest || p.AccountID != ref.AccountID || p.ConnectionID != ref.ConnectionID || p.Completion != terminal || !slices.Equal(p.WorkspaceRoots, ref.WorkspaceRoots) || native.SessionID != ref.SessionID || native.OwnerID != ref.JobID || native.InputID != ref.Completion.InputID || native.InputSHA256 != inputs[0].PromptDigest || native.NativeTurnID != string(ref.Completion.NativeTurnID) || string(native.SessionID) != string(ref.Completion.NativeThreadID) || native.SHA256 != executionInputDigest(p.Native) || len(p.Native) == 0 || len(p.Native) > 8<<20 || !json.Valid(p.Native) || (ref.Completion.Outcome != domain.ExecutionSucceeded && !native.RequiresResume) {
		return false
	}
	return true
}

// retainClaudeCompletion requires the caller's original acknowledged terminal
// publication and completed workspace lease. The closed native capability
// independently proves its settled process, original input and retained files.
// An uncertain write preserves recovery; never restart the original input.
func retainClaudeCompletion(ctx context.Context, root string, jobID domain.ID, job domain.Job, input domain.ExecutionJobInput, completion domain.ExecutionCompletion, closed *claude.ClosedAPISession) (string, error) {
	var accepted domain.ExecutionJobInput
	if domain.Decode(job.Input, &accepted) != nil || input.Validate() != nil || input.Configuration.Harness != domain.ClaudeCode || input.Installation.Version != claude.SupportedVersion || completion.ValidateForHarness(domain.ClaudeCode) != nil || completion.Version != 1 || completion.ExecutionID != input.ExecutionID || completion.InputID != input.InputID || job.Type != domain.ExecuteSessionJob || job.MachineID != input.MachineID {
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
	var manifest workspace.Manifest
	if domain.Decode(input.Manifest, &manifest) != nil {
		return "", executionCheckpointUncertain()
	}
	ref := ExecutionCheckpointRef{JobID: jobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: input.ExecutionID, AssignmentInputDigest: executionInputDigest(job.Input), ConfigurationDigest: input.ConfigurationDigest, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Completion: completion, InputMode: input.Input.Mode, WorkspaceRoots: nativeWorkspaceRoots(manifest)}
	digest := executionInputDigest([]byte(input.Input.Prompt))
	decoded, _ := hex.DecodeString(digest)
	copy(ref.PromptDigest[:], decoded)
	if input.Continuation != nil {
		ref.HistoryExecutionID = input.Continuation.HistoryExecutionID
	}
	if ref.validateForHarness(domain.ClaudeCode) != nil || len(ref.WorkspaceRoots) > 1 {
		return "", executionCheckpointUncertain()
	}
	path, err := executionCheckpointPath(root, input.ExecutionID)
	if err != nil {
		return "", err
	}
	outcome, err := claudeCheckpointOutcome(closed)
	if err != nil || outcome != completion.Outcome {
		return "", executionCheckpointUncertain()
	}
	native, nativeRef, err := closed.RetainCheckpoint(ctx)
	if err != nil {
		return "", err
	}
	p := claudeExecutionCheckpoint{Version: 1, JobID: jobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: ref.HistoryExecutionID, AssignmentInputDigest: ref.AssignmentInputDigest, ConfigurationDigest: ref.ConfigurationDigest, AccountID: ref.AccountID, ConnectionID: ref.ConnectionID, Completion: completion, WorkspaceRoots: ref.WorkspaceRoots, NativeReference: nativeRef, Native: native}
	if !p.matches(ref) {
		return "", executionCheckpointUncertain()
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > maxClaudeExecutionCheckpointBytes {
		return "", executionCheckpointUncertain()
	}
	old, err := security.ReadPrivate(path, maxClaudeExecutionCheckpointBytes)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return "", executionCheckpointUncertain()
		}
		return executionInputDigest(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) || ctx.Err() != nil {
		return "", executionCheckpointUncertain()
	}
	if security.WriteAtomic(path, raw) != nil {
		return "", executionCheckpointUncertain()
	}
	return executionInputDigest(raw), nil
}

// ReadClaudeExecutionCheckpoint needs the independently accepted preceding
// completion/digest and current exclusive continuation lease. config is derived
// from the immutable assignment, original history runtime and paired server,
// never from the checkpoint. No directories are created and no native work is
// launched. Current account/Worker readiness and the new durable claim remain
// mandatory before the caller invokes ContinueAPISession on the result.
func ReadClaudeExecutionCheckpoint(ctx context.Context, root string, ref ExecutionCheckpointRef, config claude.APIStreamConfig) (*claude.ClosedAPISession, error) {
	p, err := readClaudeExecutionCheckpoint(root, ref)
	if err != nil {
		return nil, err
	}
	historyRoot := filepath.Join(root, "runtimes", string(ref.HistoryExecutionID))
	if config.Version != claude.SupportedVersion || config.SessionID != ref.SessionID || config.Process.OwnerID != ref.JobID || config.Process.Directory != filepath.Join(root, "processes") || config.Process.Cwd != historyRoot || config.Home != filepath.Join(historyRoot, "claude") {
		return nil, executionCheckpointUncertain()
	}
	closed, err := claude.RestoreCheckpoint(ctx, config, p.Native, p.NativeReference)
	if err != nil {
		return nil, err
	}
	outcome, err := claudeCheckpointOutcome(closed)
	if err != nil || outcome != ref.Completion.Outcome {
		return nil, executionCheckpointUncertain()
	}
	return closed, nil
}

func readClaudeExecutionCheckpoint(root string, ref ExecutionCheckpointRef) (claudeExecutionCheckpoint, error) {
	var p claudeExecutionCheckpoint
	if ref.validateForHarness(domain.ClaudeCode) != nil || ref.Completion.Version != 2 {
		return p, executionCheckpointUncertain()
	}
	path, err := executionCheckpointPath(root, ref.Completion.ExecutionID)
	if err != nil {
		return p, err
	}
	raw, err := security.ReadPrivate(path, maxClaudeExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != ref.Completion.NativeCheckpointDigest {
		return p, executionCheckpointUncertain()
	}
	if json.Unmarshal(raw, &p) != nil || !p.matches(ref) {
		return p, executionCheckpointUncertain()
	}
	canonical, err := json.Marshal(p)
	if err != nil || !bytes.Equal(raw, canonical) {
		return p, executionCheckpointUncertain()
	}
	if _, err := executionCheckpointPath(root, ref.HistoryExecutionID); err != nil {
		return p, err
	}
	return p, nil
}
