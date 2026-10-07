package server

import (
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func retainedGrokJournal(tx *store.Tx, input domain.ExecutionJobInput, thread, turn string) (*grok.ToolJournal, map[domain.ID]store.Record, map[domain.ID]domain.ExecutionInteraction, error) {
	mode, err := input.Configuration.GrokModeForInput(input.Input.Mode)
	if err != nil {
		return nil, nil, nil, err
	}
	journal, err := grok.NewToolJournal(domain.ID(thread), turn, mode, "")
	if err != nil {
		return nil, nil, nil, err
	}
	records, err := tx.GrokInteractionRecords(input.SessionID, input.ExecutionID)
	if err != nil {
		return nil, nil, nil, err
	}
	byArrival := map[domain.ID]store.Record{}
	replies := map[domain.ID]domain.ExecutionInteraction{}
	for _, record := range records {
		value, err := store.Decode[domain.ExecutionInteraction](record)
		if err != nil || value.Grok == nil || value.Grok.Version != input.Installation.Version || value.NativeThreadID != thread || value.NativeTurnID != turn || value.Grok.Validate(value.Type, value.NativeRequestID, value.NativeItemID) != nil || byArrival[value.Grok.Event.ArrivalID].ID != "" {
			return nil, nil, nil, executionEventConflict()
		}
		byArrival[value.Grok.Event.ArrivalID] = record
		replies[value.Grok.Event.ArrivalID] = value
	}
	messages, err := tx.GrokToolJournal(input.SessionID, input.ExecutionID)
	if err != nil {
		return nil, nil, nil, err
	}
	var previous uint64
	for _, message := range messages {
		if message.NativeThreadID != thread || message.NativeTurnID != turn || message.FirstSequence <= previous || message.LastSequence != message.FirstSequence || message.State != domain.MessageComplete {
			return nil, nil, nil, executionEventConflict()
		}
		if _, err := journal.ObserveWithReplies(*message.GrokTool, message.FirstSequence, replies); err != nil {
			return nil, nil, nil, err
		}
		previous = message.FirstSequence
	}
	return journal, byArrival, replies, nil
}

func publishGrokTool(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.GrokTool
	if u == nil || u.Validate() != nil || input.Continuation != nil || u.Observation.Payload.Session != domain.ID(event.NativeThreadID) || p.GrokToolObservations >= 4096 || u.Observation.RequestID != nil && u.Observation.ProposalJSON == "" {
		return executionEventConflict()
	}
	if meta := u.Observation.Payload.Meta; meta != nil {
		if err := advanceGrokNativeEvent(p, meta.Event, event.NativeThreadID); err != nil {
			return err
		}
	}
	journal, records, replies, err := retainedGrokJournal(tx, input, event.NativeThreadID, event.NativeTurnID)
	if err != nil {
		return err
	}
	settled, err := journal.ObserveWithReplies(u.Observation, event.Sequence, replies)
	if err != nil {
		return err
	}
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, Role: domain.ToolMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, GrokTool: &u.Observation}
	if meta := u.Observation.Payload.Meta; meta != nil {
		message.NativeID = meta.Event
	}
	// Each complete record is one immutable observation. Native tool completion
	// remains the original typed status, independently checked by the reducer.
	if _, err := tx.Put(domain.MessageKind, u.ID, 0, sr.ID, sr.ProjectID, message); err != nil {
		return err
	}
	p.GrokToolObservations++
	p.GrokCurrentMode = journal.Mode()
	if settled != "" {
		var r store.Record
		var value domain.ExecutionInteraction
		for arrival, candidate := range replies {
			if candidate.NativeItemID == settled {
				if r.ID != "" {
					return executionEventConflict()
				}
				r, value = records[arrival], candidate
			}
		}
		if r.ID == "" || value.Closure != domain.InteractionOpen || p.UnconfirmedResponses == 0 {
			return executionEventConflict()
		}
		if value.Type == domain.UserQuestionInteraction {
			value.Response.State = domain.QuestionResponseAccepted
			value.Response.Acceptance = &domain.QuestionAcceptanceObservation{Evidence: domain.NativeGrokToolResult, Sequence: event.Sequence}
		} else {
			value.ApprovalResponse.State = domain.ApprovalResponseAccepted
			value.ApprovalResponse.Acceptance = &domain.ApprovalAcceptanceObservation{Evidence: domain.NativeGrokApprovalResult, Sequence: event.Sequence}
		}
		p.UnconfirmedResponses--
		if _, err := closePublishedInteraction(tx, r, value, domain.InteractionNativeClosed, event.Sequence); err != nil {
			return err
		}
	}
	return nil
}

func validateGrokInteraction(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent) error {
	u := event.Interaction
	if u == nil || u.Grok == nil || u.Grok.Version != input.Installation.Version || u.Grok.Validate(u.Type, u.NativeRequestID, u.NativeItemID) != nil {
		return executionEventConflict()
	}
	r, err := tx.Get(domain.MessageKind, u.Grok.ObservationID)
	if err != nil {
		return err
	}
	message, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || r.SessionID != input.SessionID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.FirstSequence >= event.Sequence || message.GrokTool == nil || !reflect.DeepEqual(*message.GrokTool, u.Grok.Event) {
		return executionEventConflict()
	}
	digest, err := grok.PublicRequestDigest(u.NativeRequestID)
	if err != nil || digest != u.Grok.RequestDigest {
		return executionEventConflict()
	}
	journal, _, _, err := retainedGrokJournal(tx, input, event.NativeThreadID, event.NativeTurnID)
	if err != nil {
		return err
	}
	return journal.ValidateRequest(u.Grok, u.NativeItemID)
}

func advanceGrokNativeEvent(p *domain.ExecutionProgress, event, thread string) error {
	if event == "" {
		return nil
	}
	next, err := domain.GrokEventIndex(event, thread)
	if err != nil {
		return err
	}
	if p.GrokLastNativeEvent != "" {
		last, err := domain.GrokEventIndex(p.GrokLastNativeEvent, thread)
		if err != nil || next <= last {
			return executionEventConflict()
		}
	}
	p.GrokLastNativeEvent = event
	return nil
}
