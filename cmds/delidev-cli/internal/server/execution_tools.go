package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishExecutionTool(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	update := event.Tool
	if update == nil {
		return executionEventConflict()
	}
	var value domain.ExecutionMessage
	var revision uint64
	if event.Kind == domain.ExecutionToolStarted {
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, Role: domain.ToolMessage, State: domain.MessageStreaming, FirstSequence: event.Sequence, Tool: &domain.ExecutionTool{Started: *update.Snapshot}}
	} else {
		r, err := tx.Get(domain.MessageKind, update.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.Role != domain.ToolMessage || value.State != domain.MessageStreaming || value.Tool == nil || value.Tool.Completed != nil {
			return executionEventConflict()
		}
		revision = r.Revision
		tool := value.Tool
		switch event.Kind {
		case domain.ExecutionToolCompleted:
			if update.Snapshot.Kind != tool.Started.Kind {
				return executionEventConflict()
			}
			if tool.Started.Kind == domain.CommandTool {
				prior, next := tool.Started.Command, update.Snapshot.Command
				// Native process/parsed/output metadata can become available later;
				// the command's original operation and source cannot be substituted.
				if prior.Command != next.Command || prior.Cwd != next.Cwd || prior.Source != next.Source {
					return executionEventConflict()
				}
			}
			tool.Completed = update.Snapshot
			value.State = domain.MessageComplete
		case domain.ExecutionToolOutput:
			if tool.Started.Kind != domain.CommandTool {
				return executionEventConflict()
			}
			output := ""
			if tool.Output != nil {
				output = *tool.Output
			}
			output += *update.Delta
			if err := domain.Text(output, "retained native tool output", domain.MaxMessageText, false); err != nil {
				return err
			}
			tool.Output = &output
		case domain.ExecutionToolInput:
			if tool.Started.Kind != domain.CommandTool || len(tool.Inputs) >= 1024 {
				return executionEventConflict()
			}
			tool.Inputs = append(tool.Inputs, domain.SequencedToolInput{Sequence: event.Sequence, Input: *update.Input})
		case domain.ExecutionToolPatch:
			if tool.Started.Kind != domain.PatchTool || len(tool.Patches) >= 1024 {
				return executionEventConflict()
			}
			tool.Patches = append(tool.Patches, domain.SequencedToolPatch{Sequence: event.Sequence, Changes: *update.Changes})
		default:
			return executionEventConflict()
		}
	}
	value.LastSequence = event.Sequence
	// Put's complete document bound prevents unbounded patch/input history. Its
	// transaction leaves prior evidence and sequence intact on size failure.
	if _, err := tx.Put(domain.MessageKind, update.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	// Tools share the native item uniqueness and completed-item gate with text
	// messages. A failed/declined tool is a completed item, not a failed turn.
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, update.ID, event.NativeThreadID, event.NativeTurnID, update.NativeID, value.State)
}
