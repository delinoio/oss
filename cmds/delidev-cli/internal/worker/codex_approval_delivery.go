package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (c *CodexEventPublisher) PublishApprovalDelivery(ctx context.Context, update domain.ExecutionApprovalResponseUpdate) (returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishApprovalDeliveryLocked(ctx, update)
}

func (c *CodexEventPublisher) publishApprovalDeliveryLocked(ctx context.Context, update domain.ExecutionApprovalResponseUpdate) (returned error) {
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if err := update.Validate(); err != nil {
		return err
	}
	original, known := c.interactions[update.InteractionID]
	_, delivered := c.approvalResponses[update.InteractionID]
	if !known || original.Type != domain.NativeApprovalInteraction || delivered || original.Closure != "" || original.NativeItemID != update.NativeItemID {
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionApprovalDeliveryObserved, ApprovalResponse: &update}); err != nil {
		return err
	}
	c.approvalResponses[update.InteractionID] = update
	return nil
}
