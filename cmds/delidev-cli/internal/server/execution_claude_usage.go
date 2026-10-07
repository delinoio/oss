package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeUsage(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	u := event.ClaudeUsage
	if u == nil || input.Configuration.Harness != domain.ClaudeCode {
		return executionEventConflict()
	}
	if u.Source != domain.ClaudeInputResultUsage {
		r, err := tx.Get(domain.MessageKind, u.MessageID)
		if err != nil {
			return err
		}
		m, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != session.ID) ||
			m.ExecutionID != input.ExecutionID || m.NativeThreadID != event.NativeThreadID || m.NativeTurnID != event.NativeTurnID || m.NativeID != u.NativeMessageID || m.Claude == nil || m.Claude.Model != u.Model || u.Model != input.Configuration.NativeModel || m.Role != domain.AssistantMessage || m.NativeParentID != "" || m.State != domain.MessageStreaming {
			return executionEventConflict()
		}
		switch u.Source {
		case domain.ClaudeMessageStartUsage:
			if len(m.Claude.Blocks) != 0 {
				return executionEventConflict()
			}
		case domain.ClaudeBlockCompleteUsage:
			if int(*u.Index) >= len(m.Claude.Blocks) || m.Claude.Blocks[*u.Index].State != domain.ClaudeBlockCompleted {
				return executionEventConflict()
			}
		}
	} else {
		complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
		if err != nil {
			return err
		}
		if !complete {
			return executionEventConflict()
		}
	}
	observation := domain.ClaudeUsageRecord{ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Harness: input.Configuration.Harness, Version: input.Installation.Version, ThreadID: event.NativeThreadID, TurnID: event.NativeTurnID, Sequence: event.Sequence, Usage: *u}
	if err := tx.PutClaudeUsage(event.ObservationID, session.ID, session.ProjectID, observation); err != nil {
		return err
	}
	return tx.PutClaudeAccounting(event.ObservationID, input.InputID, session.ID, session.ProjectID, observation)
}
