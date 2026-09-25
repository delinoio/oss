package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func (c *CodexEventPublisher) publishApprovalAcceptance(ctx context.Context, event codex.Event) error {
	status := event.InteractionState
	if status == nil || status.ApprovalEvidence != codex.PermissionOutputEvidence || !status.Accepted || status.TurnID != c.turn || status.ItemID != event.ItemID || status.ResponseID.Validate() != nil || event.Interaction != nil {
		return publicationUncertain()
	}
	original, known := c.interactions[status.ID]
	delivery, delivered := c.approvalResponses[status.ID]
	if !known || original.Type != domain.NativeApprovalInteraction || c.approvalKinds[status.ID] != domain.CodexPermissionsApproval || !delivered || original.NativeItemID != event.ItemID || delivery.ResponseID != status.ResponseID || delivery.NativeItemID != event.ItemID || delivery.Delivery == domain.ApprovalNotSent || domain.ApprovalDelivery(status.Delivery) != delivery.Delivery {
		return publicationUncertain()
	}
	update := domain.ExecutionApprovalAcceptanceUpdate{InteractionID: status.ID, ResponseID: status.ResponseID, ClaimID: delivery.ClaimID, NativeItemID: event.ItemID, Evidence: domain.NativePermissionsOutput}
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionApprovalAccepted, ApprovalAcceptance: &update})
}
