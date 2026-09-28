package domain

import "testing"

func TestClaudeTerminalPreservesNativeClassification(t *testing.T) {
	for _, scenario := range []struct {
		kind    ClaudeResultKind
		reason  ClaudeTerminalReason
		failed  bool
		command ClaudeCommandCompletion
		outcome ExecutionOutcome
	}{
		{ClaudeResultSuccess, ClaudeCompleted, false, ClaudeCommandCompleted, ExecutionSucceeded},
		{ClaudeResultSuccess, ClaudeAPIError, true, ClaudeCommandCancelled, ExecutionFailed},
		{ClaudeResultSuccess, ClaudeAbortedTools, false, ClaudeCommandCancelled, ExecutionStopped},
		{ClaudeResultExecutionError, ClaudeAbortedTools, true, ClaudeCommandCancelled, ExecutionFailed},
		{ClaudeResultSuccess, ClaudeToolDeferred, false, ClaudeCommandCompleted, ExecutionFailed},
		{ClaudeResultMaxTurns, ClaudeMaxTurns, true, ClaudeCommandCompleted, ExecutionFailed},
		{ClaudeResultMaxBudget, ClaudeBudgetExhausted, true, ClaudeCommandCancelled, ExecutionFailed},
		{ClaudeResultStructuredOutput, ClaudeStructuredOutputRetryExhausted, true, ClaudeCommandCancelled, ExecutionFailed},
	} {
		v := ClaudeTerminalObservation{InputID: NewID(), ResultNativeID: string(NewID()), CommandNativeID: string(NewID()), IdleNativeID: string(NewID()), Kind: scenario.kind, Reason: scenario.reason, Error: scenario.failed, Command: scenario.command}
		if v.Validate() != nil || v.Outcome() != scenario.outcome {
			t.Fatal("native result classification changed", scenario)
		}
		v.Command = "unknown"
		if v.Validate() == nil {
			t.Fatal("unknown command closure accepted")
		}
	}
}

func TestClaudeTerminalRejectsMissingReusedAndContradictoryEvidence(t *testing.T) {
	for _, scenario := range []string{"missing-id", "reused-id", "input-id", "kind", "reason", "error", "limit-reason", "command"} {
		v := ClaudeTerminalObservation{InputID: NewID(), ResultNativeID: string(NewID()), CommandNativeID: string(NewID()), IdleNativeID: string(NewID()), Kind: ClaudeResultSuccess, Reason: ClaudeCompleted, Command: ClaudeCommandCompleted}
		switch scenario {
		case "missing-id":
			v.IdleNativeID = ""
		case "reused-id":
			v.CommandNativeID = v.ResultNativeID
		case "input-id":
			v.ResultNativeID = string(v.InputID)
		case "kind":
			v.Kind = "unknown"
		case "reason":
			v.Reason = "unknown"
		case "error":
			v.Reason, v.Command = ClaudeAPIError, ClaudeCommandCancelled
		case "limit-reason":
			v.Kind, v.Error = ClaudeResultMaxTurns, true
		case "command":
			v.Command = ClaudeCommandCancelled
		}
		if v.Validate() == nil {
			t.Fatal("contradictory original evidence accepted", scenario)
		}
	}
}
