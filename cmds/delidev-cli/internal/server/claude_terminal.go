package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeTerminal(tx *store.Tx, input domain.ExecutionJobInput, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.ClaudeTerminal
	if input.Configuration.Harness != domain.ClaudeCode || v == nil || v.Validate() != nil || v.InputID != input.InputID || p.ClaudeInterruption != nil || p.ClaudeTerminal != nil || !p.ClaudeTasks.Closed() || !p.ClaudeCompaction.Closed() || p.UnconfirmedResponses != 0 || p.LatestUsageID == "" {
		return executionEventConflict()
	}
	for _, native := range []string{v.ResultNativeID, v.CommandNativeID, v.IdleNativeID} {
		if native == event.NativeTurnID || native == event.NativeThreadID {
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
	complete, err := tx.HasCompletedClaudeInput(input.ExecutionID, input.InputID, event.NativeTurnID)
	if err != nil {
		return err
	}
	if !complete {
		return executionEventConflict()
	}
	complete, err = tx.ExecutionMessagesComplete(input.ExecutionID)
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
	record, err := tx.Get(domain.UsageKind, p.LatestUsageID)
	if err != nil {
		return err
	}
	u, err := store.Decode[domain.ClaudeUsageRecord](record)
	if err != nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(record.SessionID), record.SessionID != input.SessionID) ||
		u.ExecutionID != input.ExecutionID || u.ThreadID != event.NativeThreadID || u.TurnID != event.NativeTurnID || u.Sequence >= event.Sequence || u.Usage.Source != domain.ClaudeInputResultUsage || u.Usage.NativeEventID != v.ResultNativeID || u.Usage.Validate() != nil {
		return executionEventConflict()
	}
	copy := *v
	p.ClaudeTerminal = &copy
	return nil
}
