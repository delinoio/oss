package worker

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func (b *OpenCodeBindingPublisher) validPublicationClaims(claims []opencode.SessionClaim) bool {
	want := 2 + len(b.replyClaims)
	if b.stopClaim != nil {
		want++
	}
	if b.expectedReply != nil || b.expectedStop != nil || len(claims) != want || b.stopClaim != nil && claims[want-1] != *b.stopClaim {
		return false
	}
	for i, claim := range b.replyClaims {
		if claims[i+2] != claim {
			return false
		}
	}
	return true
}

type openCodeClosedInteraction struct {
	original   domain.ExecutionInteractionUpdate
	rejected   bool
	correction bool
}

func (c *OpenCodeEventPublisher) publishInteractionReply(ctx context.Context, o opencode.Observation) error {
	n := o.InteractionReply
	b := c.text.binding
	if n == nil || n.SessionID != b.thread {
		return publicationUncertain()
	}
	original := c.interactions[n.RequestID]
	if original.ID == "" || original.OpenCode == nil {
		return publicationUncertain()
	}
	attempt := c.responses[n.RequestID]
	if attempt == nil {
		return c.publishPolicyClosure(ctx, o, original)
	}
	if attempt.accepted || !attempt.journal.HTTPAccepted || len(o.RejectionSources) != 0 || len(o.AlwaysObservations) != 0 {
		return publicationUncertain()
	}
	var digest string
	if n.Kind == opencode.QuestionInteraction {
		if original.Type != domain.UserQuestionInteraction || n.Decision != nil || n.Rejected != attempt.rejected || n.Rejected && o.Kind != opencode.QuestionRejectedEvent || !n.Rejected && o.Kind != opencode.QuestionRepliedEvent {
			return publicationUncertain()
		}
		question := &domain.OpenCodeQuestionResponse{Answers: n.Answers, Reject: n.Rejected}
		if question.Validate(original.OpenCode) != nil {
			return publicationUncertain()
		}
		var err error
		digest, err = domain.OpenCodeResponseDigest(question, nil)
		if err != nil {
			return err
		}
	} else if n.Kind == opencode.PermissionInteraction {
		if o.Kind != opencode.PermissionRepliedEvent || original.Type != domain.NativeApprovalInteraction || n.Decision == nil || domain.OpenCodePermissionDecision(*n.Decision) != attempt.decision || n.Rejected != attempt.rejected || n.Answers != nil {
			return publicationUncertain()
		}
		// Native permission events echo the decision, not correction feedback. The
		// exact-body HTTP receipt and immutable claim preserve that separate fact.
		digest = attempt.journal.Native.BodyDigest
	} else {
		return publicationUncertain()
	}
	if digest != attempt.journal.Native.BodyDigest {
		return publicationUncertain()
	}
	receipt, err := c.api.InteractionReceipt(ctx, n.RequestID)
	claim := attempt.journal.Native
	if err != nil || receipt.RequestID != claim.RequestID || receipt.InputRequestID != claim.InputRequestID || receipt.InteractionID != claim.InteractionID || receipt.ArrivalID != claim.ArrivalID || !receipt.HTTPAccepted || !receipt.NativeAccepted || receipt.FeedbackRequested != attempt.feedbackRequested {
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
		kind := domain.NativeOpenCodeQuestionReply
		if n.Rejected {
			kind = domain.NativeOpenCodeQuestionRejected
		}
		event.Kind, event.QuestionAcceptance = domain.ExecutionQuestionAccepted, &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: original.ID, ResponseID: claim.RequestID, ClaimID: attempt.journal.ClaimID, NativeItemID: original.NativeItemID, Evidence: kind, OpenCode: evidence}
	} else {
		event.Kind, event.ApprovalAcceptance = domain.ExecutionApprovalAccepted, &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: original.ID, ResponseID: claim.RequestID, ClaimID: attempt.journal.ClaimID, NativeItemID: original.NativeItemID, Evidence: domain.NativeOpenCodePermissionReply, OpenCode: evidence}
	}
	if err := b.publisher.Publish(ctx, event); err != nil {
		return err
	}
	if err := c.closeOriginalInteraction(ctx, original, nil, n.Rejected, attempt.correction); err != nil {
		return err
	}
	attempt.accepted = true
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_reply_accepted", "job_id", b.publisher.job, "interaction_id", original.ID, "response_id", claim.RequestID, "claim_id", attempt.journal.ClaimID)
	}
	return nil
}

func (c *OpenCodeEventPublisher) publishPolicyClosure(ctx context.Context, o opencode.Observation, original domain.ExecutionInteractionUpdate) error {
	n := o.InteractionReply
	if o.Kind != opencode.PermissionRepliedEvent || n.Kind != opencode.PermissionInteraction || n.Decision == nil || n.Answers != nil || original.Type != domain.NativeApprovalInteraction {
		return publicationUncertain()
	}
	decision := domain.OpenCodePermissionDecision(*n.Decision)
	var sources []string
	switch decision {
	case domain.OpenCodePermissionAlways:
		if n.Rejected || len(o.RejectionSources) != 0 {
			return publicationUncertain()
		}
		sources = o.AlwaysObservations
	case domain.OpenCodePermissionReject:
		if !n.Rejected || len(o.AlwaysObservations) != 0 {
			return publicationUncertain()
		}
		sources = o.RejectionSources
	default:
		return publicationUncertain()
	}
	proof := &domain.OpenCodePolicyClosure{NativeEventID: o.EventID, ProposalEventID: original.OpenCode.NativeEventID, Decision: decision, Sources: []domain.OpenCodePolicySource{}}
	for _, id := range sources {
		direct := c.responses[id]
		if direct == nil || !direct.accepted || direct.decision != decision || direct.original.OpenCode == nil || direct.original.OpenCode.Permission == nil || c.closedInteractions[id].original.ID != direct.original.ID || decision == domain.OpenCodePermissionAlways && len(direct.original.OpenCode.Permission.Always) == 0 {
			return publicationUncertain()
		}
		proof.Sources = append(proof.Sources, domain.OpenCodePolicySource{InteractionID: direct.original.ID, NativeRequestID: id})
	}
	if proof.Validate() != nil {
		return publicationUncertain()
	}
	if err := c.closeOriginalInteraction(ctx, original, proof, n.Rejected, false); err != nil {
		return err
	}
	b := c.text.binding
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_native_policy_closure", "job_id", b.publisher.job, "interaction_id", original.ID, "decision", decision, "source_count", len(sources))
	}
	return nil
}

func (c *OpenCodeEventPublisher) closeOriginalInteraction(ctx context.Context, original domain.ExecutionInteractionUpdate, proof *domain.OpenCodePolicyClosure, rejected, correction bool) error {
	closure := domain.ExecutionInteractionUpdate{ID: original.ID, NativeRequestID: original.NativeRequestID, NativeItemID: original.NativeItemID, Type: original.Type, Closure: domain.InteractionNativeClosed, OpenCodeClosure: proof}
	if closure.Validate(domain.ExecutionInteractionClosed) != nil {
		return publicationUncertain()
	}
	raw, err := json.Marshal(closure)
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	claims, claimErr := b.readClaims()
	if err != nil || len(raw) > maxOpenCodeTextBytes-c.text.bytes || claimErr != nil || !b.validPublicationClaims(claims) || b.stage != openCodeAccepted || c.text.blocked {
		return publicationUncertain()
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, NativeThreadID: b.thread, NativeTurnID: b.turn, Interaction: &closure}); err != nil {
		return err
	}
	if c.closedInteractions == nil {
		c.closedInteractions = map[string]openCodeClosedInteraction{}
	}
	c.closedInteractions[original.NativeRequestID.Text] = openCodeClosedInteraction{original: original, rejected: rejected, correction: correction}
	delete(c.interactions, original.NativeRequestID.Text)
	c.text.bytes += len(raw)
	return nil
}

// Rejection becomes root-stop evidence only after the original tool fails and
// the final assistant belongs to that request under the verified native policy.
// Nonempty direct correction continues; an automatically rejected sibling has
// no correction, even when one was supplied on a source response.
func (c *OpenCodeEventPublisher) rejectionState() (rejected, stopped bool) {
	for _, closed := range c.closedInteractions {
		tool := c.text.tools[closed.original.NativeItemID]
		if !closed.rejected || tool == nil || tool.latest.Status != domain.ToolFailed {
			continue
		}
		rejected = true
		if closed.original.OpenCode.NativeMessageID == c.final && !closed.correction && c.text.binding.requested.Rejection == opencode.StopOnInteractionRejection {
			stopped = true
		}
	}
	return rejected, stopped
}
