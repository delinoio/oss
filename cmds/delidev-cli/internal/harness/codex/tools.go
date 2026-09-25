package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ToolKind string
type ToolStatus string
type CommandSource string
type CommandActionKind string
type FileChangeKind string

const (
	CommandTool ToolKind = "command-execution"
	PatchTool   ToolKind = "file-change"

	ToolRunning   ToolStatus = "inProgress"
	ToolCompleted ToolStatus = "completed"
	ToolFailed    ToolStatus = "failed"
	ToolDeclined  ToolStatus = "declined"

	AgentCommand       CommandSource = "agent"
	UserShellCommand   CommandSource = "userShell"
	ExecStartupCommand CommandSource = "unifiedExecStartup"
	ExecInputCommand   CommandSource = "unifiedExecInteraction"

	ReadCommandAction    CommandActionKind = "read"
	ListCommandAction    CommandActionKind = "listFiles"
	SearchCommandAction  CommandActionKind = "search"
	UnknownCommandAction CommandActionKind = "unknown"

	AddedFile   FileChangeKind = "add"
	DeletedFile FileChangeKind = "delete"
	UpdatedFile FileChangeKind = "update"
)

type CommandAction struct {
	Kind    CommandActionKind
	Command string
	Name    *string
	Path    *string
	Query   *string
}

type CommandExecution struct {
	Command          string
	Cwd              string
	Source           CommandSource
	Actions          []CommandAction
	ProcessID        *string
	AggregatedOutput *string
	ExitCode         *int32
	DurationMS       *int64
	PluginID         *string
	ScriptPath       *string
}

type FileChange struct {
	Path     string         `json:"path"`
	Diff     string         `json:"diff"`
	Kind     FileChangeKind `json:"kind"`
	MovePath *string        `json:"move_path"`
}

// Tool observations retain native provenance. Command parsing is advisory;
// neither these records nor a completed tool grant permission to execute more
// work or imply that an entire turn succeeded. Native aggregate output may be
// truncated independently from streamed deltas, so retain both observations.
type Tool struct {
	ID      string
	Kind    ToolKind
	Status  ToolStatus
	Command *CommandExecution
	Changes []FileChange
}

type ToolInput struct {
	ProcessID string
	Text      string
}

func (c *Client) observeToolUpdateLocked(native nativewire.Event) (Event, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil {
		return Event{}, incompatible()
	}
	var params struct {
		ThreadID  domain.ID         `json:"threadId"`
		TurnID    domain.ID         `json:"turnId"`
		ItemID    string            `json:"itemId"`
		Delta     *string           `json:"delta"`
		Changes   []json.RawMessage `json:"changes"`
		ProcessID *string           `json:"processId"`
		Input     *string           `json:"stdin"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || domain.Text(params.ItemID, "native tool identity", 1024, true) != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	result := Event{ThreadID: c.thread, TurnID: params.TurnID, ItemID: params.ItemID, Correlated: known, Late: turn.Turn.Status.terminal()}
	allowed := []string{"threadId", "turnId", "itemId"}
	switch native.Method {
	case "item/commandExecution/outputDelta":
		allowed = append(allowed, "delta")
		if params.Delta == nil || domain.Text(*params.Delta, "native command output", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		result.Kind, result.TextDelta = ToolOutputEvent, *params.Delta
	case "item/fileChange/patchUpdated":
		allowed = append(allowed, "changes")
		changes, err := decodeFileChanges(params.Changes)
		if err != nil {
			return Event{}, err
		}
		result.Kind, result.Tool = ToolPatchEvent, &Tool{ID: params.ItemID, Kind: PatchTool, Changes: changes}
	case "item/commandExecution/terminalInteraction":
		allowed = append(allowed, "processId", "stdin")
		if params.ProcessID == nil || params.Input == nil || domain.Text(*params.ProcessID, "native process observation", 1024, true) != nil || domain.Text(*params.Input, "native terminal input", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		result.Kind, result.ToolInput = ToolInputEvent, &ToolInput{ProcessID: *params.ProcessID, Text: *params.Input}
	default:
		return Event{}, incompatible()
	}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return Event{}, incompatible()
		}
	}
	return result, nil
}

func decodeTool(raw json.RawMessage, kind string, completed bool) (*Tool, error) {
	result := &Tool{}
	switch kind {
	case "commandExecution":
		var item struct {
			Type             string            `json:"type"`
			ID               string            `json:"id"`
			Status           ToolStatus        `json:"status"`
			Command          *string           `json:"command"`
			Cwd              *string           `json:"cwd"`
			Source           json.RawMessage   `json:"source"`
			Actions          []json.RawMessage `json:"commandActions"`
			ProcessID        *string           `json:"processId"`
			AggregatedOutput *string           `json:"aggregatedOutput"`
			ExitCode         *int32            `json:"exitCode"`
			DurationMS       *int64            `json:"durationMs"`
			PluginID         *string           `json:"pluginId"`
			ScriptPath       *string           `json:"scriptPath"`
		}
		if domain.Decode(raw, &item) != nil || item.Type != kind || item.Command == nil || item.Cwd == nil || item.Actions == nil || len(item.Actions) > 1024 || domain.Text(*item.Command, "native command", nativewire.MaxFrame, true) != nil || domain.Text(*item.Cwd, "native working directory", 4096, true) != nil || (item.DurationMS != nil && *item.DurationMS < 0) {
			return nil, incompatible()
		}
		var source CommandSource
		if len(item.Source) == 0 {
			source = AgentCommand
		} else if json.Unmarshal(item.Source, &source) != nil {
			return nil, incompatible()
		}
		if !slices.Contains([]CommandSource{AgentCommand, UserShellCommand, ExecStartupCommand, ExecInputCommand}, source) {
			return nil, incompatible()
		}
		for _, field := range []struct {
			value *string
			limit int
		}{{item.ProcessID, 1024}, {item.AggregatedOutput, nativewire.MaxFrame}, {item.PluginID, 1024}, {item.ScriptPath, 4096}} {
			if field.value != nil && domain.Text(*field.value, "native command observation", field.limit, false) != nil {
				return nil, incompatible()
			}
		}
		command := &CommandExecution{Command: *item.Command, Cwd: *item.Cwd, Source: source, Actions: []CommandAction{}, ProcessID: item.ProcessID, AggregatedOutput: item.AggregatedOutput, ExitCode: item.ExitCode, DurationMS: item.DurationMS, PluginID: item.PluginID, ScriptPath: item.ScriptPath}
		for _, raw := range item.Actions {
			action, err := decodeCommandAction(raw)
			if err != nil {
				return nil, err
			}
			command.Actions = append(command.Actions, action)
		}
		result.ID, result.Kind, result.Status, result.Command = item.ID, CommandTool, item.Status, command
	case "fileChange":
		var item struct {
			Type    string            `json:"type"`
			ID      string            `json:"id"`
			Status  ToolStatus        `json:"status"`
			Changes []json.RawMessage `json:"changes"`
		}
		if domain.Decode(raw, &item) != nil || item.Type != kind {
			return nil, incompatible()
		}
		changes, err := decodeFileChanges(item.Changes)
		if err != nil {
			return nil, err
		}
		result.ID, result.Kind, result.Status, result.Changes = item.ID, PatchTool, item.Status, changes
	default:
		return nil, incompatible()
	}
	if domain.Text(result.ID, "native tool identity", 1024, true) != nil || !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed, ToolDeclined}, result.Status) || completed == (result.Status == ToolRunning) {
		return nil, incompatible()
	}
	return result, nil
}

func decodeCommandAction(raw json.RawMessage) (CommandAction, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil {
		return CommandAction{}, incompatible()
	}
	var item struct {
		Type    CommandActionKind `json:"type"`
		Command *string           `json:"command"`
		Name    *string           `json:"name"`
		Path    *string           `json:"path"`
		Query   *string           `json:"query"`
	}
	if domain.Decode(raw, &item) != nil || item.Command == nil || domain.Text(*item.Command, "native parsed command", nativewire.MaxFrame, true) != nil {
		return CommandAction{}, incompatible()
	}
	for _, field := range []*string{item.Name, item.Path, item.Query} {
		if field != nil && domain.Text(*field, "native command action", 4096, false) != nil {
			return CommandAction{}, incompatible()
		}
	}
	switch item.Type {
	case ReadCommandAction:
		if item.Name == nil || item.Path == nil || item.Query != nil {
			return CommandAction{}, incompatible()
		}
	case ListCommandAction:
		if item.Name != nil || item.Query != nil {
			return CommandAction{}, incompatible()
		}
	case SearchCommandAction:
		if item.Name != nil {
			return CommandAction{}, incompatible()
		}
	case UnknownCommandAction:
		if item.Name != nil || item.Path != nil || item.Query != nil {
			return CommandAction{}, incompatible()
		}
	default:
		return CommandAction{}, incompatible()
	}
	for name := range fields {
		if (name == "name" && item.Type != ReadCommandAction) || (name == "query" && item.Type != SearchCommandAction) || (name == "path" && item.Type == UnknownCommandAction) {
			return CommandAction{}, incompatible()
		}
	}
	return CommandAction{Kind: item.Type, Command: *item.Command, Name: item.Name, Path: item.Path, Query: item.Query}, nil
}

func decodeFileChanges(raw []json.RawMessage) ([]FileChange, error) {
	if raw == nil || len(raw) > 1024 {
		return nil, incompatible()
	}
	result := make([]FileChange, 0, len(raw))
	for _, value := range raw {
		var item struct {
			Path *string `json:"path"`
			Diff *string `json:"diff"`
			Kind struct {
				Type     FileChangeKind `json:"type"`
				MovePath *string        `json:"move_path"`
			} `json:"kind"`
		}
		if domain.Decode(value, &item) != nil || item.Path == nil || item.Diff == nil || domain.Text(*item.Path, "native changed path", 4096, true) != nil || domain.Text(*item.Diff, "native patch", nativewire.MaxFrame, false) != nil || !slices.Contains([]FileChangeKind{AddedFile, DeletedFile, UpdatedFile}, item.Kind.Type) || (item.Kind.MovePath != nil && (item.Kind.Type != UpdatedFile || domain.Text(*item.Kind.MovePath, "native move path", 4096, true) != nil)) {
			return nil, incompatible()
		}
		if item.Kind.Type != UpdatedFile {
			var fields struct {
				Path json.RawMessage            `json:"path"`
				Diff json.RawMessage            `json:"diff"`
				Kind map[string]json.RawMessage `json:"kind"`
			}
			if domain.Decode(value, &fields) != nil || len(fields.Kind) != 1 {
				return nil, incompatible()
			}
		}
		result = append(result, FileChange{Path: *item.Path, Diff: *item.Diff, Kind: item.Kind.Type, MovePath: item.Kind.MovePath})
	}
	return result, nil
}
