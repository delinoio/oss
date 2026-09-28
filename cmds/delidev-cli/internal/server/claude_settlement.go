package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeCallbackSettlement(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.ClaudeSettlement
	if u == nil || input.Configuration.Harness != domain.ClaudeCode || input.Installation.Version != domain.ClaudeProtocolVersion || progress.UnconfirmedResponses == 0 {
		return executionEventConflict()
	}
	r, err := tx.Get(domain.InteractionKind, u.InteractionID)
	if err != nil {
		return err
	}
	v, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	if r.SessionID != input.SessionID || v.ExecutionID != input.ExecutionID || v.NativeThreadID != event.NativeThreadID || v.NativeTurnID != event.NativeTurnID || v.NativeItemID != u.NativeItemID || v.Claude == nil || v.Claude.ArrivalID != u.ArrivalID || v.Claude.Tool.ID != u.ToolMessageID || v.Closure != domain.InteractionOpen || v.ClaudeCancellation != nil || v.ClaudeSettlement != nil {
		return executionEventConflict()
	}
	var claim *domain.QuestionResponseClaim
	var reply *domain.ClaudePermissionResponse
	var echo *domain.ClaudeReplyEcho
	switch v.Type {
	case domain.UserQuestionInteraction:
		a := v.Response
		if a == nil || v.ApprovalResponse != nil || a.ID != u.ResponseID || a.Acceptance != nil || a.Input.ValidateInteraction(v) != nil || a.Delivery == nil || a.Delivery.State == domain.QuestionNotSent || a.State != domain.QuestionResponseTransmitted && a.State != domain.QuestionResponseUncertain {
			return executionEventConflict()
		}
		claim, reply, echo = a.Claim, a.Input.Claude, a.ClaudeEcho
	case domain.NativeApprovalInteraction:
		a := v.ApprovalResponse
		if a == nil || v.Response != nil || a.ID != u.ResponseID || a.Acceptance != nil || a.Input.ValidateInteraction(v) != nil || a.Delivery == nil || a.Delivery.State == domain.ApprovalNotSent || a.State != domain.ApprovalResponseTransmitted && a.State != domain.ApprovalResponseUncertain {
			return executionEventConflict()
		}
		claim, reply, echo = a.Claim, a.Input.Claude, a.ClaudeEcho
	default:
		return executionEventConflict()
	}
	j, err := store.Decode[domain.Job](job)
	if err != nil {
		return err
	}
	if claim == nil || claim.ID != u.ClaimID || claim.JobID != job.ID || claim.InstanceID != j.InstanceID || claim.MachineID != input.MachineID || claim.DeviceID != actor || reply == nil || echo == nil || echo.ArrivalID != u.ArrivalID || echo.BodyDigest != u.BodyDigest || echo.Sequence >= event.Sequence {
		return executionEventConflict()
	}
	digest, err := domain.ClaudeResponseDigest(v, *reply)
	if err != nil || digest != u.BodyDigest {
		return executionEventConflict()
	}
	toolRow, err := tx.Get(domain.MessageKind, u.ToolMessageID)
	if err != nil {
		return err
	}
	tool, err := store.Decode[domain.ExecutionMessage](toolRow)
	if err != nil {
		return err
	}
	if toolRow.SessionID != input.SessionID || tool.ExecutionID != input.ExecutionID || tool.NativeThreadID != event.NativeThreadID || tool.NativeTurnID != event.NativeTurnID || tool.NativeID != u.NativeItemID || tool.Role != domain.ToolMessage || tool.State != domain.MessageComplete || tool.ClaudeTool == nil || tool.ClaudeTool.Result == nil || tool.ClaudeTool.Result.NativeEventID != u.ResultNativeID || tool.LastSequence <= echo.Sequence || tool.LastSequence >= event.Sequence {
		return executionEventConflict()
	}
	evidence, err := domain.ClaudeCallbackResultEvidence(v, *reply, *tool.ClaudeTool)
	if err != nil || evidence != u.Evidence {
		return executionEventConflict()
	}
	v.ClaudeSettlement = &domain.ClaudeCallbackSettlement{ArrivalID: u.ArrivalID, ToolMessageID: u.ToolMessageID, ResultNativeID: u.ResultNativeID, Evidence: evidence, Sequence: event.Sequence}
	if v.Response != nil {
		v.Response.State = domain.QuestionResponseAccepted
		v.Response.Acceptance = &domain.QuestionAcceptanceObservation{Evidence: domain.QuestionAcceptanceEvidence(evidence), Sequence: event.Sequence}
	} else {
		v.ApprovalResponse.State = domain.ApprovalResponseAccepted
		v.ApprovalResponse.Acceptance = &domain.ApprovalAcceptanceObservation{Evidence: domain.ApprovalAcceptanceEvidence(evidence), Sequence: event.Sequence}
	}
	// Consume only this response's unconfirmed count. Earlier uncertainty and
	// Stop/recovery remain authoritative; neither inbox reading nor root outcome
	// follows from processing one callback.
	progress.UnconfirmedResponses--
	_, err = closePublishedInteraction(tx, r, v, domain.InteractionNativeClosed, event.Sequence)
	return err
}
