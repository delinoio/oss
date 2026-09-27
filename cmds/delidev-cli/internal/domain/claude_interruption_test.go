package domain

import "testing"

func interruptionFixture() ClaudeInterruption {
	return ClaudeInterruption{Kind: ClaudeDenialResult, NativeEventID: string(NewID()), InteractionID: NewID(), ArrivalID: NewID(), ToolMessageID: NewID(), ToolResultNativeID: string(NewID()), Result: &ClaudeInterruptionResult{Kind: ClaudeInterruptionError, Reason: ClaudeToolsAborted, Error: true, Usage: &ClaudeResultUsage{}}}
}

func TestClaudeInterruptedDenialRequiresItsSeparateNonExecutionKind(t *testing.T) {
	o := claudeResponseOriginal(false)
	tool := claudeSettlementTool(o)
	message, interrupt := "Original denial", true
	reply := ClaudePermissionResponse{Behavior: ClaudeReplyDeny, Message: &message, Interrupt: &interrupt}
	failed := true
	tool.Result.Error = &failed
	for _, kind := range []ClaudeNonExecutionKind{ClaudePermissionRuleNonExecution, ClaudeUserRejectedNonExecution} {
		tool.Result.NonExecution = &ClaudeToolNonExecution{NativeID: o.NativeItemID, Kind: kind}
		evidence, err := ClaudeCallbackResultEvidence(o, reply, tool)
		if kind == ClaudeUserRejectedNonExecution {
			if err != nil || evidence != ClaudeInterruptedDenialProcessed {
				t.Fatal("original interruption denial lost", err)
			}
		} else if err == nil {
			t.Fatal("ordinary denial gained interruption evidence")
		}
	}
	interrupt = false
	if _, err := ClaudeCallbackResultEvidence(o, reply, tool); err == nil {
		t.Fatal("unrequested interruption settled an ordinary denial")
	}
}

func TestClaudeInterruptionRetainsAbsentInputAndExactContext(t *testing.T) {
	for _, change := range []string{"valid", "input", "kind", "reason", "error", "usage", "usage-counter", "identity", "foreign-field", "context-mixed", "context-changed"} {
		t.Run(change, func(t *testing.T) {
			v := interruptionFixture()
			switch change {
			case "input":
				id := NewID()
				v.Result.NativeInputID = &id
			case "kind":
				v.Result.Kind = "success"
			case "reason":
				v.Result.Reason = "completed"
			case "error":
				v.Result.Error = false
			case "usage":
				v.Result.Usage = nil
			case "usage-counter":
				count := ClaudeUsageCount("-1")
				v.Result.Usage.MainLoop = &ClaudeProviderUsage{Input: &count}
			case "identity":
				v.NativeEventID = v.ToolResultNativeID
			case "foreign-field":
				v.Kind = "stop"
			case "context-mixed":
				s := ClaudeDenialContextText
				v.Context = &s
			case "context-changed":
				s := "[Request interrupted by user]"
				v.Kind, v.Context, v.Result = ClaudeDenialContext, &s, nil
			}
			if err := v.Validate(); (err == nil) != (change == "valid") {
				t.Fatal("incorrect interruption validation", err)
			}
		})
	}
	v := interruptionFixture()
	text := ClaudeDenialContextText
	v.Kind, v.Context, v.Result = ClaudeDenialContext, &text, nil
	e := ExecutionEvent{Version: 1, Sequence: 3, ExecutionID: NewID(), NativeThreadID: string(NewID()), NativeTurnID: string(NewID()), Kind: ExecutionClaudeInterruptionObserved, ClaudeInterruption: &ExecutionClaudeInterruption{ID: NewID(), Observation: v}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.Kind = ExecutionInputAccepted
	if e.Validate() == nil {
		t.Fatal("context accepted a product input")
	}
}
