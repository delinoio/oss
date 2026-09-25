package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

type codexToolPublication struct {
	ID        domain.ID
	Kind      domain.ToolKind
	Completed bool
}

// Called under the event publisher lock. Only identities remain in this map;
// payload retention belongs to the synchronized outbox and server transcript.
func (c *CodexEventPublisher) publishTool(ctx context.Context, event codex.Event) error {
	if event.TurnID != c.turn {
		return publicationUncertain()
	}
	retained, known := c.tools[event.ItemID]
	update := domain.ExecutionToolUpdate{ID: retained.ID, NativeID: event.ItemID}
	var kind domain.ExecutionEventKind
	switch event.Kind {
	case codex.ToolStartedEvent, codex.ToolCompletedEvent:
		if event.Tool == nil || event.Tool.ID != event.ItemID {
			return publicationUncertain()
		}
		snapshot, err := codexToolSnapshot(*event.Tool)
		if err != nil {
			return err
		}
		update.Snapshot = &snapshot
		if event.Kind == codex.ToolStartedEvent {
			if c.itemKnown(event.ItemID) || c.itemLimitReached() {
				return publicationUncertain()
			}
			retained = codexToolPublication{ID: domain.NewID(), Kind: snapshot.Kind}
			update.ID = retained.ID
			kind = domain.ExecutionToolStarted
		} else {
			if !known || retained.Completed || retained.Kind != snapshot.Kind {
				return publicationUncertain()
			}
			retained.Completed = true
			kind = domain.ExecutionToolCompleted
		}
	case codex.ToolOutputEvent, codex.ToolInputEvent, codex.ToolPatchEvent:
		if !known || retained.Completed {
			return publicationUncertain()
		}
		switch event.Kind {
		case codex.ToolOutputEvent:
			if retained.Kind != domain.CommandTool {
				return publicationUncertain()
			}
			kind, update.Delta = domain.ExecutionToolOutput, &event.TextDelta
		case codex.ToolInputEvent:
			if retained.Kind != domain.CommandTool || event.ToolInput == nil {
				return publicationUncertain()
			}
			kind, update.Input = domain.ExecutionToolInput, &domain.ToolInputObservation{ProcessID: event.ToolInput.ProcessID, Text: event.ToolInput.Text}
		case codex.ToolPatchEvent:
			if retained.Kind != domain.PatchTool || event.Tool == nil || event.Tool.ID != event.ItemID || event.Tool.Kind != codex.PatchTool || event.Tool.Status != "" || event.Tool.Command != nil {
				return publicationUncertain()
			}
			changes := codexFileChanges(event.Tool.Changes)
			kind, update.Changes = domain.ExecutionToolPatch, &changes
		}
	default:
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Tool: &update}); err != nil {
		return err
	}
	c.tools[event.ItemID] = retained
	return nil
}

func codexToolSnapshot(native codex.Tool) (domain.ToolSnapshot, error) {
	result := domain.ToolSnapshot{
		Kind:    map[codex.ToolKind]domain.ToolKind{codex.CommandTool: domain.CommandTool, codex.PatchTool: domain.PatchTool}[native.Kind],
		Status:  map[codex.ToolStatus]domain.ToolStatus{codex.ToolRunning: domain.ToolRunning, codex.ToolCompleted: domain.ToolCompleted, codex.ToolFailed: domain.ToolFailed, codex.ToolDeclined: domain.ToolDeclined}[native.Status],
		Changes: codexFileChanges(native.Changes),
	}
	if n := native.Command; n != nil {
		c := &domain.CommandObservation{Command: n.Command, Cwd: n.Cwd, Source: map[codex.CommandSource]domain.CommandSource{codex.AgentCommand: domain.AgentCommand, codex.UserShellCommand: domain.UserShellCommand, codex.ExecStartupCommand: domain.ExecStartupCommand, codex.ExecInputCommand: domain.ExecInputCommand}[n.Source], ProcessID: n.ProcessID, AggregatedOutput: n.AggregatedOutput, ExitCode: n.ExitCode, DurationMS: n.DurationMS, PluginID: n.PluginID, ScriptPath: n.ScriptPath}
		if n.Actions != nil {
			c.Actions = make([]domain.CommandAction, 0, len(n.Actions))
		}
		for _, a := range n.Actions {
			kind := map[codex.CommandActionKind]domain.CommandActionKind{codex.ReadCommandAction: domain.ReadCommandAction, codex.ListCommandAction: domain.ListCommandAction, codex.SearchCommandAction: domain.SearchCommandAction, codex.UnknownCommandAction: domain.UnknownCommandAction}[a.Kind]
			c.Actions = append(c.Actions, domain.CommandAction{Kind: kind, Command: a.Command, Name: a.Name, Path: a.Path, Query: a.Query})
		}
		result.Command = c
	}
	return result, result.Validate()
}

func codexFileChanges(native []codex.FileChange) []domain.FileChangeObservation {
	if native == nil {
		return nil
	}
	result := make([]domain.FileChangeObservation, 0, len(native))
	for _, c := range native {
		kind := map[codex.FileChangeKind]domain.FileChangeKind{codex.AddedFile: domain.AddedFile, codex.DeletedFile: domain.DeletedFile, codex.UpdatedFile: domain.UpdatedFile}[c.Kind]
		result = append(result, domain.FileChangeObservation{Path: c.Path, Diff: c.Diff, Kind: kind, MovePath: c.MovePath})
	}
	return result
}
