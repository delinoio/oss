package server

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishExecutionInteraction(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) (bool, error) {
	u := event.Interaction
	if u == nil {
		return false, executionEventConflict()
	}
	if event.Kind == domain.ExecutionInteractionRequested {
		value := domain.ExecutionInteraction{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeItemID: u.NativeItemID, NativeRequestID: u.NativeRequestID, Type: u.Type, Questions: u.Questions, Closure: domain.InteractionOpen, FirstSequence: event.Sequence, LastSequence: event.Sequence}
		if _, err := tx.Put(domain.InteractionKind, u.ID, 0, session.ID, session.ProjectID, value); err != nil {
			return false, err
		}
		raw, err := json.Marshal(u.Questions)
		if err != nil {
			return false, err
		}
		return false, tx.BindExecutionInteraction(session.ID, input.ExecutionID, u.ID, event.NativeThreadID, u.NativeRequestID, len(raw))
	}
	r, err := tx.Get(domain.InteractionKind, u.ID)
	if err != nil {
		return false, err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return false, err
	}
	key, keyErr := value.NativeRequestID.Key()
	newKey, newErr := u.NativeRequestID.Key()
	if keyErr != nil || newErr != nil || key != newKey || r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != u.Type || value.Closure != domain.InteractionOpen {
		return false, executionEventConflict()
	}
	return closePublishedInteraction(tx, r, value, u.Closure, event.Sequence)
}

func closePublishedInteraction(tx *store.Tx, r store.Record, value domain.ExecutionInteraction, closure domain.InteractionClosure, sequence uint64) (bool, error) {
	uncertain := false
	if value.Response != nil {
		switch value.Response.State {
		case domain.QuestionResponseQueued, domain.QuestionResponseCanceled:
			value.Response.State = domain.QuestionResponseCanceled
		case domain.QuestionResponseClaimed, domain.QuestionResponseUncertain:
			// A claimed reply may have left the process. Closure cannot prove
			// that it was accepted or authorize a replacement send.
			value.Response.State = domain.QuestionResponseUncertain
			uncertain = true
		default:
			return false, executionEventConflict()
		}
	}
	if err := tx.CloseExecutionInteraction(r.SessionID, value.ExecutionID, r.ID, value.NativeThreadID, value.NativeRequestID, closure); err != nil {
		return false, err
	}
	value.Closure, value.LastSequence = closure, sequence
	_, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return uncertain, err
}

func endPublishedInteractions(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent) (bool, error) {
	uncertain := false
	ids, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return false, err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return false, err
		}
		if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.Closure != domain.InteractionOpen {
			return false, executionEventConflict()
		}
		unconfirmed, err := closePublishedInteraction(tx, r, value, domain.InteractionTurnEnded, event.Sequence)
		if err != nil {
			return false, err
		}
		uncertain = uncertain || unconfirmed
	}
	return uncertain, nil
}
