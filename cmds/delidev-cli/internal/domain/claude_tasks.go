package domain

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
)

type ClaudeTaskEventKind string
type ClaudeTaskStatus string
type ClaudeTaskType string
type ClaudeTaskHistoryKind string

const ClaudeInlineBashTaskHistory ClaudeTaskHistoryKind = "inline-bash"

const (
	ClaudeTaskStarted            ClaudeTaskEventKind = "task_started"
	ClaudeTaskProgressed         ClaudeTaskEventKind = "task_progress"
	ClaudeTaskUpdated            ClaudeTaskEventKind = "task_updated"
	ClaudeTaskNotified           ClaudeTaskEventKind = "task_notification"
	ClaudeBackgroundTasksChanged ClaudeTaskEventKind = "background_tasks_changed"
	ClaudeTaskPending            ClaudeTaskStatus    = "pending"
	ClaudeTaskRunning            ClaudeTaskStatus    = "running"
	ClaudeTaskPaused             ClaudeTaskStatus    = "paused"
	ClaudeTaskCompleted          ClaudeTaskStatus    = "completed"
	ClaudeTaskFailed             ClaudeTaskStatus    = "failed"
	ClaudeTaskKilled             ClaudeTaskStatus    = "killed"
	ClaudeTaskStopped            ClaudeTaskStatus    = "stopped"
	ClaudeLocalBashTask          ClaudeTaskType      = "local_bash"
)

func (s ClaudeTaskStatus) Terminal() bool {
	return slices.Contains([]ClaudeTaskStatus{ClaudeTaskCompleted, ClaudeTaskFailed, ClaudeTaskKilled, ClaudeTaskStopped}, s)
}
func sameClaudeTaskTerminal(a, b ClaudeTaskStatus) bool {
	return a == b || a == ClaudeTaskKilled && b == ClaudeTaskStopped || a == ClaudeTaskStopped && b == ClaudeTaskKilled
}
func validClaudeTaskCount(v ClaudeProgressCount) bool {
	value, err := strconv.ParseUint(string(v), 10, 64)
	return err == nil && strconv.FormatUint(value, 10) == string(v)
}
func claudeTaskTexts(values ...*string) bool {
	for _, value := range values {
		if value != nil && Text(*value, "native task metadata", MaxMessageText, false) != nil {
			return false
		}
	}
	return true
}

// These are overlapping task-local cumulative observations, never additive
// provider usage or an independent authority to bill an account.
type ClaudeTaskUsage struct {
	TotalTokens ClaudeProgressCount `json:"total_tokens"`
	ToolUses    ClaudeProgressCount `json:"tool_uses"`
	DurationMS  ClaudeProgressCount `json:"duration_ms"`
}
type ClaudeTaskPatch struct {
	Status        *ClaudeTaskStatus    `json:"status,omitempty"`
	Description   *string              `json:"description,omitempty"`
	EndTime       *ClaudeProgressCount `json:"end_time,omitempty"`
	TotalPausedMS *ClaudeProgressCount `json:"total_paused_ms,omitempty"`
	Error         *string              `json:"error,omitempty"`
	Backgrounded  *bool                `json:"is_backgrounded,omitempty"`
}
type ClaudeBackgroundTask struct {
	ID          string         `json:"task_id"`
	Type        ClaudeTaskType `json:"task_type"`
	Description string         `json:"description"`
	Ambient     *bool          `json:"ambient,omitempty"`
}

type ClaudeTaskObservation struct {
	Kind           ClaudeTaskEventKind     `json:"kind"`
	ID             string                  `json:"task_id,omitempty"`
	Tool           *ClaudeToolReference    `json:"tool,omitempty"`
	Type           *ClaudeTaskType         `json:"task_type,omitempty"`
	Description    *string                 `json:"description,omitempty"`
	Backgrounded   *bool                   `json:"is_backgrounded,omitempty"`
	SkipTranscript *bool                   `json:"skip_transcript,omitempty"`
	Ambient        *bool                   `json:"ambient,omitempty"`
	Status         *ClaudeTaskStatus       `json:"status,omitempty"`
	Reason         *string                 `json:"reason,omitempty"`
	OutputFile     *string                 `json:"output_file,omitempty"`
	Summary        *string                 `json:"summary,omitempty"`
	LastTool       *string                 `json:"last_tool_name,omitempty"`
	Usage          *ClaudeTaskUsage        `json:"usage,omitempty"`
	Patch          *ClaudeTaskPatch        `json:"patch,omitempty"`
	Background     *[]ClaudeBackgroundTask `json:"background,omitempty"`
}

func (v ClaudeTaskObservation) Validate() error {
	if v.Tool != nil && v.Tool.Validate() != nil || !claudeTaskTexts(v.Description, v.OutputFile, v.Summary, v.LastTool) {
		return invalidClaudeProgress()
	}
	if v.Usage != nil && (!validClaudeTaskCount(v.Usage.TotalTokens) || !validClaudeTaskCount(v.Usage.ToolUses) || !validClaudeTaskCount(v.Usage.DurationMS)) {
		return invalidClaudeProgress()
	}
	allowed := ClaudeTaskObservation{Kind: v.Kind, ID: v.ID}
	if v.Kind != ClaudeBackgroundTasksChanged && Text(v.ID, "native task identity", 1024, true) != nil {
		return invalidClaudeProgress()
	}
	switch v.Kind {
	case ClaudeTaskStarted:
		allowed.Tool, allowed.Type, allowed.Description, allowed.Backgrounded, allowed.SkipTranscript, allowed.Ambient = v.Tool, v.Type, v.Description, v.Backgrounded, v.SkipTranscript, v.Ambient
		if v.Tool == nil || v.Tool.Name != "Bash" || v.Type == nil || *v.Type != ClaudeLocalBashTask || v.Description == nil {
			return invalidClaudeProgress()
		}
	case ClaudeTaskProgressed:
		allowed.Tool, allowed.Description, allowed.Usage, allowed.LastTool, allowed.Summary = v.Tool, v.Description, v.Usage, v.LastTool, v.Summary
		if v.Description == nil || v.Usage == nil {
			return invalidClaudeProgress()
		}
	case ClaudeTaskUpdated:
		allowed.Patch = v.Patch
		p := v.Patch
		if p == nil || !claudeTaskTexts(p.Description, p.Error) || p.Status != nil && !slices.Contains([]ClaudeTaskStatus{ClaudeTaskPending, ClaudeTaskRunning, ClaudeTaskPaused, ClaudeTaskCompleted, ClaudeTaskFailed, ClaudeTaskKilled}, *p.Status) || p.EndTime != nil && !validClaudeTaskCount(*p.EndTime) || p.TotalPausedMS != nil && !validClaudeTaskCount(*p.TotalPausedMS) {
			return invalidClaudeProgress()
		}
	case ClaudeTaskNotified:
		allowed.Tool, allowed.Status, allowed.Reason, allowed.OutputFile, allowed.Summary, allowed.Usage, allowed.SkipTranscript, allowed.Ambient = v.Tool, v.Status, v.Reason, v.OutputFile, v.Summary, v.Usage, v.SkipTranscript, v.Ambient
		if v.Status == nil || !slices.Contains([]ClaudeTaskStatus{ClaudeTaskCompleted, ClaudeTaskFailed, ClaudeTaskStopped}, *v.Status) || v.OutputFile == nil || v.Summary == nil || v.Reason != nil && (*v.Reason != "worker_restart" || *v.Status != ClaudeTaskStopped) {
			return invalidClaudeProgress()
		}
	case ClaudeBackgroundTasksChanged:
		allowed.ID, allowed.Background = "", v.Background
		if v.Background == nil || *v.Background == nil || len(*v.Background) > 128 {
			return invalidClaudeProgress()
		}
		seen := map[string]bool{}
		for _, task := range *v.Background {
			if Text(task.ID, "native task identity", 1024, true) != nil || Text(string(task.Type), "native task type", 256, true) != nil || Text(task.Description, "native task description", MaxMessageText, false) != nil || seen[task.ID] {
				return invalidClaudeProgress()
			}
			seen[task.ID] = true
		}
	default:
		return invalidClaudeProgress()
	}
	if !reflect.DeepEqual(v, allowed) {
		return invalidClaudeProgress()
	}
	return nil
}

// Keep only bounded ownership/state metadata in the current execution. Original
// descriptions, usage, paths and summaries live in immutable progress records.
type ClaudeTaskState struct {
	History      ClaudeTaskHistoryKind `json:"history_kind,omitempty"`
	Tool         ClaudeToolReference   `json:"tool"`
	Status       ClaudeTaskStatus      `json:"status"`
	Notified     bool                  `json:"notified"`
	Backgrounded *bool                 `json:"is_backgrounded,omitempty"`
}
type ClaudeTasksState struct {
	Tasks      map[string]ClaudeTaskState `json:"tasks"`
	Background []string                   `json:"background"`
}

func (s *ClaudeTasksState) Closed() bool {
	if s == nil {
		return true
	}
	if len(s.Background) != 0 {
		return false
	}
	for _, task := range s.Tasks {
		if !task.Status.Terminal() {
			return false
		}
	}
	return true
}

// This is a public candidate only. Native history must independently prove the
// original completed inline Bash result and retained task observation hashes.
func (s *ClaudeTasksState) InlineBashHistoryReady() bool {
	if s == nil {
		return true
	}
	if len(s.Background) != 0 {
		return false
	}
	for id, task := range s.Tasks {
		if Text(id, "native task identity", 1024, true) != nil || task.History != ClaudeInlineBashTaskHistory || task.Status != ClaudeTaskCompleted || !task.Notified || task.Tool.Name != "Bash" || task.Tool.Validate() != nil || task.Backgrounded != nil && *task.Backgrounded {
			return false
		}
	}
	return true
}

func (s *ClaudeTasksState) OwnsRunningTask(id string, tool ClaudeToolReference) bool {
	if s == nil {
		return false
	}
	task, ok := s.Tasks[id]
	return ok && task.Tool == tool && !task.Status.Terminal()
}

func ApplyClaudeTask(prior *ClaudeTasksState, v ClaudeTaskObservation) (*ClaudeTasksState, error) {
	if v.Validate() != nil {
		return nil, invalidClaudeProgress()
	}
	next := &ClaudeTasksState{Tasks: map[string]ClaudeTaskState{}, Background: []string{}}
	if prior != nil {
		next.Tasks, next.Background = maps.Clone(prior.Tasks), slices.Clone(prior.Background)
		if next.Tasks == nil {
			next.Tasks = map[string]ClaudeTaskState{}
		}
	}
	if v.Kind == ClaudeBackgroundTasksChanged {
		next.Background = make([]string, 0, len(*v.Background))
		for _, task := range *v.Background {
			next.Background = append(next.Background, task.ID)
			if retained, exists := next.Tasks[task.ID]; exists {
				retained.History = ""
				next.Tasks[task.ID] = retained
			}
		}
	} else {
		current, exists := next.Tasks[v.ID]
		if v.Kind == ClaudeTaskStarted {
			if exists || len(next.Tasks) >= 4096 {
				return nil, invalidClaudeProgress()
			}
			active := 0
			for _, old := range next.Tasks {
				if old.Tool.ID == v.Tool.ID || old.Tool.NativeID == v.Tool.NativeID {
					return nil, invalidClaudeProgress()
				}
				if !old.Status.Terminal() {
					active++
				}
			}
			if active >= 128 {
				return nil, invalidClaudeProgress()
			}
			current = ClaudeTaskState{Tool: *v.Tool, Status: ClaudeTaskRunning, Backgrounded: v.Backgrounded}
			if (v.Backgrounded == nil || !*v.Backgrounded) && (v.SkipTranscript == nil || !*v.SkipTranscript) && (v.Ambient == nil || !*v.Ambient) && !slices.Contains(next.Background, v.ID) {
				current.History = ClaudeInlineBashTaskHistory
			}
		} else {
			if !exists || v.Tool != nil && *v.Tool != current.Tool {
				return nil, invalidClaudeProgress()
			}
			switch v.Kind {
			case ClaudeTaskProgressed:
				if current.Status.Terminal() {
					return nil, invalidClaudeProgress()
				}
			case ClaudeTaskUpdated:
				if v.Patch.Status != nil {
					if current.Status.Terminal() && !sameClaudeTaskTerminal(current.Status, *v.Patch.Status) {
						return nil, invalidClaudeProgress()
					}
					current.Status = *v.Patch.Status
					if current.Status.Terminal() && current.Status != ClaudeTaskCompleted {
						current.History = ""
					}
				}
				if v.Patch.Backgrounded != nil {
					current.Backgrounded = v.Patch.Backgrounded
					if *v.Patch.Backgrounded {
						current.History = ""
					}
				}
			case ClaudeTaskNotified:
				if current.Notified || current.Status.Terminal() && !sameClaudeTaskTerminal(current.Status, *v.Status) {
					return nil, invalidClaudeProgress()
				}
				current.Status, current.Notified = *v.Status, true
				if current.Status != ClaudeTaskCompleted || v.Reason != nil || v.SkipTranscript != nil && *v.SkipTranscript || v.Ambient != nil && *v.Ambient {
					current.History = ""
				}
			}
		}
		next.Tasks[v.ID] = current
	}
	encoded, err := json.Marshal(next)
	if err != nil || len(encoded) > 768<<10 {
		return nil, Fail(ResourceExhausted, "Native task ownership exceeds its retained limit.", "Preserve the original execution and reconcile it before further input.")
	}
	return next, nil
}
