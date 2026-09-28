package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type CancellationCategory string

const MidTurnAbort CancellationCategory = "MidTurnAbort"

// InterruptedPromptResult is a separate native variant. Its totalTokens value
// describes context; this response has no model usage counters. Never populate
// successful-turn usage with zeroes or infer model calls from relay requests.
type InterruptedPromptResult struct {
	Reason StopReason `json:"stopReason"`
	Meta   struct {
		Session       domain.ID            `json:"sessionId"`
		Request       string               `json:"requestId"`
		Prompt        string               `json:"promptId"`
		ContextTokens uint64               `json:"totalTokens"`
		Model         string               `json:"modelId"`
		Category      CancellationCategory `json:"cancellationCategory"`
	} `json:"_meta"`
}

type InterruptedTurnCompleted struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind      string     `json:"sessionUpdate"`
		Prompt    string     `json:"prompt_id"`
		Reason    StopReason `json:"stop_reason"`
		ElapsedMS uint64     `json:"elapsed_ms"`
	} `json:"update"`
	Meta struct {
		Event       string               `json:"eventId"`
		TimestampMS uint64               `json:"agentTimestampMs"`
		Category    CancellationCategory `json:"cancellationCategory"`
	} `json:"_meta"`
}

type InterruptedPromptCompleted struct {
	Session     domain.ID            `json:"sessionId"`
	Prompt      string               `json:"promptId"`
	Reason      StopReason           `json:"stopReason"`
	AgentResult json.RawMessage      `json:"agentResult"`
	Category    CancellationCategory `json:"cancellationCategory"`
}

func parseInterruptedPromptResult(raw []byte, session domain.ID, prompt, model string) (InterruptedPromptResult, error) {
	var value InterruptedPromptResult
	if session.Validate() != nil || !nativeUUID(prompt, 4) || !text(model, 256) || decode(raw, &value) != nil || value.Reason != Cancelled || value.Meta.Category != MidTurnAbort || value.Meta.Session != session || value.Meta.Request != prompt || value.Meta.Prompt != prompt || value.Meta.Model != model {
		return InterruptedPromptResult{}, incompatible()
	}
	return value, nil
}

func parseInterruptedTurn(raw []byte, session domain.ID, prompt string) (InterruptedTurnCompleted, error) {
	var value InterruptedTurnCompleted
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || value.Session != session || value.Update.Kind != "turn_completed" || value.Update.Prompt != prompt || value.Update.Reason != Cancelled || value.Meta.Category != MidTurnAbort || value.Meta.TimestampMS > 253402300799999 {
		return InterruptedTurnCompleted{}, incompatible()
	}
	if _, err := eventIndex(value.Meta.Event, session); err != nil {
		return InterruptedTurnCompleted{}, err
	}
	return value, nil
}

func parseInterruptedPromptCompleted(raw []byte, session domain.ID, prompt string) (InterruptedPromptCompleted, error) {
	var value InterruptedPromptCompleted
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || value.Session != session || value.Prompt != prompt || value.Reason != Cancelled || value.Category != MidTurnAbort || !isNull(value.AgentResult) {
		return InterruptedPromptCompleted{}, incompatible()
	}
	return value, nil
}

// Matching these facts does not prove a durable original Stop claim, notification
// delivery, ownership cleanup or a restorable interrupted history checkpoint.
func matchInterruption(result InterruptedPromptResult, turn InterruptedTurnCompleted, prompt InterruptedPromptCompleted) error {
	if result.Reason != Cancelled || turn.Update.Reason != Cancelled || prompt.Reason != Cancelled || result.Meta.Category != MidTurnAbort || turn.Meta.Category != MidTurnAbort || prompt.Category != MidTurnAbort || result.Meta.Session != turn.Session || result.Meta.Session != prompt.Session || result.Meta.Prompt != turn.Update.Prompt || result.Meta.Prompt != prompt.Prompt {
		return incompatible()
	}
	return nil
}
