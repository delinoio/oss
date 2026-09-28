package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"reflect"
	"slices"
)

// Bind the same original cleanup observation across canceled requests and the
// terminal event. It never clears product Stop, recovery or response uncertainty.
func bindOpenCodeStop(tx *store.Tx, input domain.ExecutionJobInput, progress *domain.ExecutionProgress, proof *domain.OpenCodeStopObservation) error {
	if input.Configuration.Harness != domain.OpenCode || proof == nil || proof.Validate() != nil || proof.InputRequestID != input.TurnRequestID || proof.RequestID == input.ThreadRequestID || proof.AssistantID == progress.NativeTurnID || progress.OpenCodeStop != nil && !reflect.DeepEqual(*progress.OpenCodeStop, *proof) {
		return executionEventConflict()
	}
	canceled, err := tx.JobCancellationRequested(progress.JobID)
	if err != nil {
		return err
	}
	if !canceled {
		return executionEventConflict()
	}
	part, err := tx.OpenCodeExecutionPart(input.SessionID, input.ExecutionID, progress.NativeThreadID, progress.NativeTurnID, proof.InputPartID)
	if err != nil {
		return err
	}
	if part.Role != domain.UserMessage || part.InputID != input.InputID || part.State != domain.MessageComplete || part.NativeParentID != progress.NativeTurnID {
		return executionEventConflict()
	}
	copy := *proof
	copy.RetryObservations = slices.Clone(proof.RetryObservations)
	progress.OpenCodeStop = &copy
	return nil
}

func validateOpenCodeStopClosure(tx *store.Tx, input domain.ExecutionJobInput, progress *domain.ExecutionProgress, current domain.ExecutionInteraction, proof *domain.OpenCodeStopClosure) error {
	if current.OpenCode == nil || current.OpenCodeClosure != nil || current.OpenCodeStop != nil || current.Closure != domain.InteractionOpen || proof == nil || proof.Validate() != nil || proof.ProposalEventID != current.OpenCode.NativeEventID {
		return executionEventConflict()
	}
	// A queued response can be canceled, but an already claimed send needs its
	// own acceptance reconciliation and cannot be erased by process cleanup.
	if r := current.Response; r != nil && (r.State != domain.QuestionResponseQueued || r.Claim != nil || r.Delivery != nil || r.Acceptance != nil) {
		return executionEventConflict()
	}
	if r := current.ApprovalResponse; r != nil && (r.State != domain.ApprovalResponseQueued || r.Claim != nil || r.Delivery != nil || r.Acceptance != nil) {
		return executionEventConflict()
	}
	tool, err := tx.OpenCodeInteractionTool(input.SessionID, input.ExecutionID, current.NativeThreadID, current.NativeTurnID, current.NativeItemID)
	if err != nil {
		return err
	}
	if tool.Role != domain.ToolMessage || tool.State != domain.MessageStreaming || tool.NativeParentID != current.OpenCode.NativeMessageID || tool.Tool == nil || tool.Tool.Completed != nil || tool.Tool.Started.OpenCodeCallID() != current.OpenCode.CallID {
		return executionEventConflict()
	}
	return bindOpenCodeStop(tx, input, progress, &proof.Stop)
}
