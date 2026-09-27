package claude

import (
	"encoding/json"
	"testing"
)

func TestThinkingEstimatesRemainSeparateFromUsage(t *testing.T) {
	for _, name := range []string{"zero", "observed", "large", "missing", "null", "negative", "fraction", "extra", "unaccepted", "foreign", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			b := contentFixture(t)
			event := lifecycleMessage(t, b, "system", map[string]any{"subtype": "thinking_tokens", "estimated_tokens": uint64(7), "estimated_tokens_delta": uint64(7)})
			switch name {
			case "zero":
				event = lifecycleChange(t, event, "estimated_tokens", uint64(0))
				event = lifecycleChange(t, event, "estimated_tokens_delta", uint64(0))
			case "large":
				event = lifecycleChange(t, event, "estimated_tokens", ^uint64(0))
			case "missing":
				event = lifecycleChange(t, event, "estimated_tokens", nil)
			case "null":
				event = lifecycleChange(t, event, "estimated_tokens_delta", json.RawMessage("null"))
			case "negative":
				event = lifecycleChange(t, event, "estimated_tokens", -1)
			case "fraction":
				event = lifecycleChange(t, event, "estimated_tokens_delta", 1.5)
			case "extra":
				event = lifecycleChange(t, event, "provider_usage", 7)
			case "unaccepted":
				b.accepted = false
			case "foreign":
				event = lifecycleChange(t, event, "session_id", "foreign")
			case "duplicate":
				lifecycleObserve(t, b, event)
			}
			observation, err := b.Observe(event)
			valid := name == "zero" || name == "observed" || name == "large"
			if !valid {
				if err == nil || b.problem == nil {
					t.Fatal("invalid thinking estimate was accepted")
				}
				return
			}
			if err != nil || observation.Kind != ProgressObserved || observation.Progress == nil || observation.Progress.Kind != ThinkingTokensEstimated || observation.Progress.Thinking == nil || observation.Result != nil || len(observation.Content) != 0 || b.finished {
				t.Fatal("estimate fabricated usage or completion", err)
			}
			want := uint64(7)
			if name == "large" {
				want = ^uint64(0)
			}
			if name == "zero" {
				want = 0
			}
			if observation.Progress.Thinking.Tokens != want {
				t.Fatal("native estimate precision changed")
			}
		})
	}
}
