package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeMessage(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	u := event.ClaudeMessage
	if u == nil || input.Configuration.Harness != domain.ClaudeCode || u.NativeID == string(input.InputID) {
		return executionEventConflict()
	}
	var value domain.ExecutionMessage
	var revision uint64
	if u.Mutation == domain.ClaudeMessageStart {
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: u.NativeID, Role: domain.AssistantMessage, FirstSequence: event.Sequence}
	} else {
		r, err := tx.Get(domain.MessageKind, u.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != session.ID) ||
			value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != u.NativeID || value.NativeParentID != "" || value.Role != domain.AssistantMessage || value.Text != "" || value.InputID != "" || value.Phase != nil || value.ClaudeTool != nil || value.Tool != nil || value.Artifact != nil || value.Progress != nil || value.Claude == nil {
			return executionEventConflict()
		}
		revision = r.Revision
	}
	content, state, err := domain.ApplyClaudeContent(value.Claude, value.State, *u)
	if err != nil {
		return err
	}
	value.Claude, value.State, value.LastSequence = content, state, event.Sequence
	if _, err := tx.Put(domain.MessageKind, u.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	if err := tx.BindExecutionMessage(session.ID, input.ExecutionID, u.ID, event.NativeThreadID, event.NativeTurnID, u.NativeID, state); err != nil {
		return err
	}
	if u.Tool != nil {
		return publishClaudeTool(tx, input, session, event, u.Tool)
	}
	return nil
}
