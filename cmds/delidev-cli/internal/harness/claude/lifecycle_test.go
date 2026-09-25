package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/google/uuid"
)

const lifecycleFixtureText = "Private original input for lifecycle correlation."

func lifecycleFixture(t *testing.T) (*ExecutionBinding, APIStreamConfig) {
	t.Helper()
	config, _ := apiFixtureConfig(t, "binding")
	binding, err := BindExecution(config, domain.NewID(), lifecycleFixtureText)
	if err != nil {
		t.Fatal(err)
	}
	return binding, config
}

func lifecycleMessage(t *testing.T, b *ExecutionBinding, kind string, fields map[string]any) StreamEvent {
	t.Helper()
	fields["type"], fields["session_id"] = kind, b.session
	if fields["uuid"] == nil {
		fields["uuid"] = uuid.NewString()
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return StreamEvent{Kind: NativeMessage, Type: kind, Body: raw}
}

func lifecycleCommand(t *testing.T, b *ExecutionBinding, state CommandState) StreamEvent {
	return lifecycleMessage(t, b, "command_lifecycle", map[string]any{"command_uuid": b.input, "state": state})
}

func lifecycleInit(t *testing.T, b *ExecutionBinding) StreamEvent {
	return lifecycleMessage(t, b, "system", map[string]any{
		"subtype": "init", "cwd": b.workspace, "model": b.model, "permissionMode": b.permission, "apiKeySource": "ANTHROPIC_API_KEY", "claude_code_version": SupportedVersion, "output_style": "default",
		"tools": []string{"Bash", "Read", "Write", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode"}, "mcp_servers": []any{}, "slash_commands": []string{"compact", "doctor"}, "terminal_slash_commands": []string{"doctor"}, "agents": []string{"claude"}, "skills": []string{"verify"}, "plugins": []any{}, "capabilities": []string{"msg_lifecycle_v1"},
		"analytics_disabled": true, "product_feedback_disabled": true, "memory_paths": map[string]string{"auto": filepath.Join(b.home, "projects", "delidev", "memory")}, "fast_mode_state": "off", "fast_mode_disabled_reason": "sdk_opt_in_required",
	})
}

func lifecycleReplay(t *testing.T, b *ExecutionBinding) StreamEvent {
	return lifecycleMessage(t, b, "user", map[string]any{"uuid": b.input, "timestamp": "2026-09-25T00:00:00.001Z", "isReplay": true, "parent_tool_use_id": nil, "message": map[string]string{"role": "user", "content": lifecycleFixtureText}})
}

func lifecycleResult(t *testing.T, b *ExecutionBinding, reason TerminalReason, failed bool) StreamEvent {
	return lifecycleMessage(t, b, "result", map[string]any{"subtype": ResultSuccess, "user_message_uuid": b.input, "terminal_reason": reason, "is_error": failed, "stop_reason": "end_turn", "duration_ms": 12, "duration_api_ms": 9, "num_turns": 1, "result": "private final content", "total_cost_usd": 0.1, "usage": map[string]any{}, "modelUsage": map[string]any{}, "permission_denials": []any{}, "fast_mode_state": "off", "fast_mode_disabled_reason": "sdk_opt_in_required"})
}

func lifecycleObserve(t *testing.T, b *ExecutionBinding, event StreamEvent) LifecycleObservation {
	t.Helper()
	observation, err := b.Observe(event)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func lifecycleReady(t *testing.T, b *ExecutionBinding) {
	t.Helper()
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
	lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
	lifecycleObserve(t, b, lifecycleInit(t, b))
}

func lifecycleChange(t *testing.T, event StreamEvent, key string, value any) StreamEvent {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(event.Body, &fields); err != nil {
		t.Fatal(err)
	}
	if value == nil {
		delete(fields, key)
	} else {
		fields[key] = value
	}
	var err error
	event.Body, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestExecutionBindingPreservesIndependentNativeAcceptanceAndCompletion(t *testing.T) {
	b, _ := lifecycleFixture(t)
	for _, state := range []CommandState{CommandQueued, CommandStarted} {
		observation := lifecycleObserve(t, b, lifecycleCommand(t, b, state))
		if observation.Kind != CommandObserved || observation.Accepted || observation.Command != state {
			t.Fatal("command observation fabricated input acceptance")
		}
	}
	if observation := lifecycleObserve(t, b, lifecycleInit(t, b)); observation.Kind != SessionInitialized || observation.Accepted {
		t.Fatal("native initialization fabricated input acceptance")
	}
	if observation := lifecycleObserve(t, b, StreamEvent{Kind: NativeReplyEcho}); observation.Kind != PrivateObservation || observation.Accepted {
		t.Fatal("callback echo became input acceptance")
	}
	accepted := lifecycleObserve(t, b, lifecycleReplay(t, b))
	if accepted.Kind != InputAccepted || !accepted.Accepted || accepted.InputID != b.input || accepted.SessionID != b.session {
		t.Fatal("exact original replay was not retained")
	}
	finished := lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
	if finished.Kind != InputFinished || !finished.Accepted || finished.Result == nil || !finished.Result.Successful() {
		t.Fatal("original completed result was not retained")
	}
	// Native command closure can follow the result. It remains a separate fact.
	closed := lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
	if closed.Kind != CommandObserved || closed.Result != nil {
		t.Fatal("command closure fabricated another completion")
	}
	for _, observation := range []LifecycleObservation{accepted, finished} {
		raw, err := json.Marshal(observation)
		if err != nil || strings.Contains(string(raw), "Private original") || strings.Contains(string(raw), "private final") || strings.Contains(string(raw), "total_cost") {
			t.Fatal("unvalidated native content escaped lifecycle observation")
		}
	}
}

func TestExecutionBindingRejectsForeignReorderedOrModifiedAcceptance(t *testing.T) {
	for _, change := range []string{"started-first", "foreign-command", "repeat-queued", "repeat-init", "changed-model", "model-alias", "model-alias-pair", "changed-permission", "changed-workspace", "changed-account", "enabled-plugin", "missing-feedback-state", "foreign-memory", "foreign-session", "foreign-input", "changed-text", "child-replay", "injected-origin", "repeat-replay", "result-before-replay", "foreign-result", "missing-result-input", "duplicate-result", "contradictory-success", "unknown-terminal", "duplicate-key"} {
		t.Run(change, func(t *testing.T) {
			b, _ := lifecycleFixture(t)
			var bad StreamEvent
			switch change {
			case "started-first":
				bad = lifecycleCommand(t, b, CommandStarted)
			case "foreign-command":
				bad = lifecycleChange(t, lifecycleCommand(t, b, CommandQueued), "command_uuid", domain.NewID())
			case "repeat-queued":
				lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
				bad = lifecycleCommand(t, b, CommandQueued)
			default:
				lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
				lifecycleObserve(t, b, lifecycleCommand(t, b, CommandStarted))
				bad = lifecycleInit(t, b)
				switch change {
				case "changed-model":
					bad = lifecycleChange(t, bad, "model", "other-model")
				case "model-alias":
					bad = lifecycleChange(t, lifecycleChange(t, bad, "model", nil), "Model", b.model)
				case "model-alias-pair":
					bad = lifecycleChange(t, bad, "Model", b.model)
				case "changed-permission":
					bad = lifecycleChange(t, bad, "permissionMode", "default")
				case "changed-workspace":
					bad = lifecycleChange(t, bad, "cwd", filepath.Dir(b.workspace))
				case "changed-account":
					bad = lifecycleChange(t, bad, "apiKeySource", "apiKeyHelper")
				case "enabled-plugin":
					bad = lifecycleChange(t, bad, "plugins", []string{"foreign-plugin"})
				case "missing-feedback-state":
					bad = lifecycleChange(t, bad, "product_feedback_disabled", nil)
				case "foreign-memory":
					bad = lifecycleChange(t, bad, "memory_paths", map[string]string{"auto": b.home})
				default:
					lifecycleObserve(t, b, bad)
					if change == "repeat-init" {
						bad = lifecycleInit(t, b)
						break
					}
					bad = lifecycleReplay(t, b)
					switch change {
					case "foreign-session":
						bad = lifecycleChange(t, bad, "session_id", domain.NewID())
					case "foreign-input":
						bad = lifecycleChange(t, bad, "uuid", domain.NewID())
					case "changed-text":
						bad = lifecycleChange(t, bad, "message", map[string]string{"role": "user", "content": "different"})
					case "child-replay":
						bad = lifecycleChange(t, bad, "parent_tool_use_id", "other-tool")
					case "injected-origin":
						bad = lifecycleChange(t, bad, "origin", map[string]string{"kind": "task-notification"})
					case "result-before-replay":
						bad = lifecycleResult(t, b, Completed, false)
					case "duplicate-key":
						bad.Body = append([]byte(`{"isReplay":false,`), bad.Body[1:]...)
					default:
						lifecycleObserve(t, b, bad)
						if change == "repeat-replay" {
							break
						}
						bad = lifecycleResult(t, b, Completed, false)
						switch change {
						case "foreign-result":
							bad = lifecycleChange(t, bad, "user_message_uuid", domain.NewID())
						case "missing-result-input":
							bad = lifecycleChange(t, bad, "user_message_uuid", nil)
						case "duplicate-result":
							lifecycleObserve(t, b, bad)
							bad = lifecycleResult(t, b, Completed, false)
						case "contradictory-success":
							bad = lifecycleChange(t, bad, "terminal_reason", APIError)
						case "unknown-terminal":
							bad = lifecycleChange(t, bad, "terminal_reason", "future-terminal")
						}
					}
				}
			}
			if _, err := b.Observe(bad); err == nil {
				t.Fatal("invalid native lifecycle accepted")
			}
			if _, err := b.Observe(lifecycleResult(t, b, Completed, false)); err == nil {
				t.Fatal("later native success erased lifecycle uncertainty")
			}
		})
	}
}

func TestNativeResultRetainsFailuresAndDeferralsWithoutSuccessInference(t *testing.T) {
	for _, reason := range []TerminalReason{APIError, ModelError, PromptTooLong, AbortedStreaming, AbortedTools, HookStopped, ToolDeferred, BackgroundRequested, MaxTurns, BudgetExhausted} {
		t.Run(string(reason), func(t *testing.T) {
			b, _ := lifecycleFixture(t)
			lifecycleReady(t, b)
			failed, _ := terminalError(reason)
			observation := lifecycleObserve(t, b, lifecycleResult(t, b, reason, failed))
			if observation.Accepted || observation.Kind != InputFinished || observation.Result == nil || observation.Result.Successful() || observation.Result.Reason != reason || observation.Result.Error != failed {
				t.Fatal("native early termination fabricated acceptance or success")
			}
		})
	}
}

func TestUncorrelatedNativeFailureRetainsAcceptanceWithoutCompletingOriginalInput(t *testing.T) {
	b, _ := lifecycleFixture(t)
	lifecycleReady(t, b)
	lifecycleObserve(t, b, lifecycleReplay(t, b))
	errorResult := lifecycleChange(t, lifecycleResult(t, b, APIError, true), "user_message_uuid", nil)
	observed := lifecycleObserve(t, b, errorResult)
	if observed.Kind != UncorrelatedTermination || observed.InputID != "" || observed.Accepted || observed.Result == nil || !observed.Result.Error || !b.accepted || b.finished {
		t.Fatal("uncorrelated error completed the original input or erased its acceptance")
	}
	if _, err := b.Observe(lifecycleResult(t, b, Completed, false)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("later terminal observation erased correlation uncertainty")
	}
}

func TestExecutionBindingRejectsContradictoryNativeCommandAndResultClosure(t *testing.T) {
	for _, order := range []string{"result-first", "command-first", "null-result"} {
		t.Run(order, func(t *testing.T) {
			b, _ := lifecycleFixture(t)
			lifecycleReady(t, b)
			lifecycleObserve(t, b, lifecycleReplay(t, b))
			var bad StreamEvent
			switch order {
			case "result-first":
				result := lifecycleObserve(t, b, lifecycleResult(t, b, Completed, false))
				// Caller-owned observations cannot rewrite retained native facts.
				result.Result.Reason = AbortedStreaming
				bad = lifecycleCommand(t, b, CommandCancelled)
			case "command-first":
				lifecycleObserve(t, b, lifecycleCommand(t, b, CommandCompleted))
				bad = lifecycleResult(t, b, APIError, true)
			case "null-result":
				bad = lifecycleChange(t, lifecycleResult(t, b, Completed, false), "result", json.RawMessage(`null`))
			}
			if _, err := b.Observe(bad); err == nil {
				t.Fatal("contradictory or malformed native completion accepted")
			}
		})
	}
}

func TestExecutionBindingCommandCancellationDoesNotProveAcceptanceOrCompletion(t *testing.T) {
	for _, state := range []CommandState{CommandCancelled, CommandDiscarded} {
		t.Run(string(state), func(t *testing.T) {
			b, _ := lifecycleFixture(t)
			lifecycleObserve(t, b, lifecycleCommand(t, b, CommandQueued))
			o := lifecycleObserve(t, b, lifecycleCommand(t, b, state))
			if o.Kind != CommandObserved || o.Accepted || o.Result != nil {
				t.Fatal("native queue cancellation fabricated input completion")
			}
			if _, err := b.Observe(lifecycleCommand(t, b, CommandStarted)); err == nil {
				t.Fatal("canceled input started again")
			}
		})
	}
}

func TestExecutionBindingRejectsInvalidIdentityWithoutRetainingContents(t *testing.T) {
	for _, change := range []string{"session", "input", "version", "permission", "workspace", "empty-text", "long-text"} {
		t.Run(change, func(t *testing.T) {
			_, cfg := lifecycleFixture(t)
			input, text := domain.NewID(), lifecycleFixtureText
			switch change {
			case "session":
				cfg.SessionID = "invalid"
			case "input":
				input = "invalid"
			case "version":
				cfg.Version = "future"
			case "permission":
				cfg.Permission = "future"
			case "workspace":
				cfg.Workspace = "relative"
			case "empty-text":
				text = ""
			case "long-text":
				text = strings.Repeat("x", (256<<10)+1)
			}
			if _, err := BindExecution(cfg, input, text); err == nil {
				t.Fatal("invalid original execution binding accepted")
			}
		})
	}
}
