package worker

import (
	"context"
	"encoding/hex"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Called under the original publication and closed process/workspace locks.
// Comparison settings come from the server's immutable assignment, while the
// paired origin comes from this Worker's private credential scope. No token or
// instruction body reaches the native inspector and no process is launched.
func inspectCompletedClaudeCheckpoint(ctx context.Context, root string, ref CompletedExecutionRef, completion domain.ExecutionCompletion, cwd string) error {
	c, native := ref.Checkpoint, ref.Claude
	if ref.Harness != domain.ClaudeCode || native == nil || native.Validate() != nil || completion.Outcome != domain.ExecutionSucceeded || string(completion.NativeThreadID) != string(c.SessionID) || (native.ClaimVersion == 1) != (c.HistoryExecutionID == completion.ExecutionID) || ref.Preparation.Type != ref.Manifest.Type || len(c.WorkspaceRoots) != 0 {
		return executionCheckpointUncertain()
	}
	bindings, err := domain.CheckedExecutionInputs(c.Completion.InputID, hex.EncodeToString(c.PromptDigest[:]), c.AcceptedInputs)
	if err != nil || len(bindings) != 1 {
		return executionCheckpointUncertain()
	}
	credential, err := LoadCredential(root)
	if err != nil || credential.Type != domain.WorkerDevice || credential.ServerID != ref.ServerID || credential.DeviceID != ref.DeviceID || credential.MachineID != c.MachineID {
		return executionCheckpointUncertain()
	}
	path, err := claudeBindingPath(root, c.JobID)
	if err != nil {
		return err
	}
	raw, err := security.ReadPrivate(path, 16<<10)
	var claim claudeBindingJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.StopClaim != nil || !claim.InputClaimed || claim.JobID != c.JobID || claim.InstanceID != ref.InstanceID || claim.ServerID != ref.ServerID || claim.DeviceID != ref.DeviceID || claim.MachineID != c.MachineID || claim.ExecutionID != completion.ExecutionID || claim.SessionID != c.SessionID || claim.InputID != completion.InputID || claim.AccountID != c.AccountID || claim.ConnectionID != c.ConnectionID || claim.ThreadRequestID != native.BindingRequestID || claim.InputRequestID != native.InputRequestID || claim.Revision != ref.AssignmentRevision || claim.AssignmentDigest != ref.AssignmentDigest || claim.ConfigurationDigest != c.ConfigurationDigest {
		return executionCheckpointUncertain()
	}
	c.Completion = completion
	c.WorkspaceRoots = nativeWorkspaceRoots(ref.Manifest)
	saved, err := readClaudeExecutionCheckpoint(root, c)
	if err != nil || saved.NativeReference.RequiresResume {
		return executionCheckpointUncertain()
	}
	history := filepath.Join(root, "runtimes", string(c.HistoryExecutionID))
	config := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: c.JobID, Executable: native.Executable, Cwd: history}, Version: native.Version, Home: filepath.Join(history, "claude"), Workspace: cwd, WorkspaceRoots: c.WorkspaceRoots, SessionID: c.SessionID, Model: native.Model, Permission: claude.NativePermission(native.Permission), Effort: claude.NativeEffort(native.Effort), API: claude.APIConfig{ServerOrigin: credential.Endpoint}}
	return claude.InspectCheckpoint(ctx, config, native.InstructionsDigest, saved.Native, saved.NativeReference)
}
