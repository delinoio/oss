package worker

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// The caller already holds the exact predecessor's continuation workspace
// lease. Server-accepted version-2 completion supplies the independent digest;
// neither local history nor a finished-but-unaccepted report can create it.
func readOpenCodeContinuationCheckpoint(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput) (openCodeExecutionCheckpoint, error) {
	var empty openCodeExecutionCheckpoint
	c := input.Continuation
	if c == nil || input.Validate() != nil || input.Configuration.Harness != domain.OpenCode ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", credential.MachineID != input.MachineID) ||
		c.Completion.Version != 2 {
		return empty, executionCheckpointUncertain()
	}
	bindings, err := domain.CheckedExecutionInputs(c.Previous.InputID, c.PromptDigest, c.Previous.AcceptedInputs)
	if err != nil || len(bindings) != 1 {
		return empty, executionCheckpointUncertain()
	}
	path, err := openCodeCheckpointPath(root, c.Previous.JobID)
	if err != nil {
		return empty, err
	}
	raw, err := security.ReadPrivate(path, maxOpenCodeExecutionCheckpointBytes)
	var saved openCodeExecutionCheckpoint
	if err != nil || executionInputDigest(raw) != c.Completion.NativeCheckpointDigest || domain.DecodeWithLimit(raw, &saved, maxOpenCodeExecutionCheckpointBytes) != nil || saved.Version != 2 {
		return empty, executionCheckpointUncertain()
	}
	terminal := c.Completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	r := saved.Reference
	ref := r.Claim
	if r.Completion != terminal || r.InputMode != c.InputMode || r.PromptSHA256 != c.PromptDigest || r.AssignmentInputSHA256 != c.AssignmentInputDigest || r.HistoryExecutionID != c.HistoryExecutionID ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", ref.ServerID != credential.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", ref.DeviceID != credential.DeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", ref.MachineID != input.MachineID) ||
		ref.SessionID != input.SessionID || ref.ExecutionID != c.Previous.ExecutionID || ref.JobID != c.Previous.JobID || ref.InputID != c.Previous.InputID ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", ref.AccountID != input.AccountID) ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", ref.ConnectionID != input.ConnectionID) ||
		ref.ConfigurationDigest != input.ConfigurationDigest || ref.InputRequestID == input.TurnRequestID || ref.ThreadRequestID == input.ThreadRequestID {
		return empty, executionCheckpointUncertain()
	}
	checkpoint, err := readOpenCodeExecutionCheckpoint(ctx, root, r, c.Completion.NativeCheckpointDigest)
	if err != nil {
		return empty, err
	}
	claims, err := readOpenCodeClaims(root, ref)
	claimBytes, encodeErr := json.Marshal(claims)
	if err != nil || encodeErr != nil || executionInputDigest(claimBytes) != r.ClaimsSHA256 || len(claims) < 2 || claims[1].SessionID != checkpoint.NativeReference.SessionID || claims[1].MessageID != checkpoint.NativeReference.InputID || claims[1].PartID != checkpoint.NativeReference.PartID || claims[1].RequestID != checkpoint.NativeReference.InputRequestID {
		return empty, executionCheckpointUncertain()
	}
	if err := verifyOpenCodeContinuationJournals(root, ref, c.Completion); err != nil {
		return empty, err
	}
	home := filepath.Join(root, "runtimes", string(ref.ExecutionID))
	if err := opencode.InspectReplacementCheckpoint(ctx, home, checkpoint.Native, checkpoint.NativeReference); err != nil {
		return empty, err
	}
	return checkpoint, nil
}

func verifyOpenCodeContinuationJournals(root string, ref openCodeClaimReference, completion domain.ExecutionCompletion) error {
	if ref.validate() != nil || completion.ValidateForHarness(domain.OpenCode) != nil || completion.Version != 2 || completion.ExecutionID != ref.ExecutionID || completion.InputID != ref.InputID {
		return executionCheckpointUncertain()
	}
	// Compare original immutable report/outbox authority without reopening its
	// writers. Finished may advance to Reported after a lost HTTP acknowledgment;
	// only the already accepted identical report can authorize this new job.
	raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(ref.JobID)+".json"), 2<<20)
	var operation journal
	var done domain.ExecutionCompletion
	if err != nil || domain.Decode(raw, &operation) != nil || operation.Version != 1 || operation.JobID != ref.JobID ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", operation.InstanceID != ref.InstanceID) ||
		operation.Revision != ref.Revision || operation.Digest != ref.AssignmentDigest || (operation.State != journalFinished && operation.State != journalReported) || operation.Problem != nil || operation.ReportID.Validate() != nil || domain.Decode(operation.Output, &done) != nil || done != completion {
		return executionCheckpointUncertain()
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "jobs", string(ref.JobID), "publication.json"), 1<<20)
	var publication publicationJournal
	if err != nil || domain.Decode(raw, &publication) != nil || publication.Version != 1 || publication.JobID != ref.JobID ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", publication.InstanceID != ref.InstanceID) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", publication.ServerID != ref.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", publication.DeviceID != ref.DeviceID) ||
		publication.Revision != ref.Revision || publication.AssignmentDigest != ref.AssignmentDigest || publication.Pending != nil || publication.LastSequence != completion.LastSequence {
		return executionCheckpointUncertain()
	}
	return nil
}
