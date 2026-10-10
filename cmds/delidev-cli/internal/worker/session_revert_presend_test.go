// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestRevertPreSendFailureRequiresAbsentClaimAndEveryCleanupJoin(t *testing.T) {
	f := newCheckpointFixture(t)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	action, owner := domain.NewID(), domain.NewID()
	progress := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), LastSequence: f.completion.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}, AcceptedInputs: f.ref.AcceptedInputs}
	restore := f.input
	restore.Version, restore.ExecutionID, restore.InputID = 2, action, domain.NewID()
	restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
	restore.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: f.ref.Completion, AssignmentInputDigest: f.ref.AssignmentInputDigest, InputMode: f.input.Input.Mode, PromptDigest: domain.BindSessionInput(f.input.InputID, f.input.Input).PromptDigest, Intent: domain.ContinueAutomatically}
	input := domain.SessionCompactionInput{Version: 4, ActionID: action, SourceJobID: f.jobID, Assignment: f.input, Restore: restore, Completion: f.ref.Completion, Dispatch: domain.DispatchReady, Revert: &domain.SessionRevertTarget{MessageID: domain.NewID(), InputID: f.input.InputID, NativeTurnID: f.completion.NativeTurnID, Prompt: f.input.Input}}
	if input.Validate() != nil {
		t.Fatal("invalid original fixture")
	}
	raw, _ := json.Marshal(input)
	job := domain.Job{Input: raw}
	joined := revertPreSendBoundary{nativeClosed: true, proxyClosed: true, workspaceClosed: true, authenticationClosed: true}
	failure := domain.Fail(domain.Conflict, "The original history target could not be verified.", "")
	output, err := completedRevertPreSendFailure(owner, job, input, failure, joined)
	var result domain.SessionRevertPreSendFailure
	if err != nil || domain.Decode(output, &result) != nil || result.Validate() != nil || result.InputDigest != executionInputDigest(raw) || result.ActionID != action || result.FailureCode != domain.Conflict || result.ContextRevision != input.Revert.ContextRevision {
		t.Fatal("original failure not retained", err)
	}
	for _, change := range []func(*revertPreSendBoundary){func(b *revertPreSendBoundary) { b.claimInvoked = true }, func(b *revertPreSendBoundary) { b.nativeClosed = false }, func(b *revertPreSendBoundary) { b.proxyClosed = false }, func(b *revertPreSendBoundary) { b.workspaceClosed = false }, func(b *revertPreSendBoundary) { b.authenticationClosed = false }, func(b *revertPreSendBoundary) { b.cleanupFailed = true }} {
		changed := joined
		change(&changed)
		if raw, err := completedRevertPreSendFailure(owner, job, input, failure, changed); err == nil || len(raw) != 0 {
			t.Fatal("claim or cleanup uncertainty became no-send")
		}
	}
	if _, err := completedRevertPreSendFailure(owner, job, input, nil, joined); err == nil {
		t.Fatal("success fabricated failure")
	}
	input.Version, input.Revert = 2, nil
	if _, err := completedRevertPreSendFailure(owner, job, input, failure, joined); err == nil {
		t.Fatal("ordinary compaction borrowed Revert failure")
	}
}
