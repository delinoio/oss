package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeDenialCompletion(tx *store.Tx, input domain.ExecutionJobInput, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v, prior := event.ClaudeDenial, p.ClaudeInterruption
	if input.Configuration.Harness != domain.ClaudeCode || v == nil || v.Validate() != nil || v.InputID != input.InputID || p.ClaudeDenial != nil || p.ClaudeStop != nil || p.ClaudeTerminal != nil || prior == nil || prior.InteractionID != v.InteractionID || prior.ContextID != v.ContextID || prior.ResultID != v.ResultID || p.UnconfirmedResponses != 0 {
		return executionEventConflict()
	}
	for _, native := range []string{v.CommandNativeID, v.IdleNativeID} {
		if native == event.NativeTurnID || native == event.NativeThreadID {
			return executionEventConflict()
		}
		seen, err := tx.HasExecutionNativeMessage(input.ExecutionID, native)
		if err != nil {
			return err
		}
		if seen {
			return executionEventConflict()
		}
	}
	var contextObservation *domain.ClaudeInterruption
	var contextSequence uint64
	for _, id := range []domain.ID{v.ContextID, v.ResultID} {
		row, err := tx.Get(domain.MessageKind, id)
		if err != nil {
			return err
		}
		message, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			return err
		}
		r := message.ClaudeInterruption
		if row.SessionID != input.SessionID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.InputID != "" || message.State != domain.MessageComplete || message.LastSequence >= event.Sequence || r == nil || r.Validate() != nil || r.InteractionID != v.InteractionID || r.ArrivalID != v.ArrivalID {
			return executionEventConflict()
		}
		if id == v.ContextID {
			if r.Kind != domain.ClaudeDenialContext {
				return executionEventConflict()
			}
			contextObservation, contextSequence = r, message.LastSequence
		} else if r.Kind != domain.ClaudeDenialResult || contextObservation == nil || r.NativeEventID == contextObservation.NativeEventID || r.ToolMessageID != contextObservation.ToolMessageID || r.ToolResultNativeID != contextObservation.ToolResultNativeID || message.FirstSequence <= contextSequence {
			return executionEventConflict()
		}
	}
	row, err := tx.Get(domain.InteractionKind, v.InteractionID)
	if err != nil {
		return err
	}
	request, err := store.Decode[domain.ExecutionInteraction](row)
	if err != nil {
		return err
	}
	settlement := request.ClaudeSettlement
	if row.SessionID != input.SessionID || request.ExecutionID != input.ExecutionID || request.NativeThreadID != event.NativeThreadID || request.NativeTurnID != event.NativeTurnID || request.Claude == nil || request.Claude.ArrivalID != v.ArrivalID || request.Closure != domain.InteractionNativeClosed || request.ClaudeCancellation != nil || settlement == nil || settlement.Evidence != domain.ClaudeInterruptedDenialProcessed || settlement.ArrivalID != v.ArrivalID || settlement.ToolMessageID != contextObservation.ToolMessageID || settlement.ResultNativeID != contextObservation.ToolResultNativeID || settlement.Sequence >= contextSequence {
		return executionEventConflict()
	}
	var reply *domain.ClaudePermissionResponse
	if request.Response != nil && request.Response.State == domain.QuestionResponseAccepted && request.Response.Acceptance != nil && request.Response.Acceptance.Sequence == settlement.Sequence {
		reply = request.Response.Input.Claude
	}
	if request.ApprovalResponse != nil && request.ApprovalResponse.State == domain.ApprovalResponseAccepted && request.ApprovalResponse.Acceptance != nil && request.ApprovalResponse.Acceptance.Sequence == settlement.Sequence {
		reply = request.ApprovalResponse.Input.Claude
	}
	if reply == nil || reply.Behavior != domain.ClaudeReplyDeny || reply.Interrupt == nil || !*reply.Interrupt || reply.Validate(request) != nil {
		return executionEventConflict()
	}
	complete, err := tx.HasCompletedClaudeInput(input.ExecutionID, input.InputID, event.NativeTurnID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	complete, err = tx.ExecutionMessagesComplete(input.ExecutionID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	pending, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return executionEventConflict()
	}
	copy := *v
	p.ClaudeDenial = &copy
	return nil
}
