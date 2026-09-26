package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (b *OpenCodeBindingPublisher) validPublicationClaims(claims []opencode.SessionClaim) bool {
	if b.expectedReply != nil || len(claims) != 2+len(b.replyClaims) {
		return false
	}
	for i, claim := range b.replyClaims {
		if claims[i+2] != claim {
			return false
		}
	}
	return true
}

func (c *OpenCodeEventPublisher) publishInteractionReply(ctx context.Context, o opencode.Observation) error {
	n := o.InteractionReply
	b := c.text.binding
	if n == nil || n.SessionID != b.thread || n.Rejected || len(o.RejectionSources) != 0 || len(o.AlwaysObservations) != 0 {
		return publicationUncertain()
	}
	original := c.interactions[n.RequestID]
	attempt := c.responses[n.RequestID]
	if original.ID == "" || original.OpenCode == nil || attempt == nil || attempt.accepted || !attempt.journal.HTTPAccepted {
		return publicationUncertain()
	}
	var question *domain.OpenCodeQuestionResponse
	var permission *domain.OpenCodePermissionResponse
	if n.Kind == opencode.QuestionInteraction {
		if o.Kind != opencode.QuestionRepliedEvent || original.Type != domain.UserQuestionInteraction || n.Decision != nil {
			return publicationUncertain()
		}
		question = &domain.OpenCodeQuestionResponse{Answers: n.Answers}
		if question.Validate(original.OpenCode) != nil {
			return publicationUncertain()
		}
	} else if n.Kind == opencode.PermissionInteraction {
		if o.Kind != opencode.PermissionRepliedEvent || original.Type != domain.NativeApprovalInteraction || n.Decision == nil || *n.Decision != opencode.PermissionOnce || n.Answers != nil {
			return publicationUncertain()
		}
		permission = &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}
	} else {
		return publicationUncertain()
	}
	digest, err := domain.OpenCodeResponseDigest(question, permission)
	if err != nil || digest != attempt.journal.Native.BodyDigest {
		return publicationUncertain()
	}
	receipt, err := c.api.InteractionReceipt(ctx, n.RequestID)
	claim := attempt.journal.Native
	if err != nil || receipt.RequestID != claim.RequestID || receipt.InputRequestID != claim.InputRequestID || receipt.InteractionID != claim.InteractionID || receipt.ArrivalID != claim.ArrivalID || !receipt.HTTPAccepted || !receipt.NativeAccepted || receipt.FeedbackRequested {
		return publicationUncertain()
	}
	b.mu.Lock()
	claims, err := b.readClaims()
	valid := err == nil && b.validPublicationClaims(claims) && b.stage == openCodeAccepted && !c.text.blocked
	b.mu.Unlock()
	if !valid {
		return publicationUncertain()
	}
	evidence := &domain.OpenCodeReplyEvidence{NativeEventID: o.EventID, ProposalEventID: original.OpenCode.NativeEventID, NativeRequestID: n.RequestID, BodyDigest: digest, HTTPAccepted: true}
	event := domain.ExecutionEvent{NativeThreadID: b.thread, NativeTurnID: b.turn}
	if original.Type == domain.UserQuestionInteraction {
		event.Kind, event.QuestionAcceptance = domain.ExecutionQuestionAccepted, &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: original.ID, ResponseID: claim.RequestID, ClaimID: attempt.journal.ClaimID, NativeItemID: original.NativeItemID, Evidence: domain.NativeOpenCodeQuestionReply, OpenCode: evidence}
	} else {
		event.Kind, event.ApprovalAcceptance = domain.ExecutionApprovalAccepted, &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: original.ID, ResponseID: claim.RequestID, ClaimID: attempt.journal.ClaimID, NativeItemID: original.NativeItemID, Evidence: domain.NativeOpenCodePermissionReply, OpenCode: evidence}
	}
	if err := b.publisher.Publish(ctx, event); err != nil {
		return err
	}
	closure := domain.ExecutionInteractionUpdate{ID: original.ID, NativeRequestID: original.NativeRequestID, NativeItemID: original.NativeItemID, Type: original.Type, Closure: domain.InteractionNativeClosed}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, NativeThreadID: b.thread, NativeTurnID: b.turn, Interaction: &closure}); err != nil {
		return err
	}
	attempt.accepted = true
	delete(c.interactions, n.RequestID)
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_reply_accepted", "job_id", b.publisher.job, "interaction_id", original.ID, "response_id", claim.RequestID, "claim_id", attempt.journal.ClaimID)
	}
	return nil
}
