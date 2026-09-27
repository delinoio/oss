package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishClaudeProgress(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, p *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.ClaudeProgress
	if u == nil || u.Validate() != nil || input.Configuration.Harness != domain.ClaudeCode || p.ClaudeInterruption != nil || domain.NativeIdentity(event.NativeTurnID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil {
		return executionEventConflict()
	}
	v := u.Observation
	accepted := p.NativeTurnID != ""
	if v.InputAccepted != accepted || accepted && (event.NativeTurnID != p.NativeTurnID || p.Outcome != domain.ExecutionRunning) || !accepted && (p.Outcome != domain.ExecutionNotStarted || v.Kind != domain.ClaudeStatusProgress && v.Kind != domain.ClaudeAPIRetryProgress) || v.NativeEventID == event.NativeTurnID || v.NativeEventID == string(input.InputID) {
		return executionEventConflict()
	}
	if p.ClaudeProgress == nil {
		p.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: event.NativeTurnID}
	}
	state := p.ClaudeProgress
	if state.NativeTurnID != event.NativeTurnID {
		return executionEventConflict()
	}
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: v.NativeEventID, Role: domain.ProgressMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, ClaudeProgress: &v}
	if _, err := tx.Put(domain.MessageKind, u.ID, 0, session.ID, session.ProjectID, message); err != nil {
		return err
	}
	if err := tx.BindExecutionMessage(session.ID, input.ExecutionID, u.ID, event.NativeThreadID, event.NativeTurnID, v.NativeEventID, domain.MessageComplete); err != nil {
		return err
	}
	switch v.Kind {
	case domain.ClaudeStatusProgress:
		state.LatestStatusID = u.ID
		if permission := v.Status.Permission; permission != nil {
			state.Permission = permission
			// Plan tools can change native mode within the accepted run. Keep
			// its current relay alive; the sticky flag gates future selection
			// reconciliation without rewriting the immutable initial settings.
			state.PermissionChanged = state.PermissionChanged || *permission != p.Observed.ClaudePermission
		}
	case domain.ClaudeAPIRetryProgress:
		state.LatestRetryID = u.ID
	case domain.ClaudeThinkingProgress:
		state.LatestThinkingID = u.ID
	default:
		return executionEventConflict()
	}
	return nil
}
