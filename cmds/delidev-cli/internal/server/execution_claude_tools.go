package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Nested proposal mutations share the provider-message transaction; a result
// arrives separately and never changes that provider message's stopped state.
func publishClaudeTool(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent, u *domain.ClaudeToolUpdate) error {
	if u == nil || input.Configuration.Harness != domain.ClaudeCode || u.Validate() != nil {
		return executionEventConflict()
	}
	if u.Mutation == domain.ClaudeToolStart {
		available, err := tx.ClaudeWebIdentityAvailable(session.ID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, u.Reference.NativeID)
		if err != nil {
			return err
		}
		if !available {
			return executionEventConflict()
		}
	}
	provider, err := tx.Get(domain.MessageKind, u.MessageID)
	if err != nil {
		return err
	}
	message, err := store.Decode[domain.ExecutionMessage](provider)
	if err != nil {
		return err
	}
	if provider.SessionID != session.ID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.NativeID != u.NativeMessageID || message.Role != domain.AssistantMessage || message.NativeParentID != "" || message.Claude == nil || int(u.Index) >= len(message.Claude.Blocks) {
		return executionEventConflict()
	}
	block := message.Claude.Blocks[u.Index]
	if block.Block.Tool == nil || *block.Block.Tool != u.Reference {
		return executionEventConflict()
	}
	expected := domain.ClaudeBlockStreaming
	if u.Mutation == domain.ClaudeToolProposalComplete {
		expected = domain.ClaudeBlockCompleted
	}
	if u.Mutation == domain.ClaudeToolResultObserved {
		expected = domain.ClaudeBlockStopped
	}
	// The original proposal block must stop before its result; the enclosing
	// provider can still be streaming. Its own message-stop remains independent.
	result := u.Mutation == domain.ClaudeToolResultObserved
	if block.State != expected || message.State != domain.MessageStreaming && (!result || message.State != domain.MessageComplete) {
		return executionEventConflict()
	}
	var value domain.ExecutionMessage
	var revision uint64
	if u.Mutation == domain.ClaudeToolStart {
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: u.Reference.NativeID, NativeParentID: u.NativeMessageID, Role: domain.ToolMessage, FirstSequence: event.Sequence}
	} else {
		row, err := tx.Get(domain.MessageKind, u.Reference.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			return err
		}
		if row.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != u.Reference.NativeID || value.NativeParentID != u.NativeMessageID || value.Role != domain.ToolMessage || value.Text != "" || value.InputID != "" || value.Phase != nil || value.Tool != nil || value.Artifact != nil || value.Progress != nil || value.Claude != nil || value.ClaudeTool == nil {
			return executionEventConflict()
		}
		revision = row.Revision
	}
	content, state, err := domain.ApplyClaudeTool(value.ClaudeTool, value.State, *u)
	if err != nil {
		return err
	}
	value.ClaudeTool, value.State, value.LastSequence = content, state, event.Sequence
	if _, err := tx.Put(domain.MessageKind, u.Reference.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, u.Reference.ID, event.NativeThreadID, event.NativeTurnID, u.Reference.NativeID, state)
}
