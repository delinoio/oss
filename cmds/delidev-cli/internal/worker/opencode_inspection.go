package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// The caller holds the original publisher lock and independently inspected
// closed workspace lease. Reconstruct comparison authority from the accepted
// assignment and original claims, never from the checkpoint being inspected.
func inspectCompletedOpenCodeCheckpoint(ctx context.Context, root string, ref CompletedExecutionRef, completion domain.ExecutionCompletion, cwd string) error {
	c, native := ref.Checkpoint, ref.OpenCode
	if ref.Harness != domain.OpenCode || native == nil || native.Validate() != nil || ref.Preparation.Type != ref.Manifest.Type || len(ref.Manifest.Repositories) > 1 || len(c.WorkspaceRoots) != 0 {
		return executionCheckpointUncertain()
	}
	bindings, err := domain.CheckedExecutionInputs(c.Completion.InputID, hex.EncodeToString(c.PromptDigest[:]), c.AcceptedInputs)
	if err != nil || len(bindings) != 1 {
		return executionCheckpointUncertain()
	}
	claim := openCodeClaimReference{
		Version: native.ClaimVersion, JobID: c.JobID, InstanceID: ref.InstanceID, ServerID: ref.ServerID, DeviceID: ref.DeviceID,
		MachineID: c.MachineID, ExecutionID: c.Completion.ExecutionID, SessionID: c.SessionID, InputID: c.Completion.InputID,
		AccountID: c.AccountID, ConnectionID: c.ConnectionID, ThreadRequestID: native.BindingRequestID, InputRequestID: native.InputRequestID,
		Revision: ref.AssignmentRevision, AssignmentDigest: ref.AssignmentDigest, ConfigurationDigest: c.ConfigurationDigest,
	}
	claims, err := readOpenCodeClaims(root, claim)
	raw, encodeErr := json.Marshal(claims)
	if err != nil || encodeErr != nil || len(claims) < 2 {
		return executionCheckpointUncertain()
	}
	expected := openCodeCheckpointReference{
		Claim: claim, Completion: c.Completion, InputMode: c.InputMode, PromptSHA256: hex.EncodeToString(c.PromptDigest[:]),
		ClaimsSHA256: executionInputDigest(raw), AssignmentInputSHA256: c.AssignmentInputDigest,
		HistoryExecutionID: c.HistoryExecutionID, CreationRequestID: native.CreationRequestID,
	}
	saved, err := readOpenCodeExecutionCheckpoint(ctx, root, expected, completion.NativeCheckpointDigest)
	if err != nil || saved.Version != 2 || claims[1].SessionID != saved.NativeReference.SessionID || claims[1].MessageID != saved.NativeReference.InputID || claims[1].PartID != saved.NativeReference.PartID || claims[1].RequestID != saved.NativeReference.InputRequestID {
		return executionCheckpointUncertain()
	}
	nativeRoot, err := openCodeWorkspaceRoot(ref.Manifest, cwd)
	if err != nil {
		return err
	}
	home := filepath.Join(root, "runtimes", string(completion.ExecutionID))
	return opencode.InspectReplacementWorkspace(ctx, home, saved.Native, saved.NativeReference, cwd, nativeRoot)
}
