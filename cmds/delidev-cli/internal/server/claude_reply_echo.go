package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeReplyEcho(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, event domain.ExecutionEvent) error {
	u := event.ClaudeReplyEcho
	if u == nil || input.Configuration.Harness != domain.ClaudeCode || input.Version != 4 && !domain.ValidNativeVersionMetadata(input.Installation.Version) {
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
	if r.SessionID != input.SessionID || v.ExecutionID != input.ExecutionID || v.NativeThreadID != event.NativeThreadID || v.NativeTurnID != event.NativeTurnID || v.NativeItemID != u.NativeItemID || v.Claude == nil || v.Claude.ArrivalID != u.ArrivalID {
		return executionEventConflict()
	}
	var claim *domain.QuestionResponseClaim
	var response *domain.ClaudePermissionResponse
	switch v.Type {
	case domain.UserQuestionInteraction:
		a := v.Response
		if a == nil || v.ApprovalResponse != nil || a.ID != u.ResponseID || a.ClaudeEcho != nil || a.Input.ValidateInteraction(v) != nil || a.Delivery == nil || a.Delivery.State == domain.QuestionNotSent || a.Delivery.Sequence >= event.Sequence || a.State != domain.QuestionResponseTransmitted && a.State != domain.QuestionResponseUncertain {
			return executionEventConflict()
		}
		claim, response = a.Claim, a.Input.Claude
	case domain.NativeApprovalInteraction:
		a := v.ApprovalResponse
		if a == nil || v.Response != nil || a.ID != u.ResponseID || a.ClaudeEcho != nil || a.Input.ValidateInteraction(v) != nil || a.Delivery == nil || a.Delivery.State == domain.ApprovalNotSent || a.Delivery.Sequence >= event.Sequence || a.State != domain.ApprovalResponseTransmitted && a.State != domain.ApprovalResponseUncertain {
			return executionEventConflict()
		}
		claim, response = a.Claim, a.Input.Claude
	default:
		return executionEventConflict()
	}
	_, err = store.Decode[domain.Job](job)
	if err != nil {
		return err
	}
	if claim == nil || claim.ID != u.ClaimID || claim.JobID != job.ID || response == nil {
		return executionEventConflict()
	}
	digest, err := domain.ClaudeResponseDigest(v, *response)
	if err != nil || digest != u.BodyDigest {
		return executionEventConflict()
	}
	echo := &domain.ClaudeReplyEcho{ArrivalID: u.ArrivalID, BodyDigest: digest, Sequence: event.Sequence}
	if v.Response != nil {
		v.Response.ClaudeEcho = echo
	} else {
		v.ApprovalResponse.ClaudeEcho = echo
	}
	// Keep unconfirmed response accounting, request closure and prior recovery.
	// An exact echo cannot prove native semantic acceptance or tool execution.
	v.LastSequence = event.Sequence
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, v)
	return err
}
