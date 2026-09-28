package claude

import (
	"encoding/json"
	"testing"
)

func apiRetryFixture(t *testing.T, b *ExecutionBinding) StreamEvent {
	return lifecycleMessage(t, b, "system", map[string]any{"subtype": "api_retry", "attempt": uint64(1), "max_retries": uint64(10), "retry_delay_ms": ^uint64(0), "error_status": nil, "error": "unknown"})
}

func TestClaudeAPIRetryPreservesExactCountersAndNeverAuthorizesAnotherInput(t *testing.T) {
	for _, change := range []string{"valid", "missing", "null", "negative", "fraction", "status", "error", "extra", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			b := contentFixture(t)
			event := apiRetryFixture(t, b)
			switch change {
			case "missing":
				event = lifecycleChange(t, event, "error_status", nil)
			case "null":
				event = lifecycleChange(t, event, "attempt", json.RawMessage("null"))
			case "negative":
				event = lifecycleChange(t, event, "retry_delay_ms", -1)
			case "fraction":
				event = lifecycleChange(t, event, "attempt", 1.5)
			case "status":
				event = lifecycleChange(t, event, "error_status", 200)
			case "error":
				event = lifecycleChange(t, event, "error", "private error body")
			case "extra":
				event = lifecycleChange(t, event, "provider", "another")
			case "duplicate":
				lifecycleObserve(t, b, event)
			}
			o, err := b.Observe(event)
			if change == "valid" {
				if err != nil || o.Kind != ProgressObserved || o.Progress.APIRetry == nil || o.Progress.APIRetry.DelayMS != ^uint64(0) || o.Progress.APIRetry.ErrorStatus != nil || b.finished || b.interruptRetryObserved {
					t.Fatal("retry invented outcome or lost original metadata", err)
				}
			} else if err == nil {
				t.Fatal("invalid native retry accepted")
			}
		})
	}
}

func TestClaudeStopClosedStreamNeedsIndependentRetryContextAndResult(t *testing.T) {
	for _, scenario := range []string{"valid", "no-control", "no-retry", "duplicate-stop", "delta-after-close"} {
		t.Run(scenario, func(t *testing.T) {
			b, _ := interruptedTextFixture(t)
			if scenario == "no-control" {
				b.interrupt = nil
			}
			blockStop := contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0})
			o, err := b.Observe(blockStop)
			if scenario == "no-control" {
				if err == nil {
					t.Fatal("unclaimed interruption accepted")
				}
				return
			}
			if err != nil || o.Content[0].Kind != ContentInterruptStreamClosed || b.interruptedMessage {
				t.Fatal("stream close became aborted assistant", err)
			}
			if scenario == "duplicate-stop" {
				if _, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_stop", "index": 0})); err == nil {
					t.Fatal("duplicate stream close accepted")
				}
				return
			}
			if scenario == "delta-after-close" {
				if _, err := b.Observe(contentPartial(t, b, "", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "later"}})); err == nil {
					t.Fatal("closed stream changed text")
				}
				return
			}
			lifecycleObserve(t, b, contentPartial(t, b, "", map[string]any{"type": "message_stop"}))
			if scenario != "no-retry" {
				lifecycleObserve(t, b, apiRetryFixture(t, b))
			}
			o, err = b.Observe(contentResult(t, b, "", map[string]any{"type": "text", "text": "[Request interrupted by user]"}))
			if scenario == "no-retry" {
				if err == nil {
					t.Fatal("missing retry evidence fabricated cancellation")
				}
				return
			}
			if err != nil || o.Content[0].Kind != NativeInterruptContext || b.content.active[""] != nil || !b.interruptedRetry || b.interruptedMessage || b.finished {
				t.Fatal("retry stop altered native outcome", err)
			}
			result := lifecycleChange(t, lifecycleResult(t, b, AbortedStreaming, true), "user_message_uuid", nil)
			o = lifecycleObserve(t, b, result)
			if o.Kind != InterruptResultObserved || o.InputID != "" || o.Accepted {
				t.Fatal("retry Stop invented result input identity")
			}
		})
	}
}
