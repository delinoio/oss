package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type codexToolPublication struct {
	ID              domain.ID
	Kind            domain.ToolKind
	Completed       bool
	SleepDurationMS *uint64
	ImageView       *domain.ImageViewObservation
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
		var snapshot domain.ToolSnapshot
		var err error
		if event.Tool.Kind == codex.ImageViewTool {
			reference := domain.NewID()
			if known && retained.ImageView != nil {
				reference = retained.ImageView.ReferenceID
			}
			var observed domain.ImageViewObservation
			observed, err = workspace.ObserveImageViewLocation(c.publisher.input, runtime.GOOS, event.Tool.ImagePath, reference)
			if err != nil && c.publisher.input.NativeDynamicTools {
				return c.publishExternalImageView(ctx, event)
			}
			snapshot = domain.ToolSnapshot{Kind: domain.ImageViewTool, Status: domain.ToolRunning, ImageView: &observed}
			if event.Kind == codex.ToolCompletedEvent {
				snapshot.Status = domain.ToolCompleted
			}
			if event.Tool.SleepDurationMS != nil || event.Tool.Command != nil || event.Tool.Changes != nil || event.Tool.ID != event.ItemID || event.Tool.Status != map[domain.ToolStatus]codex.ToolStatus{domain.ToolRunning: codex.ToolRunning, domain.ToolCompleted: codex.ToolCompleted}[snapshot.Status] {
				return publicationUncertain()
			}
		} else {
			snapshot, err = codexToolSnapshot(*event.Tool)
		}
		if err != nil {
			return err
		}
		update.Snapshot = &snapshot
		if event.Kind == codex.ToolStartedEvent {
			if c.itemKnown(event.ItemID) || c.itemLimitReached() {
				return publicationUncertain()
			}
			retained = codexToolPublication{ID: domain.NewID(), Kind: snapshot.Kind, ImageView: snapshot.ImageView, SleepDurationMS: nil}
			if snapshot.Sleep != nil {
				retained.SleepDurationMS = snapshot.Sleep.DurationMS
			}
			if snapshot.ImageView != nil {
				retained.ID = snapshot.ImageView.ReferenceID
			}
			update.ID = retained.ID
			kind = domain.ExecutionToolStarted
		} else {
			if !known || retained.Completed || retained.Kind != snapshot.Kind || snapshot.Kind == domain.ImageViewTool && (retained.ImageView == nil || snapshot.ImageView == nil || *retained.ImageView != *snapshot.ImageView) {
				return publicationUncertain()
			}
			if snapshot.Kind == domain.SleepTool && (retained.SleepDurationMS == nil || snapshot.Sleep == nil || snapshot.Sleep.DurationMS == nil || *retained.SleepDurationMS != *snapshot.Sleep.DurationMS) {
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
			outputKind := domain.CommandTool
			if event.ToolOutputKind == codex.PatchTool {
				outputKind = domain.PatchTool
			} else if event.ToolOutputKind != "" && event.ToolOutputKind != codex.CommandTool {
				return publicationUncertain()
			}
			if retained.Kind != outputKind {
				return publicationUncertain()
			}
			kind, update.Delta = domain.ExecutionToolOutput, &event.TextDelta
			if outputKind == domain.PatchTool {
				update.OutputKind = outputKind
			}
		case codex.ToolInputEvent:
			if retained.Kind != domain.CommandTool || event.ToolInput == nil {
				return publicationUncertain()
			}
			kind, update.Input = domain.ExecutionToolInput, &domain.ToolInputObservation{ProcessID: event.ToolInput.ProcessID, Text: event.ToolInput.Text}
		case codex.ToolPatchEvent:
			if retained.Kind != domain.PatchTool || event.Tool == nil || event.Tool.ID != event.ItemID || event.Tool.Kind != codex.PatchTool || event.Tool.Status != "" || event.Tool.Command != nil || event.Tool.SleepDurationMS != nil {
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

// Native view_image may read a temporary image outside the prepared workspace.
// Such a call has no workspace reference or preview authority. Retain its
// lifecycle and a location commitment through the negotiated inert tool path
// instead of failing the original turn or adopting the file into the workspace.
func (c *CodexEventPublisher) publishExternalImageView(ctx context.Context, event codex.Event) error {
	input := c.publisher.input
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil || preparation.SessionID != input.SessionID || preparation.MachineID != input.MachineID || workspace.ValidateResult(preparation, manifest, runtime.GOOS) != nil {
		return publicationUncertain()
	}
	tool := event.Tool
	if tool == nil || tool.ID != event.ItemID || tool.Kind != codex.ImageViewTool || tool.SleepDurationMS != nil || tool.Command != nil || tool.Changes != nil || !filepath.IsAbs(tool.ImagePath) || filepath.Clean(tool.ImagePath) != tool.ImagePath || strings.Contains(tool.ImagePath, "://") || domain.Text(tool.ImagePath, "native image location", 4096, true) != nil {
		return publicationUncertain()
	}
	if event.Kind != codex.ToolStartedEvent && event.Kind != codex.ToolCompletedEvent || event.Kind == codex.ToolStartedEvent && tool.Status != codex.ToolRunning || event.Kind == codex.ToolCompletedEvent && tool.Status != codex.ToolCompleted {
		return publicationUncertain()
	}
	// A malformed scoped observation must not gain the external fallback. Only
	// a positively validated location outside every original root can enter it.
	roots := []string{manifest.PrimaryPath}
	for _, repository := range manifest.Repositories {
		roots = append(roots, repository.Path)
	}
	for _, root := range roots {
		relative, err := filepath.Rel(root, tool.ImagePath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return publicationUncertain()
		}
	}
	args, _ := json.Marshal(struct {
		Path string `json:"path"`
	}{tool.ImagePath})
	digest := sha256.Sum256(args)
	namespace := "codex"
	snapshot := &domain.DynamicToolSnapshot{Identity: domain.DynamicToolIdentity{Tool: "view_image", Namespace: &namespace, ArgumentsDigest: hex.EncodeToString(digest[:])}, Status: domain.DynamicRunning}
	stage := domain.DynamicStarted
	if event.Kind == codex.ToolCompletedEvent {
		stage, snapshot.Status = domain.DynamicCompleted, domain.DynamicDone
	}
	return c.publishDynamicTool(ctx, codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: event.ItemID, Correlated: true, DynamicTool: &domain.DynamicToolUpdate{NativeID: event.ItemID, Stage: stage, Snapshot: snapshot}})
}

func codexToolSnapshot(native codex.Tool) (domain.ToolSnapshot, error) {
	result := domain.ToolSnapshot{
		Kind:    map[codex.ToolKind]domain.ToolKind{codex.CommandTool: domain.CommandTool, codex.PatchTool: domain.PatchTool, codex.SleepTool: domain.SleepTool}[native.Kind],
		Status:  map[codex.ToolStatus]domain.ToolStatus{codex.ToolRunning: domain.ToolRunning, codex.ToolCompleted: domain.ToolCompleted, codex.ToolFailed: domain.ToolFailed, codex.ToolDeclined: domain.ToolDeclined}[native.Status],
		Changes: codexFileChanges(native.Changes),
	}
	if native.SleepDurationMS != nil {
		value := *native.SleepDurationMS
		result.Sleep = &domain.SleepObservation{DurationMS: &value}
	}
	if native.ImagePath != "" {
		return result, publicationUncertain()
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
