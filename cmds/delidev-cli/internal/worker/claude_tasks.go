package worker

import (
	"context"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func cloneClaudeTaskField[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func claudeTaskCount(value uint64) domain.ClaudeProgressCount {
	return domain.ClaudeProgressCount(strconv.FormatUint(value, 10))
}
func claudeOptionalTaskCount(value *uint64) *domain.ClaudeProgressCount {
	if value == nil {
		return nil
	}
	copy := claudeTaskCount(*value)
	return &copy
}

// Task mutations share the content outbox: ownership advances only after the
// original receipt is acknowledged, including an exact lost-ack replay. Native
// task metadata never grants process, file, approval or descendant authority.
func (c *ClaudeContentPublisher) PublishTaskObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	if o.Kind != claude.TaskObserved {
		return false, nil
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return true, err
	}
	if !c.inputPublished || o.SessionID != b.journal.SessionID || o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || o.Task == nil || o.NativeID == b.turn || o.NativeID == string(b.journal.InputID) || b.progressSeen[o.NativeID] || len(b.progressSeen) >= 65536 || !reflect.DeepEqual(o, claude.LifecycleObservation{Kind: o.Kind, SessionID: o.SessionID, InputID: o.InputID, TurnID: o.TurnID, NativeID: o.NativeID, Accepted: true, Task: o.Task, Native: o.Native}) {
		return true, b.block()
	}
	n := o.Task
	// Child/workflow semantics must have their own ownership composition. Do not
	// discard fields to reinterpret an unsupported task as an ordinary Bash task.
	if n.SubagentType != nil || n.Prompt != nil || n.WorkflowName != nil || n.SpawnDepth != nil {
		return true, b.block()
	}
	v := domain.ClaudeTaskObservation{Kind: domain.ClaudeTaskEventKind(n.Kind), ID: n.ID, Description: cloneClaudeTaskField(n.Description), Backgrounded: cloneClaudeTaskField(n.Backgrounded), SkipTranscript: cloneClaudeTaskField(n.SkipTranscript), Ambient: cloneClaudeTaskField(n.Ambient), Reason: cloneClaudeTaskField(n.Reason), OutputFile: cloneClaudeTaskField(n.OutputFile), Summary: cloneClaudeTaskField(n.Summary), LastTool: cloneClaudeTaskField(n.LastTool)}
	if n.Type != nil {
		value := domain.ClaudeTaskType(*n.Type)
		v.Type = &value
	}
	if n.Status != nil {
		value := domain.ClaudeTaskStatus(*n.Status)
		v.Status = &value
	}
	if n.ToolID != nil {
		tool, ok := c.tools[*n.ToolID]
		if !ok || tool.reference.Validate() != nil || n.Kind == claude.TaskStarted && (c.resultUsage || tool.state != domain.MessageStreaming || tool.content == nil || tool.content.Proposal == nil) {
			return true, b.block()
		}
		v.Tool = cloneClaudeTaskField(&tool.reference)
	}
	if n.Usage != nil {
		v.Usage = &domain.ClaudeTaskUsage{TotalTokens: claudeTaskCount(n.Usage.TotalTokens), ToolUses: claudeTaskCount(n.Usage.ToolUses), DurationMS: claudeTaskCount(n.Usage.DurationMS)}
	}
	if p := n.Patch; p != nil {
		v.Patch = &domain.ClaudeTaskPatch{Description: cloneClaudeTaskField(p.Description), EndTime: claudeOptionalTaskCount(p.EndTime), TotalPausedMS: claudeOptionalTaskCount(p.TotalPausedMS), Error: cloneClaudeTaskField(p.Error), Backgrounded: cloneClaudeTaskField(p.Backgrounded)}
		if p.Status != nil {
			value := domain.ClaudeTaskStatus(*p.Status)
			v.Patch.Status = &value
		}
	}
	if n.Background != nil {
		tasks := make([]domain.ClaudeBackgroundTask, 0, len(n.Background))
		for _, task := range n.Background {
			tasks = append(tasks, domain.ClaudeBackgroundTask{ID: task.ID, Type: domain.ClaudeTaskType(task.Type), Description: task.Description, Ambient: cloneClaudeTaskField(task.Ambient)})
		}
		v.Background = &tasks
	}
	next, err := domain.ApplyClaudeTask(c.tasks, v)
	if err != nil {
		return true, b.block()
	}
	u := &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: domain.ClaudeProgressObservation{NativeEventID: o.NativeID, Kind: domain.ClaudeTaskLifecycleProgress, InputAccepted: true, Task: &v}}
	if u.Validate() != nil {
		return true, b.block()
	}
	if b.progressSeen == nil {
		b.progressSeen = map[string]bool{}
	}
	b.progressSeen[o.NativeID] = true
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeProgressObserved, ClaudeProgress: u}, tasksNext: next}}
	return true, c.drain(ctx)
}
