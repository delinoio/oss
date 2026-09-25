package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func taskEvent(t *testing.T, b *ExecutionBinding, kind TaskEventKind, fields map[string]any) StreamEvent {
	t.Helper()
	fields["subtype"] = kind
	return lifecycleMessage(t, b, "system", fields)
}
func taskStart(t *testing.T, b *ExecutionBinding, id, tool string) StreamEvent {
	t.Helper()
	return taskEvent(t, b, TaskStarted, map[string]any{"task_id": id, "tool_use_id": tool, "task_type": LocalAgentTask, "description": "Child fixture", "prompt": "Private child prompt", "subagent_type": "general-purpose"})
}
func taskFixture(t *testing.T) *ExecutionBinding {
	t.Helper()
	b := contentFixture(t)
	contentTool(t, b, "", "parent", "Agent")
	lifecycleObserve(t, b, taskStart(t, b, "task", "parent"))
	return b
}

func TestTaskLifecycleKeepsSnapshotAndTerminalSourcesSeparate(t *testing.T) {
	b := contentFixture(t)
	// Native level reports may precede edges, include unknown tasks and omit
	// foreground work. They cannot grant tool ownership or finish a task.
	level := taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": []any{map[string]any{"task_id": "task", "task_type": "local_agent", "description": "private summary"}}})
	observation := lifecycleObserve(t, b, level)
	observation.Task.Background[0].ID = "caller mutation"
	if !b.backgroundTasks["task"] || len(b.tasks) != 0 {
		t.Fatal("background level changed task ownership")
	}
	contentTool(t, b, "", "parent", "Agent")
	started := lifecycleObserve(t, b, taskStart(t, b, "task", "parent"))
	*started.Task.ToolID = "caller mutation"
	*started.Task.Type = LocalBashTask
	if b.tasks["task"].tool != "parent" || !b.activeChildTask("parent") {
		t.Fatal("display consumer changed task ownership")
	}
	raw, _ := json.Marshal(started)
	if strings.Contains(string(raw), "Private child prompt") {
		t.Fatal("private task payload escaped publication boundary")
	}
	lifecycleObserve(t, b, taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": []any{}}))
	if !b.activeChildTask("parent") {
		t.Fatal("empty level snapshot completed foreground task")
	}
	progress := lifecycleObserve(t, b, taskEvent(t, b, TaskProgress, map[string]any{"task_id": "task", "tool_use_id": "parent", "description": "Working", "usage": map[string]any{"total_tokens": 0, "tool_uses": 2, "duration_ms": 3}}))
	if progress.Task.Usage.TotalTokens != 0 || progress.Task.Status != nil || progress.Task.Usage.ToolUses != 2 {
		t.Fatal("progress fabricated terminal status or token usage")
	}
	lifecycleObserve(t, b, taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "paused", "is_backgrounded": true}}))
	if !b.activeChildTask("parent") {
		t.Fatal("paused task became completed")
	}
	lifecycleObserve(t, b, taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "killed", "end_time": 1234}}))
	if b.activeChildTask("parent") || b.tasks["task"].notified {
		t.Fatal("terminal patch needed or fabricated a notification")
	}
	notified := lifecycleObserve(t, b, taskEvent(t, b, TaskNotification, map[string]any{"task_id": "task", "tool_use_id": "parent", "status": "stopped", "output_file": "/private/task-output", "summary": "Stopped"}))
	if notified.Task.Status == nil || *notified.Task.Status != TaskStopped || b.tasks["task"].status != TaskStopped || !b.tasks["task"].notified {
		t.Fatal("mapped notification lost original terminal vocabulary")
	}
	if b.content.tools["parent"].finished {
		t.Fatal("task completion invented a parent tool result")
	}
}

func TestTaskRejectsForeignContradictoryAndUnboundedEvidence(t *testing.T) {
	for _, name := range []string{"foreign-task", "foreign-tool", "wrong-task-tool-kind", "duplicate-start", "duplicate-notification", "terminal-regression", "terminal-conflict", "progress-after-terminal", "missing-usage-counter", "negative-usage", "case-alias", "foreign-event-field", "unknown-patch", "snapshot-duplicate", "snapshot-null", "snapshot-bound", "open-bound", "identity-bound"} {
		t.Run(name, func(t *testing.T) {
			b := taskFixture(t)
			bad := taskEvent(t, b, TaskProgress, map[string]any{"task_id": "task", "tool_use_id": "parent", "description": "Working", "usage": map[string]any{"total_tokens": 1, "tool_uses": 1, "duration_ms": 1}})
			switch name {
			case "foreign-task":
				bad = lifecycleChange(t, bad, "task_id", "foreign")
			case "foreign-tool":
				bad = lifecycleChange(t, bad, "tool_use_id", "foreign")
			case "wrong-task-tool-kind":
				contentTool(t, b, "", "bash", "Bash")
				bad = taskStart(t, b, "wrong-kind", "bash")
			case "duplicate-start":
				bad = taskStart(t, b, "task", "parent")
			case "terminal-regression", "terminal-conflict", "progress-after-terminal", "duplicate-notification":
				terminal := map[string]any{"task_id": "task", "status": "completed", "output_file": "/private/output", "summary": "Done"}
				lifecycleObserve(t, b, taskEvent(t, b, TaskNotification, terminal))
				switch name {
				case "terminal-regression":
					bad = taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "running"}})
				case "terminal-conflict":
					bad = taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "failed"}})
				case "duplicate-notification":
					bad = taskEvent(t, b, TaskNotification, terminal)
				}
			case "missing-usage-counter":
				bad = lifecycleChange(t, bad, "usage", map[string]any{"total_tokens": 1, "tool_uses": 1})
			case "negative-usage":
				bad = lifecycleChange(t, bad, "usage", map[string]any{"total_tokens": -1, "tool_uses": 1, "duration_ms": 1})
			case "case-alias":
				bad = lifecycleChange(t, bad, "Task_id", "task")
			case "foreign-event-field":
				bad = lifecycleChange(t, bad, "prompt", json.RawMessage("null"))
			case "unknown-patch":
				bad = taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"process": 123}})
			case "snapshot-duplicate":
				item := map[string]any{"task_id": "task", "task_type": "local_agent", "description": "fixture"}
				bad = taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": []any{item, item}})
			case "snapshot-null":
				bad = taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": nil})
			case "snapshot-bound":
				items := make([]any, 129)
				for i := range items {
					items[i] = map[string]any{"task_id": fmt.Sprint(i), "task_type": "local_agent", "description": "fixture"}
				}
				bad = taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": items})
			case "open-bound":
				for i := 0; i < 127; i++ {
					b.tasks[fmt.Sprint(i)] = nativeTaskState{status: TaskRunning}
				}
				bad = taskEvent(t, b, TaskStarted, map[string]any{"task_id": "new", "task_type": "unknown-native-extension", "description": "fixture"})
			case "identity-bound":
				for i := 0; i < 4095; i++ {
					b.tasks[fmt.Sprint(i)] = nativeTaskState{status: TaskCompleted}
				}
				bad = taskEvent(t, b, TaskStarted, map[string]any{"task_id": "new", "description": "fixture"})
			}
			if _, err := b.Observe(bad); err == nil || b.problem == nil {
				t.Fatal("invalid task evidence did not latch recovery")
			}
		})
	}
}

func TestChildSnapshotsKeepOriginalBlocksAndToolOwnership(t *testing.T) {
	b := taskFixture(t)
	input := contentResult(t, b, "parent", map[string]any{"type": "text", "text": "child context"})
	observed := lifecycleObserve(t, b, input)
	if observed.Content[0].Kind != ChildInputObserved || observed.Content[0].MessageID != "" || observed.Kind == InputAccepted {
		t.Fatal("child context accepted a product input")
	}
	message := contentMessage("child_message", map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "tool_use", "id": "child_tool", "name": "Read", "input": map[string]any{"file_path": "/private/file"}})
	event := lifecycleChange(t, contentCompleted(t, b, "parent", "child_message", nil), "message", message)
	completed := lifecycleObserve(t, b, event).Content[0]
	if completed.Kind != ProviderMessageSnapshot || completed.Index != nil || len(completed.Blocks) != 2 || completed.Blocks[1].Tool.ProposedInput != nil {
		t.Fatal("child snapshot was flattened or gained synthetic stream evidence")
	}
	completed.Blocks[1].Tool.Name = "caller mutation"
	if b.content.tools["child_tool"].name != "Read" || b.content.tools["child_tool"].streamed {
		t.Fatal("child tool ownership changed")
	}
	result := lifecycleObserve(t, b, contentResult(t, b, "parent", map[string]any{"type": "tool_result", "tool_use_id": "child_tool", "content": "read result"})).Content[0]
	if result.Index != nil || result.ParentToolID != "parent" || result.ToolResult.Name != "Read" {
		t.Fatal("child result lost identity or invented a stream index")
	}
	// Distinct native frames can carry separate completed blocks for the same
	// provider message. Preserve their arrival without inventing stream indices.
	second := lifecycleObserve(t, b, contentCompleted(t, b, "parent", "child_message", map[string]any{"type": "text", "text": "second"})).Content[0]
	if len(second.Blocks) != 1 || *second.Blocks[0].Text != "second" || second.Index != nil {
		t.Fatal("later child message fragment changed")
	}
}

func TestChildSnapshotsRejectPartialOwnershipAndForeignEvidence(t *testing.T) {
	for _, name := range []string{"no-task", "terminal-task", "stream-after-terminal-task", "changed-model", "unknown-tool", "duplicate-tool", "mixed-user-result", "unknown-content", "stream-after-snapshot"} {
		t.Run(name, func(t *testing.T) {
			b := taskFixture(t)
			message := contentMessage("child", map[string]any{"type": "tool_use", "id": "valid_tool", "name": "Read", "input": map[string]any{}})
			bad := lifecycleChange(t, contentCompleted(t, b, "parent", "child", nil), "message", message)
			switch name {
			case "no-task":
				delete(b.tasks, "task")
			case "terminal-task", "stream-after-terminal-task":
				task := b.tasks["task"]
				task.status = TaskCompleted
				b.tasks["task"] = task
				if name == "stream-after-terminal-task" {
					bad = contentPartial(t, b, "parent", map[string]any{"type": "message_start", "message": contentMessage("child")})
				}
			case "changed-model":
				lifecycleObserve(t, b, contentCompleted(t, b, "parent", "child", map[string]any{"type": "text", "text": "before"}))
				message["model"] = "foreign"
				bad = lifecycleChange(t, bad, "message", message)
			case "unknown-tool", "duplicate-tool", "unknown-content":
				second := map[string]any{"type": "tool_use", "id": "other_tool", "name": "Read", "input": map[string]any{}}
				if name == "unknown-tool" {
					second["name"] = "ForeignTool"
				}
				if name == "duplicate-tool" {
					second["id"] = "valid_tool"
				}
				if name == "unknown-content" {
					second = map[string]any{"type": "future-extension"}
				}
				message["content"] = append(message["content"].([]any), second)
				bad = lifecycleChange(t, bad, "message", message)
			case "mixed-user-result":
				bad = contentResult(t, b, "parent", map[string]any{"type": "text", "text": "context"}, map[string]any{"type": "tool_result", "tool_use_id": "parent", "content": "unowned"})
			case "stream-after-snapshot":
				lifecycleObserve(t, b, contentCompleted(t, b, "parent", "child", map[string]any{"type": "text", "text": "before"}))
				bad = contentPartial(t, b, "parent", map[string]any{"type": "message_start", "message": contentMessage("child")})
			}
			if _, err := b.Observe(bad); err == nil || b.content.tools["valid_tool"].name != "" {
				t.Fatal("invalid child content partially installed a tool or escaped ownership")
			}
		})
	}
}

func TestTaskProgressNeverGrantsInputToolOrPermissionAcceptance(t *testing.T) {
	b, _ := lifecycleFixture(t)
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
	lifecycleObserve(t, b, lifecycleInit(t, b))
	status := lifecycleMessage(t, b, "system", map[string]any{"subtype": "status", "status": "requesting", "permissionMode": "acceptEdits"})
	observed := lifecycleObserve(t, b, status)
	if b.accepted || observed.Accepted || observed.Kind != ProgressObserved || b.permission == AcceptEditsPermission {
		t.Fatal("status granted input or changed permissions")
	}
	b = taskFixture(t)
	progress := lifecycleMessage(t, b, "tool_progress", map[string]any{"tool_use_id": "parent", "tool_name": "Agent", "parent_tool_use_id": nil, "elapsed_time_seconds": json.RawMessage("0.001"), "task_id": "task", "heartbeat": true})
	observation := lifecycleObserve(t, b, progress)
	if observation.Progress.Elapsed == nil || *observation.Progress.Elapsed != "0.001" || b.content.tools["parent"].finished {
		t.Fatal("tool progress changed native duration or completed its tool")
	}
	summary := lifecycleMessage(t, b, "tool_use_summary", map[string]any{"summary": "Native advisory summary", "preceding_tool_use_ids": []string{"parent"}})
	lifecycleObserve(t, b, summary)
	if b.content.tools["parent"].finished {
		t.Fatal("summary completed tool")
	}
	if _, err := b.Observe(lifecycleChange(t, progress, "tool_use_id", string(domain.NewID()))); err == nil {
		t.Fatal("foreign tool progress accepted")
	}
}

func TestOriginalResultRetainsOwnedBackgroundWorkAndLaterChildCallbacks(t *testing.T) {
	b := taskFixture(t)
	lifecycleObserve(t, b, taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": []any{map[string]any{"task_id": "task", "task_type": "local_agent", "description": "background"}}}))
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "parent", "content": "Background task launched"}))
	result := lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false)).Result
	if !result.Successful() || result.KnownWork.PendingTasks != 1 || result.KnownWork.BackgroundTasks != 1 || !b.finished {
		t.Fatal("original result completed or forgot its known background task")
	}
	lifecycleObserve(t, b, contentCompleted(t, b, "parent", "late_child", map[string]any{"type": "tool_use", "id": "late_tool", "name": "Bash", "input": map[string]any{"command": "private fixture"}}))
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "late_tool", "input": map[string]any{"command": "private fixture"}})
	request := StreamEvent{Kind: NativeRequest, RequestID: "child_permission", ArrivalID: domain.NewID(), Body: raw}
	lifecycleObserve(t, b, request)
	_, reply, err := b.PreparePermissionReply(request.ArrivalID, PermissionReply{Behavior: PermissionAllow})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleObserve(t, b, interactionEcho(request, reply))
	lifecycleObserve(t, b, contentResult(t, b, "parent", map[string]any{"type": "tool_result", "tool_use_id": "late_tool", "content": "owned result"}))
	lifecycleObserve(t, b, taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "completed"}}))
	if b.knownWork().PendingTasks != 0 || b.knownWork().BackgroundTasks != 1 || result.KnownWork.PendingTasks != 1 {
		t.Fatal("task update rewrote the original result or the independent level snapshot")
	}
	lifecycleObserve(t, b, taskEvent(t, b, BackgroundTasksChanged, map[string]any{"tasks": []any{}}))
	if _, err := b.Observe(contentCompleted(t, b, "parent", "after_task", map[string]any{"type": "text", "text": "foreign late text"})); err == nil {
		t.Fatal("completed task acquired new child content")
	}
}

func TestTaskTerminalCannotAbandonAnObservedChild(t *testing.T) {
	for _, kind := range []string{"stream", "tool", "nested-task"} {
		t.Run(kind, func(t *testing.T) {
			b := taskFixture(t)
			switch kind {
			case "stream":
				contentStart(t, b, "parent", "active_message")
			case "tool":
				contentTool(t, b, "parent", "child", "Read")
			case "nested-task":
				contentTool(t, b, "parent", "nested", "Agent")
				lifecycleObserve(t, b, taskStart(t, b, "nested_task", "nested"))
				lifecycleObserve(t, b, contentResult(t, b, "parent", map[string]any{"type": "tool_result", "tool_use_id": "nested", "content": "Backgrounded"}))
			}
			if _, err := b.Observe(taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": "completed"}})); err == nil || b.tasks["task"].status.terminal() {
				t.Fatal("terminal patch abandoned observed child work")
			}
		})
	}
}

func TestProgressRejectsMalformedOrForeignNativeEvidence(t *testing.T) {
	for _, name := range []string{"negative-duration", "null-duration", "missing-parent", "empty-parent", "wrong-parent", "wrong-name", "unknown-task", "terminal-task", "finished-tool", "missing-status", "unknown-status", "unknown-permission", "unknown-result", "unknown-field", "summary-foreign", "retry-null-counter", "retry-unknown-field", "null-task-patch"} {
		t.Run(name, func(t *testing.T) {
			b := taskFixture(t)
			bad := lifecycleMessage(t, b, "tool_progress", map[string]any{"tool_use_id": "parent", "tool_name": "Agent", "parent_tool_use_id": nil, "elapsed_time_seconds": 0.5, "task_id": "task"})
			switch name {
			case "negative-duration":
				bad = lifecycleChange(t, bad, "elapsed_time_seconds", -1)
			case "null-duration":
				bad = lifecycleChange(t, bad, "elapsed_time_seconds", json.RawMessage("null"))
			case "missing-parent":
				bad = lifecycleChange(t, bad, "parent_tool_use_id", nil)
			case "empty-parent":
				bad = lifecycleChange(t, bad, "parent_tool_use_id", "")
			case "wrong-parent":
				bad = lifecycleChange(t, bad, "parent_tool_use_id", "foreign")
			case "wrong-name":
				bad = lifecycleChange(t, bad, "tool_name", "Bash")
			case "unknown-task":
				bad = lifecycleChange(t, bad, "task_id", "foreign")
			case "terminal-task":
				task := b.tasks["task"]
				task.status = TaskCompleted
				b.tasks["task"] = task
			case "finished-tool":
				tool := b.content.tools["parent"]
				tool.finished = true
				b.content.tools["parent"] = tool
				bad = lifecycleChange(t, bad, "task_id", nil)
			case "missing-status", "unknown-status", "unknown-permission", "unknown-result":
				bad = lifecycleMessage(t, b, "system", map[string]any{"subtype": "status", "status": nil})
				switch name {
				case "missing-status":
					bad = lifecycleChange(t, bad, "status", nil)
				case "unknown-status":
					bad = lifecycleChange(t, bad, "status", "finished")
				case "unknown-permission":
					bad = lifecycleChange(t, bad, "permissionMode", "unreviewed")
				case "unknown-result":
					bad = lifecycleChange(t, bad, "compact_result", "unrecognized")
				}
			case "unknown-field":
				bad = lifecycleChange(t, bad, "Elapsed_time_seconds", 1)
			case "summary-foreign":
				bad = lifecycleMessage(t, b, "tool_use_summary", map[string]any{"summary": "unowned", "preceding_tool_use_ids": []string{"foreign"}})
			case "retry-null-counter", "retry-unknown-field":
				retry := map[string]any{"agent_id": "child", "attempt": 1, "max_retries": 2, "retry_delay_ms": 0, "error_status": nil, "error_category": "server_error"}
				if name == "retry-null-counter" {
					retry["attempt"] = nil
				} else {
					retry["fallback_model"] = "foreign"
				}
				bad = lifecycleChange(t, bad, "subagent_retry", retry)
			case "null-task-patch":
				bad = taskEvent(t, b, TaskUpdated, map[string]any{"task_id": "task", "patch": map[string]any{"status": nil}})
			}
			if _, err := b.Observe(bad); err == nil {
				t.Fatal("malformed or unowned progress accepted")
			}
		})
	}
}
