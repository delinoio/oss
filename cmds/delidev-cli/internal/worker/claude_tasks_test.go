package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeTaskFixture(t *testing.T) (*ClaudeContentPublisher, *openCodeBindingRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	c, rpc, result := claudeToolFixture(t, "", "Bash")
	task := result
	task.NativeID = string(domain.NewID())
	task.Kind, task.Content = claude.TaskObserved, nil
	tool, kind, description := "tool_original_one", claude.LocalBashTask, "Original Bash task"
	task.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "native_task", ToolID: &tool, Type: &kind, Description: &description}
	return c, rpc, task, result
}
func TestClaudeTaskReceiptReplayPreservesOwnershipAndCompletedToolProgress(t *testing.T) {
	c, rpc, start, result := claudeTaskFixture(t)
	ctx := context.Background()
	rpc.lose = true

	if handled, err := c.PublishTaskObservation(ctx, start); !handled || err == nil || c.tasks != nil {
		t.Fatal("uncertain receipt advanced task state")
	}
	last := len(rpc.events) - 1
	*start.Task.Description = "Changed caller-owned text"
	if _, err := c.PublishObservation(ctx, result); err == nil {
		t.Fatal("pending task receipt allowed later result")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
		t.Fatal("original task receipt changed")
	}
	var event domain.ExecutionEvent
	if domain.Decode(rpc.events[last], &event) != nil || *event.ClaudeProgress.Observation.Task.Description != "Original Bash task" || c.tasks.Closed() {
		t.Fatal("original task observation changed")
	}
	if _, err := c.PublishObservation(ctx, result); err != nil {
		t.Fatal(err)
	}
	if !c.toolsComplete() || c.tasks.Closed() {
		t.Fatal("tool completion completed native task")
	}
	o := start
	o.Kind, o.Task, o.NativeID = claude.ProgressObserved, nil, string(domain.NewID())
	elapsed, task := claude.NativeSeconds("1.0010"), "native_task"
	o.Progress = &claude.NativeProgressObservation{Kind: claude.ToolProgressObserved, ToolID: "tool_original_one", ToolName: "Bash", Elapsed: &elapsed, TaskID: &task}
	if handled, err := c.PublishToolProgressObservation(ctx, o); !handled || err != nil {
		t.Fatal("active task lost completed original tool owner", err)
	}
	status := claude.TaskCompleted
	o = start
	o.NativeID = string(domain.NewID())
	o.Task = &claude.NativeTaskObservation{Kind: claude.TaskUpdated, ID: task, Patch: &claude.TaskPatch{Status: &status}}
	rpc.lose = true
	if _, err := c.PublishTaskObservation(ctx, o); err == nil || c.tasks.Closed() {
		t.Fatal("uncertain terminal receipt completed task")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil || !c.tasks.Closed() {
		t.Fatal("original terminal receipt did not settle task", err)
	}
}
func TestClaudeTaskRejectsForeignUnownedAndUnsupportedObservations(t *testing.T) {
	for _, scenario := range []string{"tool", "type", "subagent", "prompt", "workflow", "depth", "input", "turn", "session", "mixed", "event-id", "early-progress", "duplicate", "finished-tool"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, o, result := claudeTaskFixture(t)
			switch scenario {
			case "tool":
				*o.Task.ToolID = "foreign"
			case "type":
				*o.Task.Type = claude.LocalAgentTask
			case "subagent":
				v := "agent"
				o.Task.SubagentType = &v
			case "prompt":
				v := "prompt"
				o.Task.Prompt = &v
			case "workflow":
				v := "workflow"
				o.Task.WorkflowName = &v
			case "depth":
				v := uint32(1)
				o.Task.SpawnDepth = &v
			case "input":
				o.InputID = domain.NewID()
			case "turn":
				o.TurnID = string(domain.NewID())
			case "session":
				o.SessionID = domain.NewID()
			case "mixed":
				o.Progress = &claude.NativeProgressObservation{}
			case "event-id":
				o.NativeID = o.TurnID
			case "early-progress":
				o.Task = &claude.NativeTaskObservation{Kind: claude.TaskProgress, ID: o.Task.ID, Description: o.Task.Description, Usage: &claude.TaskUsage{}}
			case "duplicate":
				if _, err := c.PublishTaskObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
				o.NativeID = string(domain.NewID())
			case "finished-tool":
				if _, err := c.PublishObservation(context.Background(), result); err != nil {
					t.Fatal(err)
				}
			}
			before := len(rpc.events)
			if scenario == "session" {
				_, err := c.PublishTaskObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if handled, err := c.PublishTaskObservation(context.Background(), o); !handled || err == nil || len(rpc.events) != before {
				t.Fatal("invalid task published")
			}
		})
	}
}
func TestClaudeTerminalRequiresAcknowledgedTaskClosure(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		c, rpc, command, idle := claudeTerminalFixture(t)
		c.tasks = &domain.ClaudeTasksState{Tasks: map[string]domain.ClaudeTaskState{"task": {Status: domain.ClaudeTaskRunning}}}
		if snapshot {
			c.tasks.Tasks = nil
			c.tasks.Background = []string{"snapshot-task"}
		}
		if _, err := c.PublishBoundaryObservation(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		before := len(rpc.events)
		if _, err := c.PublishBoundaryObservation(context.Background(), idle); err == nil || c.terminal != nil || len(rpc.events) != before {
			t.Fatal("native idle erased retained task work")
		}
	}
}
