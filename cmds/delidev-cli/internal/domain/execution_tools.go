package domain

import (
	"encoding/json"
	"slices"
)

type ToolKind string
type ToolStatus string
type CommandSource string
type CommandActionKind string
type FileChangeKind string

const (
	CommandTool ToolKind = "command"
	PatchTool   ToolKind = "patch"

	ToolRunning   ToolStatus = "running"
	ToolCompleted ToolStatus = "completed"
	ToolFailed    ToolStatus = "failed"
	ToolDeclined  ToolStatus = "declined"

	AgentCommand       CommandSource = "agent"
	UserShellCommand   CommandSource = "user-shell"
	ExecStartupCommand CommandSource = "exec-startup"
	ExecInputCommand   CommandSource = "exec-input"

	ReadCommandAction    CommandActionKind = "read"
	ListCommandAction    CommandActionKind = "list"
	SearchCommandAction  CommandActionKind = "search"
	UnknownCommandAction CommandActionKind = "unknown"

	AddedFile   FileChangeKind = "add"
	DeletedFile FileChangeKind = "delete"
	UpdatedFile FileChangeKind = "update"
)

type CommandAction struct {
	Kind    CommandActionKind `json:"kind"`
	Command string            `json:"command"`
	Name    *string           `json:"name,omitempty"`
	Path    *string           `json:"path,omitempty"`
	Query   *string           `json:"query,omitempty"`
}

// Parsed actions are native advisory observations, not executable authority.
type CommandObservation struct {
	Command          string          `json:"command"`
	Cwd              string          `json:"cwd"`
	Source           CommandSource   `json:"source"`
	Actions          []CommandAction `json:"actions"`
	ProcessID        *string         `json:"process_id"`
	AggregatedOutput *string         `json:"aggregated_output"`
	ExitCode         *int32          `json:"exit_code"`
	DurationMS       *int64          `json:"duration_ms"`
	PluginID         *string         `json:"plugin_id"`
	ScriptPath       *string         `json:"script_path"`
}

type FileChangeObservation struct {
	Path     string         `json:"path"`
	Diff     string         `json:"diff"`
	Kind     FileChangeKind `json:"kind"`
	MovePath *string        `json:"move_path"`
}

type ToolSnapshot struct {
	Kind    ToolKind                `json:"kind"`
	Status  ToolStatus              `json:"status"`
	Command *CommandObservation     `json:"command,omitempty"`
	Changes []FileChangeObservation `json:"changes"`
}

type ToolInputObservation struct {
	ProcessID string `json:"process_id"`
	Text      string `json:"text"`
}

type ExecutionToolUpdate struct {
	ID       ID                       `json:"id"`
	NativeID string                   `json:"native_id"`
	Snapshot *ToolSnapshot            `json:"snapshot,omitempty"`
	Delta    *string                  `json:"delta,omitempty"`
	Changes  *[]FileChangeObservation `json:"changes,omitempty"`
	Input    *ToolInputObservation    `json:"input,omitempty"`
}

// Preserve every patch/input observation with its execution sequence. Native
// aggregate command output can be truncated; it never replaces stream output.
type SequencedToolInput struct {
	Sequence uint64               `json:"sequence"`
	Input    ToolInputObservation `json:"input"`
}
type SequencedToolPatch struct {
	Sequence uint64                  `json:"sequence"`
	Changes  []FileChangeObservation `json:"changes"`
}
type ExecutionTool struct {
	Started   ToolSnapshot         `json:"started"`
	Completed *ToolSnapshot        `json:"completed,omitempty"`
	Output    *string              `json:"output"`
	Inputs    []SequencedToolInput `json:"inputs,omitempty"`
	Patches   []SequencedToolPatch `json:"patches,omitempty"`
}

func (k ExecutionEventKind) IsTool() bool {
	return slices.Contains([]ExecutionEventKind{ExecutionToolStarted, ExecutionToolCompleted, ExecutionToolOutput, ExecutionToolInput, ExecutionToolPatch}, k)
}

func invalidTool() error {
	return Fail(InvalidArgument, "Invalid native tool observation.", "Preserve the bounded typed native lifecycle and its exact ownership.")
}

func (u ExecutionToolUpdate) Validate(kind ExecutionEventKind) error {
	if u.ID.Validate() != nil || Text(u.NativeID, "native tool identity", 1024, true) != nil {
		return invalidTool()
	}
	if (kind != ExecutionToolStarted && kind != ExecutionToolCompleted && u.Snapshot != nil) || (kind != ExecutionToolOutput && u.Delta != nil) || (kind != ExecutionToolInput && u.Input != nil) || (kind != ExecutionToolPatch && u.Changes != nil) {
		return invalidTool()
	}
	switch kind {
	case ExecutionToolStarted, ExecutionToolCompleted:
		if u.Snapshot == nil || u.Snapshot.Validate() != nil || (kind == ExecutionToolStarted) != (u.Snapshot.Status == ToolRunning) {
			return invalidTool()
		}
	case ExecutionToolOutput:
		if u.Delta == nil || Text(*u.Delta, "native tool delta", MaxMessageText, false) != nil {
			return invalidTool()
		}
	case ExecutionToolInput:
		if u.Input == nil || Text(u.Input.ProcessID, "native process observation", 1024, true) != nil || Text(u.Input.Text, "native tool input", MaxMessageText, false) != nil {
			return invalidTool()
		}
	case ExecutionToolPatch:
		if u.Changes == nil || validateFileChanges(*u.Changes) != nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	// Leave bounded room for the execution envelope and durable outbox metadata.
	// Oversized evidence is rejected intact, never truncated to fit storage.
	raw, err := json.Marshal(u)
	if err != nil || len(raw) > 512<<10 {
		return Fail(ResourceExhausted, "The tool observation exceeds its publication bound.", "Retain native history and reconcile this observation without truncating it.")
	}
	return nil
}

func (s ToolSnapshot) Validate() error {
	if !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed, ToolDeclined}, s.Status) {
		return invalidTool()
	}
	switch s.Kind {
	case PatchTool:
		if s.Command != nil {
			return invalidTool()
		}
		return validateFileChanges(s.Changes)
	case CommandTool:
		if s.Changes != nil || s.Command == nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	c := s.Command
	if Text(c.Command, "native command", MaxMessageText, true) != nil || Text(c.Cwd, "native working directory", 4096, true) != nil || !slices.Contains([]CommandSource{AgentCommand, UserShellCommand, ExecStartupCommand, ExecInputCommand}, c.Source) || c.Actions == nil || len(c.Actions) > 1024 || (c.DurationMS != nil && *c.DurationMS < 0) {
		return invalidTool()
	}
	for _, field := range []struct {
		value *string
		limit int
	}{{c.ProcessID, 1024}, {c.AggregatedOutput, MaxMessageText}, {c.PluginID, 1024}, {c.ScriptPath, 4096}} {
		if field.value != nil && Text(*field.value, "native command observation", field.limit, false) != nil {
			return invalidTool()
		}
	}
	for _, a := range c.Actions {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateFileChanges(changes []FileChangeObservation) error {
	if changes == nil || len(changes) > 1024 {
		return invalidTool()
	}
	for _, c := range changes {
		if Text(c.Path, "native changed path", 4096, true) != nil || Text(c.Diff, "native patch", MaxMessageText, false) != nil || !slices.Contains([]FileChangeKind{AddedFile, DeletedFile, UpdatedFile}, c.Kind) || (c.MovePath != nil && (c.Kind != UpdatedFile || Text(*c.MovePath, "native move path", 4096, true) != nil)) {
			return invalidTool()
		}
	}
	return nil
}

func (a CommandAction) Validate() error {
	if Text(a.Command, "native parsed command", MaxMessageText, true) != nil {
		return invalidTool()
	}
	for _, field := range []*string{a.Name, a.Path, a.Query} {
		if field != nil && Text(*field, "native command action", 4096, false) != nil {
			return invalidTool()
		}
	}
	switch a.Kind {
	case ReadCommandAction:
		if a.Name == nil || a.Path == nil || a.Query != nil {
			return invalidTool()
		}
	case ListCommandAction:
		if a.Name != nil || a.Query != nil {
			return invalidTool()
		}
	case SearchCommandAction:
		if a.Name != nil {
			return invalidTool()
		}
	case UnknownCommandAction:
		if a.Name != nil || a.Path != nil || a.Query != nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	return nil
}
