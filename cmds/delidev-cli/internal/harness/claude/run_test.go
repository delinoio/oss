package claude

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func runStateEvent(t *testing.T, b *ExecutionBinding, state NativeRunState) StreamEvent {
	return lifecycleMessage(t, b, "system", map[string]any{"subtype": "session_state_changed", "state": state})
}

func completedRunFixture(t *testing.T) *ExecutionBinding {
	t.Helper()
	b := taskFixture(t)
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	lifecycleObserve(t, b, taskEvent(t, b, TaskNotification, map[string]any{"task_id": "task", "tool_use_id": "parent", "status": "completed", "output_file": "/private/task-output", "summary": "Done"}))
	lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "parent", "content": "Background task result"}))
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	return b
}

func continuationResult(t *testing.T, b *ExecutionBinding, reason TerminalReason, failed bool) StreamEvent {
	t.Helper()
	event := lifecycleChange(t, lifecycleResult(t, b, reason, failed), "user_message_uuid", nil)
	return lifecycleChange(t, event, "origin", map[string]any{"kind": TaskNotificationOrigin})
}

func TestAutomaticContinuationRetainsNativeIdentityWithoutAcceptingInput(t *testing.T) {
	b := completedRunFixture(t)
	originalTurn := b.turnID
	if b.runState != RunRunning {
		t.Fatal("empty tasks and original result fabricated idle")
	}
	init := lifecycleObserve(t, b, lifecycleInit(t, b))
	if init.Kind != ContinuationInitialized || init.TurnID == originalTurn || !nativeUUID(init.TurnID) || init.InputID != "" || init.Accepted {
		t.Fatal("continuation borrowed original input")
	}
	contentTool(t, b, "", "followup", "Read")
	result := lifecycleObserve(t, b, contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "followup", "content": "original native continuation content"}))
	if result.InputID != "" || result.Accepted || result.TurnID != init.TurnID || result.Content[0].ToolResult.ID != "followup" {
		t.Fatal("continuation content lost its native owner")
	}
	final := lifecycleObserve(t, b, continuationResult(t, b, APIError, true))
	if final.Kind != ContinuationFinished || final.InputID != "" || final.Accepted || final.Result.Origin == nil || *final.Result.Origin != TaskNotificationOrigin || final.TurnID != init.TurnID || !b.terminal.Successful() {
		t.Fatal("continuation rewrote original terminal outcome")
	}
	idle := lifecycleObserve(t, b, runStateEvent(t, b, RunIdle))
	if idle.Run == nil || !idle.Run.ContinuationFailed || idle.InputID != "" || idle.Accepted {
		t.Fatal("idle erased failed continuation or acknowledged input")
	}
}

func TestRunBoundaryRejectsPrematureIdleAndForeignContinuation(t *testing.T) {
	for _, name := range []string{"no-result", "no-command-close", "no-running", "requires-action", "duplicate-state", "unknown-state", "case-alias", "foreign-session", "foreign-input", "null-input", "missing-origin", "null-origin", "unknown-origin", "origin-task-guess", "duplicate-result", "idle-during-continuation", "changed-init", "no-task-notification", "replay-original", "wake-after-idle"} {
		t.Run(name, func(t *testing.T) {
			b := completedRunFixture(t)
			event := runStateEvent(t, b, RunIdle)
			switch name {
			case "no-result":
				b.finished = false
			case "no-command-close":
				b.command = CommandStarted
			case "no-running":
				b.runState = ""
			case "requires-action":
				lifecycleObserve(t, b, runStateEvent(t, b, RunRequiresAction))
			case "duplicate-state":
				event = runStateEvent(t, b, RunRunning)
			case "unknown-state":
				event = runStateEvent(t, b, "unknown")
			case "case-alias":
				event = lifecycleChange(t, event, "State", "idle")
			case "foreign-session":
				event = lifecycleChange(t, event, "session_id", domain.NewID())
			case "changed-init":
				event = lifecycleChange(t, lifecycleInit(t, b), "model", "changed")
			case "no-task-notification":
				b.tasks = map[string]nativeTaskState{}
				b.notifications = 0
				event = lifecycleInit(t, b)
			case "replay-original":
				lifecycleObserve(t, b, lifecycleInit(t, b))
				event = lifecycleReplay(t, b)
			case "wake-after-idle":
				lifecycleObserve(t, b, event)
				event = runStateEvent(t, b, RunRunning)
			default:
				lifecycleObserve(t, b, lifecycleInit(t, b))
				if name != "idle-during-continuation" {
					event = continuationResult(t, b, Completed, false)
					switch name {
					case "foreign-input":
						event = lifecycleChange(t, event, "user_message_uuid", b.input)
					case "null-input":
						event = lifecycleChange(t, event, "user_message_uuid", json.RawMessage("null"))
					case "missing-origin":
						event = lifecycleChange(t, event, "origin", nil)
					case "null-origin":
						event = lifecycleChange(t, event, "origin", json.RawMessage("null"))
					case "unknown-origin":
						event = lifecycleChange(t, event, "origin", map[string]any{"kind": "auto-continuation"})
					case "origin-task-guess":
						event = lifecycleChange(t, event, "origin", map[string]any{"kind": TaskNotificationOrigin, "task_id": "task"})
					case "duplicate-result":
						lifecycleObserve(t, b, event)
						event = continuationResult(t, b, Completed, false)
					}
				}
			}
			if _, err := b.Observe(event); err == nil || b.problem == nil {
				t.Fatal("contradictory run evidence did not latch uncertainty")
			}
			if _, err := b.Observe(runStateEvent(t, b, RunIdle)); err == nil {
				t.Fatal("later idle erased uncertainty")
			}
		})
	}
}

func TestRunRequiresActionDoesNotAcceptInputOrFinishRun(t *testing.T) {
	b, _ := lifecycleFixture(t)
	running := lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	if running.Accepted || running.InputID != "" {
		t.Fatal("native run start accepted input")
	}
	lifecycleReady(t, b)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	waiting := lifecycleObserve(t, b, runStateEvent(t, b, RunRequiresAction))
	if waiting.Accepted || waiting.InputID != "" || b.finished {
		t.Fatal("requires-action fabricated terminal input")
	}
	lifecycleObserve(t, b, runStateEvent(t, b, RunRunning))
	lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	lifecycleObserve(t, b, runStateEvent(t, b, RunIdle))
}
