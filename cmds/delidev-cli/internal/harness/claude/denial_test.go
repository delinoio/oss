package claude

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func interruptedDenialFixture(t *testing.T, interrupt, echo bool) (*ExecutionBinding, StreamEvent) {
	t.Helper()
	b := contentFixture(t)
	request := interactionTool(t, b, "Bash", map[string]any{"command": "private original command"})
	lifecycleObserve(t, b, request)
	_, raw, err := b.PreparePermissionReply(request.ArrivalID, PermissionReply{Behavior: PermissionDeny, Message: "Original refusal", Interrupt: interrupt})
	if err != nil {
		t.Fatal(err)
	}
	if echo {
		lifecycleObserve(t, b, interactionEcho(request, raw))
	}
	return b, request
}

func interruptedDenialToolResult(t *testing.T, b *ExecutionBinding) StreamEvent {
	t.Helper()
	e := contentResult(t, b, "", map[string]any{"type": "tool_result", "tool_use_id": "toolu_callback", "is_error": true, "content": "Original refusal"})
	return lifecycleChange(t, e, "tool_result_meta", []any{map[string]any{"id": "toolu_callback", "non_execution_kind": "user-rejected"}})
}

func interruptedDenialContext(t *testing.T, b *ExecutionBinding) StreamEvent {
	t.Helper()
	return contentResult(t, b, "", map[string]any{"type": "text", "text": "[Request interrupted by user for tool use]"})
}

func interruptedDenialResult(t *testing.T, b *ExecutionBinding) StreamEvent {
	t.Helper()
	e := lifecycleResult(t, b, AbortedTools, true)
	for key, value := range map[string]any{"user_message_uuid": nil, "subtype": ResultExecutionError, "result": nil, "errors": []string{}, "stop_reason": "tool_use"} {
		e = lifecycleChange(t, e, key, value)
	}
	return e
}

func TestInterruptedDenialPreservesCallbackContextWithoutInputOrStopAuthority(t *testing.T) {
	b, request := interruptedDenialFixture(t, true, true)
	tool := lifecycleObserve(t, b, interruptedDenialToolResult(t, b))
	if len(tool.Content) != 1 || tool.Content[0].ToolResult.NonExecution.Kind != domain.ClaudeUserRejectedNonExecution || b.content.tools["toolu_callback"].inline != nil {
		t.Fatal("interrupted denial lost original classification or acquired executed history")
	}
	context := lifecycleObserve(t, b, interruptedDenialContext(t, b))
	if len(context.Content) != 1 || context.Content[0].Kind != NativeCallbackInterruptContext || context.Content[0].CallbackArrivalID != request.ArrivalID || context.NativeID == tool.NativeID {
		t.Fatal("original independent context lost ownership")
	}
	result := lifecycleObserve(t, b, interruptedDenialResult(t, b))
	if result.Kind != CallbackInterruptResultObserved || result.CallbackArrivalID != request.ArrivalID || result.InputID != "" || result.Accepted || b.finished || b.terminal != nil || b.interrupt != nil || b.interruptResult == nil || !b.accepted {
		t.Fatal("callback interruption invented input completion or explicit Stop")
	}
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCancelled))
}

func TestInterruptedDenialRejectsUnprovedOrChangedToolResult(t *testing.T) {
	for _, scenario := range []string{"no-interrupt", "no-echo", "canceled", "foreign-meta", "missing-error", "successful", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			b, request := interruptedDenialFixture(t, scenario != "no-interrupt", scenario != "no-echo")
			e := interruptedDenialToolResult(t, b)
			switch scenario {
			case "canceled":
				b.interactions[request.ArrivalID].canceled = true
			case "foreign-meta":
				e = lifecycleChange(t, e, "tool_result_meta", []any{map[string]any{"id": "foreign", "non_execution_kind": "user-rejected"}})
			case "missing-error", "successful":
				block := map[string]any{"type": "tool_result", "tool_use_id": "toolu_callback", "content": "Original refusal"}
				if scenario == "successful" {
					block["is_error"] = false
				}
				e = lifecycleChange(t, e, "message", map[string]any{"role": "user", "content": []any{block}})
			case "duplicate":
				lifecycleObserve(t, b, e)
				e = interruptedDenialToolResult(t, b)
			}
			if _, err := b.Observe(e); err == nil || b.problem == nil {
				t.Fatal("invalid interrupted denial did not retain uncertainty")
			}
			if scenario != "duplicate" && (b.content.tools["toolu_callback"].finished || b.denialToolResult) {
				t.Fatal("invalid result completed the original tool")
			}
		})
	}
}

func TestInterruptedDenialRequiresSeparateExactOriginalContext(t *testing.T) {
	for _, scenario := range []string{"before-result", "changed-text", "child", "synthetic", "structured", "result-meta", "extra-block", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			b, _ := interruptedDenialFixture(t, true, true)
			if scenario != "before-result" {
				lifecycleObserve(t, b, interruptedDenialToolResult(t, b))
			}
			e := interruptedDenialContext(t, b)
			switch scenario {
			case "changed-text":
				e = contentResult(t, b, "", map[string]any{"type": "text", "text": "[Request interrupted by user]"})
			case "child":
				e = lifecycleChange(t, e, "parent_tool_use_id", "toolu_callback")
			case "synthetic":
				e = lifecycleChange(t, e, "isSynthetic", true)
			case "structured":
				e = lifecycleChange(t, e, "tool_use_result", "Original refusal")
			case "result-meta":
				e = lifecycleChange(t, e, "tool_result_meta", []any{map[string]any{"id": "toolu_callback", "non_execution_kind": "user-rejected"}})
			case "extra-block":
				e = contentResult(t, b, "", map[string]any{"type": "text", "text": "[Request interrupted by user for tool use]"}, map[string]any{"type": "text", "text": "Unowned text"})
			case "duplicate":
				lifecycleObserve(t, b, e)
				e = interruptedDenialContext(t, b)
			}
			if _, err := b.Observe(e); err == nil || b.problem == nil || (scenario != "duplicate" && b.denialContext) {
				t.Fatal("invalid interrupted context acquired original authority")
			}
		})
	}
}

func TestInterruptedDenialRejectsPrematureOrChangedTerminal(t *testing.T) {
	for _, scenario := range []string{"before-result", "missing-context", "wrong-reason", "false-error", "null-input", "foreign-input", "duplicate", "completed-command"} {
		t.Run(scenario, func(t *testing.T) {
			b, _ := interruptedDenialFixture(t, true, true)
			if scenario != "before-result" {
				lifecycleObserve(t, b, interruptedDenialToolResult(t, b))
				if scenario != "missing-context" {
					lifecycleObserve(t, b, interruptedDenialContext(t, b))
				}
			}
			e := interruptedDenialResult(t, b)
			switch scenario {
			case "wrong-reason":
				e = lifecycleChange(t, e, "terminal_reason", AbortedStreaming)
			case "false-error":
				e = lifecycleChange(t, e, "is_error", false)
			case "null-input":
				e = lifecycleChange(t, e, "user_message_uuid", json.RawMessage("null"))
			case "foreign-input":
				e = lifecycleChange(t, e, "user_message_uuid", domain.NewID())
			case "duplicate", "completed-command":
				lifecycleObserve(t, b, e)
				e = interruptedDenialResult(t, b)
				if scenario == "completed-command" {
					e = lifecycleCommand(t, b, CommandCompleted)
				}
			}
			if _, err := b.Observe(e); err == nil || b.problem == nil || b.finished || b.terminal != nil {
				t.Fatal("invalid session result invented input completion")
			}
		})
	}
}

func TestInterruptedDenialRequiresExclusiveRootToolBeforePreparation(t *testing.T) {
	for _, scenario := range []string{"child", "other-tool", "continuation", "task", "background-task", "stop"} {
		t.Run(scenario, func(t *testing.T) {
			b := contentFixture(t)
			request := interactionTool(t, b, "Bash", map[string]any{"command": "fixture"})
			lifecycleObserve(t, b, request)
			switch scenario {
			case "child":
				tool := b.content.tools["toolu_callback"]
				tool.parent = "original_parent"
				b.content.tools["toolu_callback"] = tool
			case "other-tool":
				contentTool(t, b, "", "toolu_other", "Read")
			case "continuation":
				b.continuing = true
			case "task":
				b.tasks = map[string]nativeTaskState{"task": {}}
			case "background-task":
				b.backgroundTasks = map[string]bool{"task": true}
			case "stop":
				b.interrupt = &InterruptClaim{1, b.owner, b.session, b.input, b.turnID, domain.NewID()}
			}
			if _, _, err := b.PreparePermissionReply(request.ArrivalID, PermissionReply{Behavior: PermissionDeny, Message: "Original refusal", Interrupt: true}); err == nil || b.interruptedReply != "" || b.interactions[request.ArrivalID].prepared {
				t.Fatal("unowned interruption acquired native send intent")
			}
		})
	}
}
