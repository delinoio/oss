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
	compaction := v.Kind == domain.ClaudeCompactionProgress || v.Kind == domain.ClaudeCompactionSummaryProgress
	if v.InputAccepted != accepted || accepted && (event.NativeTurnID != p.NativeTurnID || p.Outcome != domain.ExecutionRunning) || !accepted && (p.Outcome != domain.ExecutionNotStarted || v.Kind != domain.ClaudeStatusProgress && v.Kind != domain.ClaudeAPIRetryProgress && !compaction) || v.NativeEventID == event.NativeTurnID || v.NativeEventID == string(input.InputID) {
		return executionEventConflict()
	}
	if p.ClaudeProgress == nil {
		p.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: event.NativeTurnID}
	}
	state := p.ClaudeProgress
	if state.NativeTurnID != event.NativeTurnID {
		return executionEventConflict()
	}
	if compaction {
		next, err := domain.ApplyClaudeCompaction(p.ClaudeCompaction, *u)
		if err != nil {
			return err
		}
		p.ClaudeCompaction = next
	}
	if v.Tool != nil {
		if err := validateClaudeProgressTool(tx, input, session, event, v.Tool.Tool, v.Tool.TaskID == nil); err != nil {
			return err
		}
		if v.Tool.NativeToolID != "" {
			collision, err := tx.HasExecutionNativeMessage(input.ExecutionID, v.Tool.NativeToolID)
			if err != nil {
				return err
			}
			if collision {
				return executionEventConflict()
			}
		}
	}
	if v.Tool != nil && v.Tool.TaskID != nil && !p.ClaudeTasks.OwnsRunningTask(*v.Tool.TaskID, v.Tool.Tool) {
		return executionEventConflict()
	}
	if v.Task != nil {
		if v.Task.Tool != nil {
			if err := validateClaudeProgressTool(tx, input, session, event, *v.Task.Tool, v.Task.Kind == domain.ClaudeTaskStarted); err != nil {
				return err
			}
		}
		next, err := domain.ApplyClaudeTask(p.ClaudeTasks, *v.Task)
		if err != nil {
			return err
		}
		p.ClaudeTasks = next
	}
	if v.ToolSummary != nil {
		for _, tool := range v.ToolSummary.Tools {
			if err := validateClaudeProgressTool(tx, input, session, event, tool, false); err != nil {
				return err
			}
		}
	}
	message := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: v.NativeEventID, Role: domain.ProgressMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, ClaudeProgress: &v}
	if _, err := tx.Put(domain.MessageKind, u.ID, 0, session.ID, session.ProjectID, message); err != nil {
		return err
	}
	if err := tx.BindExecutionMessage(session.ID, input.ExecutionID, u.ID, event.NativeThreadID, event.NativeTurnID, v.NativeEventID, domain.MessageComplete); err != nil {
		return err
	}
	switch v.Kind {
	case domain.ClaudeCompactionProgress:
		state.LatestCompactionID = u.ID
	case domain.ClaudeCompactionSummaryProgress:
		state.LatestCompactionSummaryID = u.ID
	case domain.ClaudeStatusProgress:
		state.LatestStatusID = u.ID
		if permission := v.Status.Permission; permission != nil {
			state.Permission = permission
			// Plan tools can change native mode within the accepted run. Keep
			// its current relay alive; the sticky flag gates future selection
			// reconciliation without rewriting the immutable initial settings.
			state.PermissionChanged = state.PermissionChanged || *permission != p.Observed.ClaudePermission
		}
	case domain.ClaudeTaskLifecycleProgress:
		state.LatestTaskID = u.ID
	case domain.ClaudeToolProgress:
		state.LatestToolID = u.ID
	case domain.ClaudeToolSummaryProgress:
		state.LatestToolSummaryID = u.ID
	case domain.ClaudeAPIRetryProgress:
		state.LatestRetryID = u.ID
	case domain.ClaudeThinkingProgress:
		state.LatestThinkingID = u.ID
	default:
		return executionEventConflict()
	}
	return nil
}

func validateClaudeProgressTool(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent, ref domain.ClaudeToolReference, running bool) error {
	r, err := tx.Get(domain.MessageKind, ref.ID)
	if err != nil {
		return err
	}
	message, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || r.SessionID != session.ID || message.ExecutionID != input.ExecutionID || message.NativeThreadID != event.NativeThreadID || message.NativeTurnID != event.NativeTurnID || message.Role != domain.ToolMessage || message.ClaudeTool == nil || message.ClaudeTool.Reference != ref || message.NativeID != ref.NativeID || message.NativeParentID != message.ClaudeTool.NativeMessageID || message.LastSequence >= event.Sequence || (message.State != domain.MessageStreaming && message.State != domain.MessageComplete) {
		return executionEventConflict()
	}
	if running && (message.State != domain.MessageStreaming || message.ClaudeTool.Proposal == nil || message.ClaudeTool.Result != nil) {
		return executionEventConflict()
	}
	return nil
}
