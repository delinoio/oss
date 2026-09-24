package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func (c *CodexEventPublisher) publishInteraction(ctx context.Context, e codex.Event) error {
	if e.TurnID != c.turn {
		return publicationUncertain()
	}
	kind := domain.ExecutionInteractionRequested
	var update domain.ExecutionInteractionUpdate
	if e.Kind == codex.InteractionRequestedEvent {
		i := e.Interaction
		if i == nil || i.ID.Validate() != nil || e.InteractionState != nil {
			return publicationUncertain()
		}
		if _, exists := c.interactions[i.ID]; exists || len(c.interactions) >= domain.MaxExecutionInteractions {
			return publicationUncertain()
		}
		update = domain.ExecutionInteractionUpdate{ID: i.ID, NativeItemID: e.ItemID, Type: domain.UserQuestionInteraction}
		switch i.NativeID.Kind {
		case codex.TextRequestID:
			update.NativeRequestID = domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: i.NativeID.Text, Number: i.NativeID.Number}
		case codex.NumberRequestID:
			update.NativeRequestID = domain.InteractionRequestID{Kind: domain.InteractionNumberID, Text: i.NativeID.Text, Number: i.NativeID.Number}
		default:
			return publicationUncertain()
		}
		switch i.Kind {
		case codex.ApprovalInteraction:
			if i.Questions != nil {
				return publicationUncertain()
			}
			approval, err := codexApprovalRequest(i.Approval)
			if err != nil {
				return err
			}
			update.Type, update.Approval = domain.NativeApprovalInteraction, approval
		case codex.UserInputInteraction:
			if i.Questions == nil || i.Approval != nil {
				return publicationUncertain()
			}
			q := &domain.QuestionRequest{Blocking: i.Questions.Blocking, AutoResolutionMS: i.Questions.AutoResolutionMS, Questions: []domain.Question{}}
			for _, source := range i.Questions.Questions {
				question := domain.Question{ID: source.ID, Header: source.Header, Text: source.Text, Other: source.Other, Secret: source.Secret}
				if source.Options != nil {
					question.Options = make([]domain.QuestionOption, 0, len(source.Options))
				}
				for _, option := range source.Options {
					question.Options = append(question.Options, domain.QuestionOption{Label: option.Label, Description: option.Description})
				}
				q.Questions = append(q.Questions, question)
			}
			update.Questions = q
		default:
			return publicationUncertain()
		}
	} else {
		status := e.InteractionState
		if status == nil || status.TurnID != c.turn || status.ItemID != e.ItemID || status.Closure != codex.InteractionNativeClosed || e.Interaction != nil {
			return publicationUncertain()
		}
		var known bool
		update, known = c.interactions[status.ID]
		if !known || update.NativeItemID != e.ItemID || update.Closure != "" {
			return publicationUncertain()
		}
		var responseID domain.ID
		var observed codex.QuestionDelivery
		var hasDelivery bool
		switch update.Type {
		case domain.UserQuestionInteraction:
			delivery, ok := c.questionResponses[status.ID]
			responseID, observed, hasDelivery = delivery.ResponseID, codex.QuestionDelivery(delivery.Delivery), ok
		case domain.NativeApprovalInteraction:
			delivery, ok := c.approvalResponses[status.ID]
			responseID, observed, hasDelivery = delivery.ResponseID, codex.QuestionDelivery(delivery.Delivery), ok
		default:
			return publicationUncertain()
		}
		if status.ResponseID != "" || status.Delivery != codex.QuestionNotSent {
			if !hasDelivery {
				return domain.Fail(domain.Unsupported, "Response publication needs its original durable owner claim.", "Retain the native response without inferring authorization or acceptance from closure.")
			}
			if status.ResponseID != responseID || status.Delivery != observed {
				return publicationUncertain()
			}
		} else if hasDelivery && observed != codex.QuestionNotSent {
			return publicationUncertain()
		}

		kind, update.Closure = domain.ExecutionInteractionClosed, domain.InteractionNativeClosed
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Interaction: &update}); err != nil {
		return err
	}
	update.Questions = nil // Durable original question content belongs to the server.
	update.Approval = nil  // The original approval also belongs to server retention.
	if update.NativeRequestID.Number != nil {
		number := *update.NativeRequestID.Number
		update.NativeRequestID.Number = &number
	}
	c.interactions[update.ID] = update
	return nil
}

// PublishQuestionDelivery retains a journaled native send observation. The
// original server claim is checked again at publication; this metadata never
// carries an answer or grants permission to invoke the native send itself.
func (c *CodexEventPublisher) PublishQuestionDelivery(ctx context.Context, update domain.ExecutionQuestionResponseUpdate) (returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishQuestionDeliveryLocked(ctx, update)
}

func (c *CodexEventPublisher) publishQuestionDeliveryLocked(ctx context.Context, update domain.ExecutionQuestionResponseUpdate) (returned error) {
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if err := update.Validate(); err != nil {
		return err
	}
	original, known := c.interactions[update.InteractionID]
	_, delivered := c.questionResponses[update.InteractionID]
	if !known || original.Type != domain.UserQuestionInteraction || delivered || original.Closure != "" || original.NativeItemID != update.NativeItemID {
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionQuestionDeliveryObserved, QuestionResponse: &update}); err != nil {
		return err
	}
	c.questionResponses[update.InteractionID] = update
	return nil
}

func (c *CodexEventPublisher) publishWaiting(ctx context.Context, status *codex.ThreadStatus) (bool, error) {
	if status == nil || (status.Type != codex.ThreadIdle && status.Type != codex.ThreadActive) {
		return false, nil
	}
	waiting := domain.NativeWaiting{}
	for _, flag := range status.ActiveFlags {
		switch flag {
		case codex.WaitingApproval:
			if waiting.Approval {
				return false, publicationUncertain()
			}
			waiting.Approval = true
		case codex.WaitingInput:
			if waiting.UserInput {
				return false, publicationUncertain()
			}
			waiting.UserInput = true
		default:
			return false, publicationUncertain()
		}
	}
	if status.Type == codex.ThreadIdle && waiting != (domain.NativeWaiting{}) {
		return false, publicationUncertain()
	}
	if waiting == c.waiting {
		return true, nil
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionWaitingChanged, Waiting: &waiting}); err != nil {
		return true, err
	}
	c.waiting = waiting
	return true, nil
}

func (c *CodexEventPublisher) publishQuestionAcceptance(ctx context.Context, event codex.Event) error {
	status := event.InteractionState
	if status == nil || !status.Accepted || status.TurnID != c.turn || status.ItemID != event.ItemID || status.ResponseID.Validate() != nil || event.Interaction != nil {
		return publicationUncertain()
	}
	original, known := c.interactions[status.ID]
	delivery, delivered := c.questionResponses[status.ID]
	if !known || !delivered || original.NativeItemID != event.ItemID || delivery.ResponseID != status.ResponseID || delivery.NativeItemID != event.ItemID || delivery.Delivery == domain.QuestionNotSent || domain.QuestionDelivery(status.Delivery) != delivery.Delivery {
		return publicationUncertain()
	}
	update := domain.ExecutionQuestionAcceptanceUpdate{InteractionID: status.ID, ResponseID: status.ResponseID, ClaimID: delivery.ClaimID, NativeItemID: event.ItemID, Evidence: domain.NativeQuestionOutput}
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionQuestionAccepted, QuestionAcceptance: &update})
}
