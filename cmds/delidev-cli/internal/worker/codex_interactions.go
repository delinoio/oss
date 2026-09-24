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
		if i == nil || i.ID.Validate() != nil || i.Kind != codex.UserInputInteraction || i.Questions == nil || e.InteractionState != nil {
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
	} else {
		status := e.InteractionState
		if status == nil || status.TurnID != c.turn || status.ItemID != e.ItemID || status.Closure != codex.InteractionNativeClosed || e.Interaction != nil {
			return publicationUncertain()
		}
		if status.ResponseID != "" || status.Delivery != codex.QuestionNotSent {
			return domain.Fail(domain.Unsupported, "Question response publication needs its durable owner claim.", "Retain the native response without inferring authorization or acceptance from closure.")
		}
		var known bool
		update, known = c.interactions[status.ID]
		if !known || update.NativeItemID != e.ItemID || update.Closure != "" {
			return publicationUncertain()
		}
		kind, update.Closure = domain.ExecutionInteractionClosed, domain.InteractionNativeClosed
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Interaction: &update}); err != nil {
		return err
	}
	update.Questions = nil // Durable original question content belongs to the server.
	if update.NativeRequestID.Number != nil {
		number := *update.NativeRequestID.Number
		update.NativeRequestID.Number = &number
	}
	c.interactions[update.ID] = update
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
