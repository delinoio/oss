package claude

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type TaskEventKind string
type TaskStatus string
type TaskType string

const (
	TaskStarted            TaskEventKind = "task_started"
	TaskProgress           TaskEventKind = "task_progress"
	TaskUpdated            TaskEventKind = "task_updated"
	TaskNotification       TaskEventKind = "task_notification"
	BackgroundTasksChanged TaskEventKind = "background_tasks_changed"
	TaskPending            TaskStatus    = "pending"
	TaskRunning            TaskStatus    = "running"
	TaskPaused             TaskStatus    = "paused"
	TaskCompleted          TaskStatus    = "completed"
	TaskFailed             TaskStatus    = "failed"
	TaskKilled             TaskStatus    = "killed"
	TaskStopped            TaskStatus    = "stopped"
	LocalAgentTask         TaskType      = "local_agent"
	LocalBashTask          TaskType      = "local_bash"
	LocalWorkflowTask      TaskType      = "local_workflow"
)

func (s TaskStatus) terminal() bool {
	return slices.Contains([]TaskStatus{TaskCompleted, TaskFailed, TaskKilled, TaskStopped}, s)
}
func sameTaskTerminal(a, b TaskStatus) bool {
	return a == b || (a == TaskKilled && b == TaskStopped) || (a == TaskStopped && b == TaskKilled)
}

// These are native task-local cumulative observations, not response counters or
// billable usage. The main-loop and model ledgers can overlap this evidence.
type TaskUsage struct {
	TotalTokens uint64 `json:"total_tokens"`
	ToolUses    uint64 `json:"tool_uses"`
	DurationMS  uint64 `json:"duration_ms"`
}

func (u *TaskUsage) UnmarshalJSON(raw []byte) error {
	var value struct {
		Tokens   *uint64 `json:"total_tokens"`
		Tools    *uint64 `json:"tool_uses"`
		Duration *uint64 `json:"duration_ms"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Tokens == nil || value.Tools == nil || value.Duration == nil {
		return lifecycleUncertain()
	}
	*u = TaskUsage{*value.Tokens, *value.Tools, *value.Duration}
	return nil
}

type TaskPatch struct {
	Status        *TaskStatus `json:"status,omitempty"`
	Description   *string     `json:"description,omitempty"`
	EndTime       *uint64     `json:"end_time,omitempty"`
	TotalPausedMS *uint64     `json:"total_paused_ms,omitempty"`
	Error         *string     `json:"error,omitempty"`
	Backgrounded  *bool       `json:"is_backgrounded,omitempty"`
}

func (p *TaskPatch) UnmarshalJSON(raw []byte) error {
	type plain TaskPatch
	var value plain
	if decodeNativeObject(raw, &value) != nil || (value.Status != nil && !slices.Contains([]TaskStatus{TaskPending, TaskRunning, TaskPaused, TaskCompleted, TaskFailed, TaskKilled}, *value.Status)) || !taskTexts(value.Description, value.Error) {
		return lifecycleUncertain()
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, field := range fields {
		if bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			return lifecycleUncertain()
		}
	}
	*p = TaskPatch(value)
	return nil
}

// Snapshot membership is deliberately independent of lifecycle ownership. The
// native level signal may precede task_started and omit foreground tasks; its
// absence never acknowledges completion or abandons an observed descendant.
type BackgroundTask struct {
	ID          string   `json:"task_id"`
	Type        TaskType `json:"task_type"`
	Description string   `json:"description"`
	Ambient     *bool    `json:"ambient,omitempty"`
}

func (t *BackgroundTask) UnmarshalJSON(raw []byte) error {
	type plain BackgroundTask
	var value plain
	if decodeNativeObject(raw, &value) != nil || domain.Text(value.ID, "native task identity", 1024, true) != nil || domain.Text(string(value.Type), "native task type", 256, true) != nil || domain.Text(value.Description, "native task description", domain.MaxMessageText, false) != nil {
		return lifecycleUncertain()
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if len(fields["description"]) == 0 || bytes.Equal(bytes.TrimSpace(fields["description"]), []byte("null")) {
		return lifecycleUncertain()
	}
	*t = BackgroundTask(value)
	return nil
}

type NativeTaskObservation struct {
	Kind           TaskEventKind
	ID             string
	ToolID         *string
	Type           *TaskType
	Description    *string
	SubagentType   *string
	Prompt         *string
	WorkflowName   *string
	Backgrounded   *bool
	SpawnDepth     *uint32
	SkipTranscript *bool
	Ambient        *bool
	Status         *TaskStatus
	Reason         *string
	OutputFile     *string
	Summary        *string
	LastTool       *string
	Usage          *TaskUsage
	Patch          *TaskPatch
	Background     []BackgroundTask
}

type nativeTaskState struct {
	tool         string
	kind         TaskType
	agentType    string
	status       TaskStatus
	notified     bool
	backgrounded bool
}

func taskTexts(values ...*string) bool {
	for _, value := range values {
		if value != nil && domain.Text(*value, "native task metadata", domain.MaxMessageText, false) != nil {
			return false
		}
	}
	return true
}

func (b *ExecutionBinding) observeTask(kind TaskEventKind, raw []byte) (*NativeTaskObservation, error) {
	if !b.initialized || !b.accepted {
		return nil, lifecycleUncertain()
	}
	if b.tasks == nil {
		b.tasks = map[string]nativeTaskState{}
	}
	var value struct {
		Type           string           `json:"type"`
		Subtype        TaskEventKind    `json:"subtype"`
		Session        domain.ID        `json:"session_id"`
		UUID           string           `json:"uuid"`
		ID             string           `json:"task_id,omitempty"`
		ToolID         *string          `json:"tool_use_id,omitempty"`
		TaskType       *TaskType        `json:"task_type,omitempty"`
		Description    *string          `json:"description,omitempty"`
		SubagentType   *string          `json:"subagent_type,omitempty"`
		Prompt         *string          `json:"prompt,omitempty"`
		WorkflowName   *string          `json:"workflow_name,omitempty"`
		Backgrounded   *bool            `json:"is_backgrounded,omitempty"`
		SpawnDepth     *uint32          `json:"spawn_depth,omitempty"`
		SkipTranscript *bool            `json:"skip_transcript,omitempty"`
		Ambient        *bool            `json:"ambient,omitempty"`
		Status         *TaskStatus      `json:"status,omitempty"`
		Reason         *string          `json:"reason,omitempty"`
		OutputFile     *string          `json:"output_file,omitempty"`
		Summary        *string          `json:"summary,omitempty"`
		LastTool       *string          `json:"last_tool_name,omitempty"`
		Usage          *TaskUsage       `json:"usage,omitempty"`
		Patch          *TaskPatch       `json:"patch,omitempty"`
		Tasks          []BackgroundTask `json:"tasks,omitempty"`
	}
	if decodeNativeObject(raw, &value) != nil || value.Subtype != kind || !taskTexts(value.Description, value.SubagentType, value.Prompt, value.WorkflowName, value.OutputFile, value.Summary, value.LastTool) {
		return nil, lifecycleUncertain()
	}
	if value.SubagentType != nil && domain.Text(*value.SubagentType, "native subagent type", 256, true) != nil {
		return nil, lifecycleUncertain()
	}
	// Check presence, including explicit null: a field owned by another event
	// cannot acquire a different meaning through this shared decoding shape.
	allowed := map[string]bool{"type": true, "subtype": true, "session_id": true, "uuid": true}
	var extra []string
	switch kind {
	case TaskStarted:
		extra = []string{"task_id", "tool_use_id", "task_type", "description", "subagent_type", "prompt", "workflow_name", "is_backgrounded", "spawn_depth", "skip_transcript", "ambient"}
	case TaskProgress:
		extra = []string{"task_id", "tool_use_id", "description", "subagent_type", "usage", "last_tool_name", "summary"}
	case TaskUpdated:
		extra = []string{"task_id", "patch"}
	case TaskNotification:
		extra = []string{"task_id", "tool_use_id", "status", "reason", "output_file", "summary", "usage", "skip_transcript", "ambient"}
	case BackgroundTasksChanged:
		extra = []string{"tasks"}
	default:
		return nil, lifecycleUncertain()
	}
	for _, key := range extra {
		allowed[key] = true
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for key := range fields {
		if !allowed[key] {
			return nil, lifecycleUncertain()
		}
	}
	observation := &NativeTaskObservation{Kind: kind, ID: value.ID, ToolID: value.ToolID, Type: value.TaskType, Description: value.Description, SubagentType: value.SubagentType, Prompt: value.Prompt, WorkflowName: value.WorkflowName, Backgrounded: value.Backgrounded, SpawnDepth: value.SpawnDepth, SkipTranscript: value.SkipTranscript, Ambient: value.Ambient, Status: value.Status, Reason: value.Reason, OutputFile: value.OutputFile, Summary: value.Summary, LastTool: value.LastTool, Usage: value.Usage, Patch: value.Patch, Background: value.Tasks}
	if kind == BackgroundTasksChanged {
		if value.Tasks == nil || len(value.Tasks) > 128 {
			return nil, lifecycleUncertain()
		}
		seen := map[string]bool{}
		for _, task := range value.Tasks {
			if seen[task.ID] {
				return nil, lifecycleUncertain()
			}
			seen[task.ID] = true
		}
		// Retain only identities, independently of returned display metadata.
		b.backgroundTasks = seen
		return observation, nil
	}
	if domain.Text(value.ID, "native task identity", 1024, true) != nil || (value.ToolID != nil && domain.Text(*value.ToolID, "native task tool identity", 1024, true) != nil) {
		return nil, lifecycleUncertain()
	}
	retained, exists := b.tasks[value.ID]
	if kind == TaskStarted {
		if exists || len(b.tasks) >= 4096 || value.Description == nil || (value.TaskType != nil && domain.Text(string(*value.TaskType), "native task type", 256, true) != nil) || (value.SpawnDepth != nil && (*value.SpawnDepth == 0 || *value.SpawnDepth > 128)) {
			return nil, lifecycleUncertain()
		}
		if b.finished && !b.continuing && (value.ToolID == nil || !b.activeChildTask(b.content.tools[*value.ToolID].parent)) {
			return nil, lifecycleUncertain()
		}
		open := 0
		for _, task := range b.tasks {
			if !task.status.terminal() {
				open++
			}
		}
		if open >= 128 {
			return nil, lifecycleUncertain()
		}
		if value.TaskType != nil {
			retained.kind = *value.TaskType
		}
		if value.SubagentType != nil {
			retained.agentType = *value.SubagentType
		}
		if value.ToolID != nil {
			tool, ok := b.content.tools[*value.ToolID]
			if !ok || tool.finished {
				return nil, lifecycleUncertain()
			}
			if retained.kind == LocalAgentTask && tool.name != "Agent" && tool.name != "Task" {
				return nil, lifecycleUncertain()
			}
			if retained.kind == LocalBashTask && tool.name != "Bash" {
				return nil, lifecycleUncertain()
			}
			for _, old := range b.tasks {
				if old.tool == *value.ToolID {
					return nil, lifecycleUncertain()
				}
			}
			retained.tool = *value.ToolID
		}
		retained.status = TaskRunning
		if value.Backgrounded != nil {
			retained.backgrounded = *value.Backgrounded
		}
	} else {
		if !exists || (value.ToolID != nil && *value.ToolID != retained.tool) || (value.SubagentType != nil && retained.agentType != "" && *value.SubagentType != retained.agentType) {
			return nil, lifecycleUncertain()
		}
		switch kind {
		case TaskProgress:
			if retained.status.terminal() || value.Description == nil || value.Usage == nil {
				return nil, lifecycleUncertain()
			}
		case TaskUpdated:
			if value.Patch == nil {
				return nil, lifecycleUncertain()
			}
			if value.Patch.Status != nil {
				if retained.status.terminal() && !sameTaskTerminal(retained.status, *value.Patch.Status) {
					return nil, lifecycleUncertain()
				}
				retained.status = *value.Patch.Status
			}
			if value.Patch.Backgrounded != nil {
				retained.backgrounded = *value.Patch.Backgrounded
			}
		case TaskNotification:
			if retained.notified || value.Status == nil || !slices.Contains([]TaskStatus{TaskCompleted, TaskFailed, TaskStopped}, *value.Status) || value.OutputFile == nil || value.Summary == nil || (value.Reason != nil && (*value.Reason != "worker_restart" || *value.Status != TaskStopped)) || (retained.status.terminal() && !sameTaskTerminal(retained.status, *value.Status)) {
				return nil, lifecycleUncertain()
			}
			retained.status = *value.Status
			retained.notified = true
		}
	}
	if retained.status.terminal() && retained.tool != "" {
		if b.content.active[retained.tool] != nil || b.hasOpenServerChild(retained.tool) {
			return nil, lifecycleUncertain()
		}
		for _, child := range b.content.tools {
			if child.parent == retained.tool && !child.finished {
				return nil, lifecycleUncertain()
			}
		}
		for id, child := range b.tasks {
			if id != value.ID && child.tool != "" && !child.status.terminal() && b.content.tools[child.tool].parent == retained.tool {
				return nil, lifecycleUncertain()
			}
		}
	}
	b.tasks[value.ID] = retained
	if b.logger != nil {
		b.logger.Debug("Claude Code task lifecycle observed", "owner_id", b.owner, "event", kind, "status", retained.status)
	}
	return observation, nil
}

// Known work counts describe only retained observations. Even an empty set
// cannot prove a native run boundary: a settled task may enqueue a synthetic
// continuation after the original input's result. Process/history reconciliation
// must retain that separate uncertainty before dispatching another product input.
type NativeWorkObservation struct {
	PendingTasks    uint32
	BackgroundTasks uint32
}

func (b *ExecutionBinding) knownWork() NativeWorkObservation {
	value := NativeWorkObservation{BackgroundTasks: uint32(len(b.backgroundTasks))}
	for _, task := range b.tasks {
		if !task.status.terminal() {
			value.PendingTasks++
		}
	}
	return value
}

func (b *ExecutionBinding) activeChildTask(toolID string) bool {
	for _, task := range b.tasks {
		if task.tool == toolID && task.kind == LocalAgentTask && !task.status.terminal() {
			return true
		}
	}
	return false
}
