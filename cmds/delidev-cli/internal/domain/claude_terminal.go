package domain

type ClaudeResultKind string
type ClaudeTerminalReason string
type ClaudeCommandCompletion string

const (
	ClaudeResultSuccess                  ClaudeResultKind        = "success"
	ClaudeResultExecutionError           ClaudeResultKind        = "error_during_execution"
	ClaudeResultMaxTurns                 ClaudeResultKind        = "error_max_turns"
	ClaudeResultMaxBudget                ClaudeResultKind        = "error_max_budget_usd"
	ClaudeResultStructuredOutput         ClaudeResultKind        = "error_max_structured_output_retries"
	ClaudeCompleted                      ClaudeTerminalReason    = "completed"
	ClaudeAbortedStreaming               ClaudeTerminalReason    = "aborted_streaming"
	ClaudeAbortedTools                   ClaudeTerminalReason    = "aborted_tools"
	ClaudeBlockingLimit                  ClaudeTerminalReason    = "blocking_limit"
	ClaudeRapidRefillBreaker             ClaudeTerminalReason    = "rapid_refill_breaker"
	ClaudePromptTooLong                  ClaudeTerminalReason    = "prompt_too_long"
	ClaudeImageError                     ClaudeTerminalReason    = "image_error"
	ClaudeModelError                     ClaudeTerminalReason    = "model_error"
	ClaudeAPIError                       ClaudeTerminalReason    = "api_error"
	ClaudeMalformedToolUseExhausted      ClaudeTerminalReason    = "malformed_tool_use_exhausted"
	ClaudeStopHookPrevented              ClaudeTerminalReason    = "stop_hook_prevented"
	ClaudeHookStopped                    ClaudeTerminalReason    = "hook_stopped"
	ClaudeToolDeferred                   ClaudeTerminalReason    = "tool_deferred"
	ClaudeMaxTurns                       ClaudeTerminalReason    = "max_turns"
	ClaudeBackgroundRequested            ClaudeTerminalReason    = "background_requested"
	ClaudeBudgetExhausted                ClaudeTerminalReason    = "budget_exhausted"
	ClaudeStructuredOutputRetryExhausted ClaudeTerminalReason    = "structured_output_retry_exhausted"
	ClaudeToolDeferredUnavailable        ClaudeTerminalReason    = "tool_deferred_unavailable"
	ClaudeTurnSetupFailed                ClaudeTerminalReason    = "turn_setup_failed"
	ClaudeCommandCompleted               ClaudeCommandCompletion = "completed"
	ClaudeCommandCancelled               ClaudeCommandCompletion = "cancelled"
)

// These three original envelopes prove a settled root input boundary, not
// process cleanup or permission to resume. Usage remains separately retained.
type ClaudeTerminalObservation struct {
	InputID         ID                      `json:"input_id"`
	ResultNativeID  string                  `json:"result_native_id"`
	CommandNativeID string                  `json:"command_native_id"`
	IdleNativeID    string                  `json:"idle_native_id"`
	Kind            ClaudeResultKind        `json:"kind"`
	Reason          ClaudeTerminalReason    `json:"reason"`
	Error           bool                    `json:"is_error"`
	Command         ClaudeCommandCompletion `json:"command"`
}

func invalidClaudeTerminal() error {
	return Fail(InvalidArgument, "Invalid original Claude terminal evidence.", "Preserve separate original input result, command closure, idle and cleanup observations.")
}

func (v ClaudeTerminalObservation) Validate() error {
	if v.InputID.Validate() != nil {
		return invalidClaudeTerminal()
	}
	seen := map[string]bool{string(v.InputID): true}
	for _, id := range []string{v.ResultNativeID, v.CommandNativeID, v.IdleNativeID} {
		if NativeIdentity(id).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[id] {
			return invalidClaudeTerminal()
		}
		seen[id] = true
	}
	failed, aborted := false, false
	switch v.Reason {
	case ClaudeBlockingLimit, ClaudeRapidRefillBreaker, ClaudePromptTooLong, ClaudeImageError, ClaudeModelError, ClaudeAPIError, ClaudeMalformedToolUseExhausted, ClaudeBudgetExhausted, ClaudeStructuredOutputRetryExhausted, ClaudeToolDeferredUnavailable, ClaudeTurnSetupFailed:
		failed = true
	case ClaudeAbortedStreaming, ClaudeAbortedTools:
		aborted = true
	case ClaudeCompleted, ClaudeStopHookPrevented, ClaudeHookStopped, ClaudeToolDeferred, ClaudeMaxTurns, ClaudeBackgroundRequested:
	default:
		return invalidClaudeTerminal()
	}
	switch v.Kind {
	case ClaudeResultSuccess, ClaudeResultExecutionError:
	case ClaudeResultMaxTurns:
		if v.Reason != ClaudeMaxTurns {
			return invalidClaudeTerminal()
		}
	case ClaudeResultMaxBudget:
		if v.Reason != ClaudeBudgetExhausted {
			return invalidClaudeTerminal()
		}
	case ClaudeResultStructuredOutput:
		if v.Reason != ClaudeStructuredOutputRetryExhausted && !aborted {
			return invalidClaudeTerminal()
		}
	default:
		return invalidClaudeTerminal()
	}
	if (failed || v.Kind != ClaudeResultSuccess) && !v.Error || (failed || aborted) && v.Command != ClaudeCommandCancelled || !failed && !aborted && v.Command != ClaudeCommandCompleted {
		return invalidClaudeTerminal()
	}
	return nil
}

func (v ClaudeTerminalObservation) Outcome() ExecutionOutcome {
	if v.Kind == ClaudeResultSuccess && !v.Error {
		if v.Reason == ClaudeCompleted {
			return ExecutionSucceeded
		}
		if v.Reason == ClaudeAbortedStreaming || v.Reason == ClaudeAbortedTools {
			return ExecutionStopped
		}
	}
	return ExecutionFailed
}
