package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeInterruption(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, session *domain.Session, event domain.ExecutionEvent) error {
	u := event.ClaudeInterruption
	if u == nil || u.Validate() != nil || input.Configuration.Harness != domain.ClaudeCode || session.Execution == nil {
		return executionEventConflict()
	}
	v := u.Observation
	r, err := tx.Get(domain.InteractionKind, v.InteractionID)
	if err != nil {
		return err
	}
	request, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	proof := request.ClaudeSettlement
	if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != sr.ID) ||
		request.ExecutionID != input.ExecutionID || request.NativeThreadID != event.NativeThreadID || request.NativeTurnID != event.NativeTurnID || request.Claude == nil || request.Claude.ArrivalID != v.ArrivalID || request.Claude.Tool.ID != v.ToolMessageID || request.Closure != domain.InteractionNativeClosed || request.ClaudeCancellation != nil || proof == nil || proof.ArrivalID != v.ArrivalID || proof.ToolMessageID != v.ToolMessageID || proof.ResultNativeID != v.ToolResultNativeID || proof.Evidence != domain.ClaudeInterruptedDenialProcessed || proof.Sequence >= event.Sequence {
		return executionEventConflict()
	}
	var reply *domain.ClaudePermissionResponse
	if request.Type == domain.UserQuestionInteraction && request.Response != nil && request.ApprovalResponse == nil && request.Response.State == domain.QuestionResponseAccepted && request.Response.Acceptance != nil && request.Response.Acceptance.Sequence == proof.Sequence && request.Response.Acceptance.Evidence == domain.QuestionAcceptanceEvidence(proof.Evidence) {
		reply = request.Response.Input.Claude
	} else if request.Type == domain.NativeApprovalInteraction && request.ApprovalResponse != nil && request.Response == nil && request.ApprovalResponse.State == domain.ApprovalResponseAccepted && request.ApprovalResponse.Acceptance != nil && request.ApprovalResponse.Acceptance.Sequence == proof.Sequence && request.ApprovalResponse.Acceptance.Evidence == domain.ApprovalAcceptanceEvidence(proof.Evidence) {
		reply = request.ApprovalResponse.Input.Claude
	}
	if reply == nil || reply.Behavior != domain.ClaudeReplyDeny || reply.Interrupt == nil || !*reply.Interrupt || reply.Validate(request) != nil {
		return executionEventConflict()
	}
	toolRow, err := tx.Get(domain.MessageKind, v.ToolMessageID)
	if err != nil {
		return err
	}
	tool, err := store.Decode[domain.ExecutionMessage](toolRow)
	if err != nil {
		return err
	}
	if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(toolRow.SessionID), toolRow.SessionID != sr.ID) ||
		tool.ExecutionID != input.ExecutionID || tool.NativeThreadID != event.NativeThreadID || tool.NativeTurnID != event.NativeTurnID || tool.State != domain.MessageComplete || tool.ClaudeTool == nil || tool.ClaudeTool.Result == nil || tool.ClaudeTool.Result.NativeEventID != v.ToolResultNativeID || tool.LastSequence >= proof.Sequence {
		return executionEventConflict()
	}
	if evidence, err := domain.ClaudeCallbackResultEvidence(request, *reply, *tool.ClaudeTool); err != nil || evidence != proof.Evidence {
		return executionEventConflict()
	}
	p := session.Execution
	switch v.Kind {
	case domain.ClaudeDenialContext:
		if p.ClaudeInterruption != nil {
			return executionEventConflict()
		}
		p.ClaudeInterruption = &domain.ClaudeInterruptionProgress{InteractionID: v.InteractionID, ContextID: u.ID}
	case domain.ClaudeDenialResult:
		prior := p.ClaudeInterruption
		if prior == nil || prior.InteractionID != v.InteractionID || prior.ResultID != "" || p.UnconfirmedResponses != 0 {
			return executionEventConflict()
		}
		contextRow, err := tx.Get(domain.MessageKind, prior.ContextID)
		if err != nil {
			return err
		}
		context, err := store.Decode[domain.ExecutionMessage](contextRow)
		if err != nil {
			return err
		}
		c := context.ClaudeInterruption
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(contextRow.SessionID), contextRow.SessionID != sr.ID) ||
			context.ExecutionID != input.ExecutionID || context.NativeThreadID != event.NativeThreadID || context.NativeTurnID != event.NativeTurnID || context.State != domain.MessageComplete || context.LastSequence <= proof.Sequence || context.LastSequence >= event.Sequence || c == nil || c.Validate() != nil || c.Kind != domain.ClaudeDenialContext || c.NativeEventID == v.NativeEventID || c.InteractionID != v.InteractionID || c.ArrivalID != v.ArrivalID || c.ToolMessageID != v.ToolMessageID || c.ToolResultNativeID != v.ToolResultNativeID {
			return executionEventConflict()
		}
		complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
		if err != nil {
			return err
		}
		pending, err := tx.OpenExecutionInteractions(input.ExecutionID)
		if err != nil {
			return err
		}
		if !complete || len(pending) != 0 {
			return executionEventConflict()
		}
		prior.ResultID = u.ID
		// A callback-owned session result cannot fabricate a correlated input
		// terminal or cleanup. Pause for original-owner reconciliation while
		// preserving accepted input, prior recovery and the independent outcome.
		session.Dispatch = domain.DispatchPaused
		if session.Recovery == domain.NoRecovery {
			session.Recovery = domain.NeedsRecovery
		}
		if session.Problem == nil {
			session.Problem = domain.Fail(domain.RecoveryRequired, "Claude stopped after the original denied request without reporting an input result identity.", "Retain the original interruption and verify owned cleanup before reconciling this execution.")
		}
	default:
		return executionEventConflict()
	}
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: v.NativeEventID, Role: domain.ProgressMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, ClaudeInterruption: &v}
	if _, err := tx.Put(domain.MessageKind, u.ID, 0, sr.ID, sr.ProjectID, message); err != nil {
		return err
	}
	return tx.BindExecutionMessage(sr.ID, input.ExecutionID, u.ID, event.NativeThreadID, event.NativeTurnID, v.NativeEventID, domain.MessageComplete)
}
