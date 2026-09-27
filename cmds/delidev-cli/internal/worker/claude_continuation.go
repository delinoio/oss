package worker

import (
	"context"
	"encoding/hex"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func readClaudeContinuation(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, manifest workspace.Manifest, config claude.APIStreamConfig) (*claude.ClosedAPISession, error) {
	c := input.Continuation
	if c == nil || input.Validate() != nil {
		return nil, executionCheckpointUncertain()
	}
	if err := verifyClaudeContinuationJournals(root, credential, input); err != nil {
		return nil, err
	}
	ref := ExecutionCheckpointRef{JobID: c.Previous.JobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: c.HistoryExecutionID, AssignmentInputDigest: c.AssignmentInputDigest, ConfigurationDigest: input.ConfigurationDigest, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Completion: c.Completion, InputMode: c.InputMode, AcceptedInputs: c.Previous.AcceptedInputs, WorkspaceRoots: nativeWorkspaceRoots(manifest)}
	digest, err := hex.DecodeString(c.PromptDigest)
	if err != nil || len(digest) != len(ref.PromptDigest) {
		return nil, executionCheckpointUncertain()
	}
	copy(ref.PromptDigest[:], digest)
	config.Process.OwnerID = c.Previous.JobID
	config.Process.Cwd = filepath.Join(root, "runtimes", string(c.HistoryExecutionID))
	config.Home = filepath.Join(config.Process.Cwd, "claude")
	return ReadClaudeExecutionCheckpoint(ctx, root, ref, config)
}

// RetainCompletion runs only after Complete and the original workspace lease
// have joined. Unsupported histories keep their truthful v1 cleanup report;
// a failure while verifying an eligible history instead retains uncertainty.
func (c *ClaudeContentPublisher) RetainCompletion(ctx context.Context, api *claude.APISession, completion domain.ExecutionCompletion) (domain.ExecutionCompletion, error) {
	if c == nil || c.binding == nil || api == nil {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.verify() != nil || c.completion == nil || *c.completion != completion || b.stage != claudeTerminalPublished || c.pending || len(c.queue) != 0 {
		return domain.ExecutionCompletion{}, publicationUncertain()
	}
	if c.checkpoint != nil {
		return *c.checkpoint, nil
	}
	if completion.Outcome != domain.ExecutionSucceeded || c.terminal == nil || c.stop != nil || c.denial != nil || c.interruption != nil || len(c.messages) == 0 {
		return completion, nil
	}
	for _, tool := range c.tools {
		if tool.state != domain.MessageComplete || tool.historyKind == "" {
			return completion, nil
		}
	}
	for _, interaction := range c.interactions {
		if !interaction.closed || (interaction.continuation != domain.ClaudeAnswersProcessed && interaction.continuation != domain.ClaudeToolProcessed) {
			return completion, nil
		}
	}
	closed, err := api.RetainOriginalCompletion(ctx, b.journal.JobID, b.journal.SessionID, b.journal.InputID, b.turn)
	if err != nil {
		return domain.ExecutionCompletion{}, err
	}
	var job domain.Job
	if domain.Decode(b.publisher.config.Assignment.DocumentJson, &job) != nil {
		return domain.ExecutionCompletion{}, executionCheckpointUncertain()
	}
	digest, err := retainClaudeCompletion(ctx, b.publisher.config.Root, b.journal.JobID, job, b.publisher.input, completion, closed)
	if err != nil {
		return domain.ExecutionCompletion{}, err
	}
	completion.Version, completion.NativeCheckpointDigest = 2, digest
	c.checkpoint = &completion
	if logger := b.publisher.config.Logger; logger != nil {
		logger.InfoContext(ctx, "claude_original_checkpoint_retained", "job_id", b.journal.JobID, "execution_id", b.journal.ExecutionID)
	}
	return completion, nil
}

// Original journals remain observation-only. A lost report acknowledgment may
// leave Finished instead of Reported, but its exact accepted output must agree.
func verifyClaudeContinuationJournals(root string, credential Credential, input domain.ExecutionJobInput) error {
	c := input.Continuation
	if c == nil || input.Validate() != nil || credential.MachineID != input.MachineID {
		return executionCheckpointUncertain()
	}
	path, err := claudeBindingPath(root, c.Previous.JobID)
	if err != nil {
		return err
	}
	raw, err := security.ReadPrivate(path, 16<<10)
	var claim claudeBindingJournal
	if err != nil || domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.StopClaim != nil || !claim.InputClaimed || claim.JobID != c.Previous.JobID || claim.ExecutionID != c.Previous.ExecutionID || claim.InputID != c.Previous.InputID || claim.SessionID != input.SessionID || claim.MachineID != input.MachineID || claim.ServerID != credential.ServerID || claim.DeviceID != credential.DeviceID || claim.AccountID != input.AccountID || claim.ConnectionID != input.ConnectionID || claim.ConfigurationDigest != input.ConfigurationDigest || claim.InstanceID.Validate() != nil || claim.ThreadRequestID.Validate() != nil || claim.InputRequestID.Validate() != nil || claim.ThreadRequestID == input.ThreadRequestID || claim.InputRequestID == input.TurnRequestID || claim.Revision == 0 {
		return executionCheckpointUncertain()
	}
	digest, err := hex.DecodeString(claim.AssignmentDigest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != claim.AssignmentDigest {
		return executionCheckpointUncertain()
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "jobs", string(claim.JobID)+".json"), 2<<20)
	var operation journal
	var completion domain.ExecutionCompletion
	if err != nil || domain.Decode(raw, &operation) != nil || operation.Version != 1 || operation.JobID != claim.JobID || operation.InstanceID != claim.InstanceID || operation.Revision != claim.Revision || operation.Digest != claim.AssignmentDigest || (operation.State != journalFinished && operation.State != journalReported) || operation.Problem != nil || operation.ReportID.Validate() != nil || domain.Decode(operation.Output, &completion) != nil || completion != c.Completion {
		return executionCheckpointUncertain()
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "jobs", string(claim.JobID), "publication.json"), 1<<20)
	var publication publicationJournal
	if err != nil || domain.Decode(raw, &publication) != nil || publication.Version != 1 || publication.JobID != claim.JobID || publication.InstanceID != claim.InstanceID || publication.ServerID != claim.ServerID || publication.DeviceID != claim.DeviceID || publication.Revision != claim.Revision || publication.AssignmentDigest != claim.AssignmentDigest || publication.Pending != nil || publication.LastSequence != completion.LastSequence {
		return executionCheckpointUncertain()
	}
	return nil
}
