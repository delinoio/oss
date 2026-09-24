package worker

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

type nativeResponseInspector interface {
	InspectInteraction(context.Context, domain.ID) (codex.InteractionStatus, error)
	InspectInteractionResponse(context.Context, domain.ID, domain.ID) (codex.InteractionStatus, error)
}

// The response observation is journaled before this automatic read. It never
// invokes another response send or changes the original delivery fact. When
// NextEvent observes exact acceptance, let its original normal event pass the
// durable outbox instead of killing it with the send context. The caller holds
// c.mu; capture immutable scope, release it during inspection and reacquire
// before returning. Missing/foreign proof retains ordinary uncertainty.
func (c *CodexEventPublisher) inspectUncertainResponseLocked(ctx context.Context, identity responseControlIdentity, original domain.ExecutionInteractionUpdate, client nativeResponseInspector) bool {
	turn, kind, logger := c.turn, c.approvalKinds[identity.InteractionID], c.publisher.config.Logger
	c.mu.Unlock()
	defer c.mu.Lock()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := client.InspectInteractionResponse(bounded, identity.ResponseID, identity.InteractionID)
	confirmed := err == nil && status.Accepted && status.ID == identity.InteractionID && status.ResponseID == identity.ResponseID && status.TurnID == turn && status.ItemID == original.NativeItemID && status.Delivery == codex.QuestionDeliveryUncertain
	if confirmed {
		switch original.Type {
		case domain.UserQuestionInteraction:
			confirmed = status.ApprovalEvidence == ""
		case domain.NativeApprovalInteraction:
			switch kind {
			case domain.CodexCommandApproval:
				confirmed = status.ApprovalEvidence == codex.ApprovedCommandEvidence
			case domain.CodexFileApproval:
				confirmed = status.ApprovalEvidence == codex.ApprovedPatchEvidence
			case domain.CodexPermissionsApproval:
				confirmed = status.ApprovalEvidence == codex.PermissionOutputEvidence
			default:
				confirmed = false
			}
		default:
			confirmed = false
		}
	}
	if logger != nil {
		code := "ok"
		if err != nil {
			code = string(domain.SafeError(err).Code)
		}
		logger.InfoContext(ctx, "interaction_response_inspection_observed", "job_id", identity.JobID, "interaction_id", identity.InteractionID, "response_id", identity.ResponseID, "confirmed_live", confirmed, "code", code)
	}
	return confirmed
}
