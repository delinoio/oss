package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeStop(tx *store.Tx, input domain.ExecutionJobInput, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.ClaudeStop
	if input.Configuration.Harness != domain.ClaudeCode || v == nil || v.Validate() != nil || v.InputID != input.InputID || p.ClaudeStop != nil || p.ClaudeTerminal != nil || p.ClaudeInterruption != nil || p.UnconfirmedResponses != 0 {
		return executionEventConflict()
	}
	canceled, err := tx.JobCancellationRequested(p.JobID)
	if err != nil {
		return err
	}
	if !canceled {
		return executionEventConflict()
	}
	nativeIDs := []string{string(v.RequestID), v.InterruptedNativeID, v.ContextNativeID, v.ResultNativeID, v.CommandNativeID, v.IdleNativeID, v.BlockStopNativeID, v.MessageStopNativeID}
	for _, retry := range v.Retries {
		nativeIDs = append(nativeIDs, retry.NativeEventID)
	}
	for _, native := range nativeIDs {
		if native == "" {
			continue
		}
		if native == event.NativeThreadID || native == event.NativeTurnID || native == string(input.ThreadRequestID) || native == string(input.TurnRequestID) || native == string(p.JobID) {
			return executionEventConflict()
		}
		seen, err := tx.HasExecutionNativeMessage(input.ExecutionID, native)
		if err != nil {
			return err
		}
		if seen {
			return executionEventConflict()
		}
	}
	r, err := tx.Get(domain.MessageKind, v.MessageID)
	if err != nil {
		return err
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil {
		return err
	}
	if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(r.SessionID), r.SessionID != input.SessionID) ||
		m.ExecutionID != input.ExecutionID || m.NativeThreadID != event.NativeThreadID || m.NativeTurnID != event.NativeTurnID || m.NativeID != v.NativeMessageID || m.Role != domain.AssistantMessage || m.Claude == nil || m.Claude.Model != input.Configuration.NativeModel || m.LastSequence >= event.Sequence {
		return executionEventConflict()
	}
	m.Claude, err = domain.InterruptClaudeContent(m.Claude, m.State, *v)
	if err != nil {
		return executionEventConflict()
	}
	m.State, m.LastSequence = domain.MessageComplete, event.Sequence
	if _, err := tx.Put(domain.MessageKind, r.ID, r.Revision, r.SessionID, r.ProjectID, m); err != nil {
		return err
	}
	if err := tx.BindExecutionMessage(input.SessionID, input.ExecutionID, r.ID, event.NativeThreadID, event.NativeTurnID, m.NativeID, domain.MessageComplete); err != nil {
		return err
	}
	complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	complete, err = tx.HasCompletedClaudeInput(input.ExecutionID, input.InputID, event.NativeTurnID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	pending, err := tx.OpenExecutionInteractions(input.ExecutionID)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return executionEventConflict()
	}
	copy := *v
	p.ClaudeStop = &copy
	return nil
}
