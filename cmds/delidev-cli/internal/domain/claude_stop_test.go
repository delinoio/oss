package domain

import "testing"

func stopProofFixture(retry bool) ClaudeStopObservation {
	v := ClaudeStopObservation{RequestID: NewID(), InputID: NewID(), MessageID: NewID(), NativeMessageID: "original_message", ContentEvidence: ClaudeAbortedAssistant, InterruptedNativeID: string(NewID()), ContextNativeID: string(NewID()), ResultNativeID: string(NewID()), CommandNativeID: string(NewID()), IdleNativeID: string(NewID()), Text: "Original partial text.", Context: ClaudeStopContextText, Kind: ClaudeResultExecutionError, Reason: ClaudeAbortedStreaming, Error: true, Command: ClaudeCommandCancelled, Usage: &ClaudeResultUsage{}, Acknowledged: true, Idle: true, CleanupVerified: true}
	if retry {
		v.ContentEvidence, v.InterruptedNativeID = ClaudeRetryStreamClosed, ""
		v.BlockStopNativeID, v.MessageStopNativeID = string(NewID()), string(NewID())
		v.Retries = []ClaudeStopRetryObservation{{NativeEventID: string(NewID()), Attempt: "1", MaxRetries: "9007199254740993", DelayMS: "18446744073709551615", Error: ClaudeAPIUnknown}}
	}
	return v
}

func TestClaudeStopPreservesAbsentInputAndDistinctPartialEvidence(t *testing.T) {
	for _, retry := range []bool{false, true} {
		v := stopProofFixture(retry)
		if v.Validate() != nil {
			t.Fatal("original Stop rejected")
		}
		prior := &ClaudeMessageContent{Model: "fixture", Blocks: []ClaudeRetainedBlock{{Index: 0, Block: ClaudeTextBlock{Kind: ClaudeText, Text: v.Text}, State: ClaudeBlockStreaming}}}
		closed, err := InterruptClaudeContent(prior, MessageStreaming, v)
		if err != nil || closed.Blocks[0].State != ClaudeBlockInterrupted || closed.Interruption == nil || closed.Interruption.Evidence != v.ContentEvidence || prior.Blocks[0].State != ClaudeBlockStreaming || prior.Interruption != nil {
			t.Fatal("partial response changed completion or prior snapshot", err)
		}
		e := ExecutionEvent{Version: 1, ExecutionID: NewID(), Sequence: 12, Kind: ExecutionTurnFinished, NativeThreadID: string(NewID()), NativeTurnID: string(NewID()), Outcome: ExecutionStopped, ClaudeStop: &v}
		if e.Validate() != nil {
			t.Fatal("product Stop evidence rejected")
		}
		e.Outcome = ExecutionSucceeded
		if e.Validate() == nil {
			t.Fatal("Stop became input success")
		}
	}
}

func TestClaudeStopRejectsForgedInputAndCleanupOrNativeEvidence(t *testing.T) {
	for _, name := range []string{"input", "context", "reused", "missing-partial", "missing-retry", "fake-assistant", "native-usage", "counter", "error", "reason", "ack", "idle", "cleanup", "mixed"} {
		t.Run(name, func(t *testing.T) {
			v := stopProofFixture(true)
			switch name {
			case "input":
				id := v.InputID
				v.NativeInputID = &id
			case "context":
				v.Context = ClaudeDenialContextText
			case "reused":
				v.Retries[0].NativeEventID = v.ContextNativeID
			case "missing-partial":
				v.MessageStopNativeID = ""
			case "missing-retry":
				v.Retries = nil
			case "fake-assistant":
				v.InterruptedNativeID = string(NewID())
			case "native-usage":
				v.PartialUsage = &ClaudeProviderUsage{}
			case "counter":
				v.Retries[0].Attempt = "18446744073709551616"
			case "error":
				v.Error = false
			case "reason":
				v.Reason = ClaudeCompleted
			case "ack":
				v.Acknowledged = false
			case "idle":
				v.Idle = false
			case "cleanup":
				v.CleanupVerified = false
			case "mixed":
				v.ContentEvidence = ClaudeAbortedAssistant
			}
			if (v.Validate() == nil) != (name == "cleanup") {
				t.Fatal("Stop validation changed an input boundary", name)
			}
		})
	}
}
