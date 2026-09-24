package server

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishExecutionInteraction(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	u := event.Interaction
	if u == nil {
		return executionEventConflict()
	}
	if event.Kind == domain.ExecutionInteractionRequested {
		value := domain.ExecutionInteraction{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeItemID: u.NativeItemID, NativeRequestID: u.NativeRequestID, Type: u.Type, Questions: u.Questions, Closure: domain.InteractionOpen, FirstSequence: event.Sequence, LastSequence: event.Sequence}
		if _, err := tx.Put(domain.InteractionKind, u.ID, 0, session.ID, session.ProjectID, value); err != nil {
			return err
		}
		raw, err := json.Marshal(u.Questions)
		if err != nil {
			return err
		}
		return tx.BindExecutionInteraction(session.ID, input.ExecutionID, u.ID, event.NativeThreadID, u.NativeRequestID, len(raw))
	}
	r, err := tx.Get(domain.InteractionKind, u.ID)
	if err != nil {
		return err
	}
	value, err := store.Decode[domain.ExecutionInteraction](r)
	if err != nil {
		return err
	}
	key, keyErr := value.NativeRequestID.Key()
	newKey, newErr := u.NativeRequestID.Key()
	if keyErr != nil || newErr != nil || key != newKey || r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeItemID != u.NativeItemID || value.Type != u.Type || value.Closure != domain.InteractionOpen {
		return executionEventConflict()
	}
	return closePublishedInteraction(tx, r, value, u.Closure, event.Sequence)
}

func closePublishedInteraction(tx *store.Tx, r store.Record, value domain.ExecutionInteraction, closure domain.InteractionClosure, sequence uint64) error {
	if value.Response != nil {
		// No Worker response claim exists at this boundary yet. A response that
		// never left the durable queue becomes canceled, never transmitted or
		// accepted. Future delivery states require their own reconciliation.
		if value.Response.State != domain.QuestionResponseQueued && value.Response.State != domain.QuestionResponseCanceled {
			return executionEventConflict()
		}
		value.Response.State = domain.QuestionResponseCanceled
	}
	if err := tx.CloseExecutionInteraction(r.SessionID, value.ExecutionID, r.ID, value.NativeThreadID, value.NativeRequestID, closure); err != nil {
		return err
	}
	value.Closure, value.LastSequence = closure, sequence
	_, err := tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	return err
}

func endPublishedInteractions(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent) error {
	ids, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return err
		}
		if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.Closure != domain.InteractionOpen {
			return executionEventConflict()
		}
		if err := closePublishedInteraction(tx, r, value, domain.InteractionTurnEnded, event.Sequence); err != nil {
			return err
		}
	}
	return nil
}
