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
		if update.Snapshot.Kind.IsOpenCode() {
			exists, err := tx.HasOpenCodeToolCall(input.ExecutionID, update.Snapshot.OpenCodeCallID())
			if err != nil {
				return err
			}
			if exists {
				return executionEventConflict()
			}
		}
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, NativeParentID: update.NativeParentID, Role: domain.ToolMessage, State: domain.MessageStreaming, FirstSequence: event.Sequence, Tool: &domain.ExecutionTool{Started: *update.Snapshot}}
	} else {
		r, err := tx.Get(domain.MessageKind, update.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.NativeParentID != update.NativeParentID || value.Role != domain.ToolMessage || value.State != domain.MessageStreaming || value.Tool == nil || value.Tool.Completed != nil {
			return executionEventConflict()
		}
		revision = r.Revision
		tool := value.Tool
		if tool.Started.Kind.IsOpenCode() {
			prior := tool.Started
			if len(tool.States) > 0 {
				prior = tool.States[len(tool.States)-1].Snapshot
			}
			if update.Snapshot == nil || domain.ValidateOpenCodeToolTransition(prior, *update.Snapshot) != nil {
				return executionEventConflict()
			}
		}
		switch event.Kind {
		case domain.ExecutionToolUpdated:
			if !tool.Started.Kind.IsOpenCode() || len(tool.States) >= 1024 {
				return executionEventConflict()
			}
			tool.States = append(tool.States, domain.SequencedToolState{Sequence: event.Sequence, Snapshot: *update.Snapshot})
		case domain.ExecutionToolCompleted:
			if tool.Started.Kind.IsOpenCode() {
				ids, err := tx.ExecutionItemInteractions(input.ExecutionID, event.NativeThreadID, event.NativeTurnID, update.NativeID)
				if err != nil {
					return err
				}
				for _, id := range ids {
					r, err := tx.Get(domain.InteractionKind, id)
					if err != nil {
						return err
					}
					interaction, err := store.Decode[domain.ExecutionInteraction](r)
					if err != nil {
						return err
					}
					if interaction.Closure == domain.InteractionOpen {
						return executionEventConflict()
					}
				}
			}
			if update.Snapshot.Kind != tool.Started.Kind {
				return executionEventConflict()
			}
			if tool.Started.Kind == domain.CommandTool {
				prior, next := tool.Started.Command, update.Snapshot.Command
				// Native process/parsed/output metadata can become available later;
				// the command's original operation cannot be substituted.
				if prior.Command != next.Command || prior.Cwd != next.Cwd {
					return executionEventConflict()
				}
				if prior.Source != next.Source {
					matches, err := codexApprovalSourceTransition(tx, input, event, value.FirstSequence, prior.Source, next.Source)
					if err != nil {
						return err
					}
					if !matches {
						return executionEventConflict()
					}
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

// Codex 0.151.0's approval presentation emits an early Agent start and
// suppresses the later canonical start. Its unified-exec completion reports
// ExecStartup instead. Preserve both observations only for an admitted Codex profile
// and an exactly attributed intervening command approval; this is not approval acceptance.
// Remove this compatibility rule when the pinned native profile emits a stable
// source, after validating the replacement against installed native evidence.
func codexApprovalSourceTransition(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent, started uint64, prior, next domain.CommandSource) (bool, error) {
	if input.Configuration.Harness != domain.Codex || !domain.CodexVersionAllowed(input.Installation.Version) || prior != domain.AgentCommand || next != domain.ExecStartupCommand {
		return false, nil
	}
	ids, err := tx.ExecutionItemInteractions(input.ExecutionID, event.NativeThreadID, event.NativeTurnID, event.Tool.NativeID)
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
		if r.SessionID != input.SessionID || value.ExecutionID != input.ExecutionID || value.Type != domain.NativeApprovalInteraction || value.FirstSequence <= started || value.FirstSequence >= event.Sequence || value.Approval == nil {
			continue
		}
		approval := value.Approval
		if approval.Harness == domain.Codex && approval.Version == input.Installation.Version && approval.Codex != nil && approval.Codex.Kind == domain.CodexCommandApproval && approval.Codex.Command != nil && approval.Codex.Command.Kind == domain.CodexExecuteCommandApproval && approval.Codex.Command.ApprovalID == nil {
			return true, nil
		}
	}
	return false, nil
}
