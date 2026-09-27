package grok

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// StopReason preserves ACP's native distinction. Parsing a reason alone never
// proves original input ownership, durable publication or process cleanup.
type StopReason string

const (
	EndTurn         StopReason = "end_turn"
	MaxTokens       StopReason = "max_tokens"
	MaxTurnRequests StopReason = "max_turn_requests"
	Refusal         StopReason = "refusal"
	Cancelled       StopReason = "cancelled"
)

func (r StopReason) valid() bool {
	switch r {
	case EndTurn, MaxTokens, MaxTurnRequests, Refusal, Cancelled:
		return true
	default:
		return false
	}
}

// ModelUsage contains only original native counters. Zero remains an observed
// native value; these counters do not estimate auxiliary requests or billing.
type ModelUsage struct {
	Input         uint64 `json:"inputTokens"`
	Output        uint64 `json:"outputTokens"`
	Total         uint64 `json:"totalTokens"`
	CachedRead    uint64 `json:"cachedReadTokens"`
	CacheCreation uint64 `json:"cacheCreationTokens"`
	Reasoning     uint64 `json:"reasoningTokens"`
	Calls         uint64 `json:"modelCalls"`
	DurationMS    uint64 `json:"apiDurationMs"`
}

type TurnUsage struct {
	Input         uint64          `json:"inputTokens"`
	Output        uint64          `json:"outputTokens"`
	Total         uint64          `json:"totalTokens"`
	CachedRead    uint64          `json:"cachedReadTokens"`
	CacheCreation uint64          `json:"cacheCreationTokens"`
	Reasoning     uint64          `json:"reasoningTokens"`
	Calls         uint64          `json:"modelCalls"`
	DurationMS    uint64          `json:"apiDurationMs"`
	Models        json.RawMessage `json:"modelUsage"`
	Turns         uint64          `json:"numTurns"`
}

type PromptResult struct {
	Reason StopReason `json:"stopReason"`
	Meta   struct {
		Session    domain.ID `json:"sessionId"`
		Request    string    `json:"requestId"`
		Prompt     string    `json:"promptId"`
		Total      uint64    `json:"totalTokens"`
		Model      string    `json:"modelId"`
		Input      uint64    `json:"inputTokens"`
		Output     uint64    `json:"outputTokens"`
		CachedRead uint64    `json:"cachedReadTokens"`
		Reasoning  uint64    `json:"reasoningTokens"`
		Usage      TurnUsage `json:"usage"`
	} `json:"_meta"`
}

func validateUsage(value TurnUsage, model string) (ModelUsage, error) {
	if !text(model, 256) {
		return ModelUsage{}, incompatible()
	}
	var models map[string]json.RawMessage
	if json.Unmarshal(value.Models, &models) != nil || len(models) != 1 {
		return ModelUsage{}, incompatible()
	}
	var selected ModelUsage
	if decode(models[model], &selected) != nil {
		return ModelUsage{}, incompatible()
	}
	// Singleton-model totals must retain the same native observation. Never
	// derive total/input/output arithmetic or add omitted auxiliary usage.
	if selected != (ModelUsage{Input: value.Input, Output: value.Output, Total: value.Total, CachedRead: value.CachedRead, CacheCreation: value.CacheCreation, Reasoning: value.Reasoning, Calls: value.Calls, DurationMS: value.DurationMS}) {
		return ModelUsage{}, incompatible()
	}
	return selected, nil
}

func parsePromptResult(raw []byte, session domain.ID, prompt, model string) (PromptResult, error) {
	var value PromptResult
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || !value.Reason.valid() || value.Meta.Session != session || value.Meta.Request != prompt || value.Meta.Prompt != prompt || value.Meta.Model != model {
		return PromptResult{}, incompatible()
	}
	usage := value.Meta.Usage
	if _, err := validateUsage(usage, model); err != nil {
		return PromptResult{}, err
	}
	if value.Meta.Input != usage.Input || value.Meta.Output != usage.Output || value.Meta.Total != usage.Total || value.Meta.CachedRead != usage.CachedRead || value.Meta.Reasoning != usage.Reasoning {
		return PromptResult{}, incompatible()
	}
	return value, nil
}

type TextChunk struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind    string `json:"sessionUpdate"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"update"`
	Meta struct {
		ContextTokens uint64 `json:"totalTokens"`
		Event         string `json:"eventId"`
		TimestampMS   uint64 `json:"agentTimestampMs"`
		Prompt        string `json:"promptId"`
		StreamStartMS uint64 `json:"streamStartMs"`
		TurnStartMS   uint64 `json:"turnStartMs"`
		Type          string `json:"updateType"`
		Chunk         uint64 `json:"chunkId"`
	} `json:"_meta"`
}

func eventIndex(event string, session domain.ID) (uint64, error) {
	prefix := string(session) + "-"
	if !strings.HasPrefix(event, prefix) {
		return 0, incompatible()
	}
	suffix := strings.TrimPrefix(event, prefix)
	index, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil || strconv.FormatUint(index, 10) != suffix {
		return 0, incompatible()
	}
	return index, nil
}

func parseTextChunk(raw []byte, session domain.ID, prompt string) (TextChunk, error) {
	var value TextChunk
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || value.Session != session || value.Update.Kind != "agent_message_chunk" || value.Update.Content.Type != "text" || domain.Text(value.Update.Content.Text, "native text chunk", 256<<10, false) != nil || value.Meta.Prompt != prompt || value.Meta.Type != "AgentMessageChunk" || value.Meta.Chunk == 0 {
		return TextChunk{}, incompatible()
	}
	if _, err := eventIndex(value.Meta.Event, session); err != nil {
		return TextChunk{}, err
	}
	for _, stamp := range []uint64{value.Meta.TimestampMS, value.Meta.StreamStartMS, value.Meta.TurnStartMS} {
		if stamp > 253402300799999 {
			return TextChunk{}, incompatible()
		}
	}
	return value, nil
}

type TurnCompleted struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind      string     `json:"sessionUpdate"`
		Prompt    string     `json:"prompt_id"`
		Reason    StopReason `json:"stop_reason"`
		Usage     TurnUsage  `json:"usage"`
		ElapsedMS uint64     `json:"elapsed_ms"`
	} `json:"update"`
	Meta struct {
		Event       string `json:"eventId"`
		TimestampMS uint64 `json:"agentTimestampMs"`
	} `json:"_meta"`
}

func parseTurnCompleted(raw []byte, session domain.ID, prompt, model string) (TurnCompleted, error) {
	var value TurnCompleted
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || value.Session != session || value.Update.Kind != "turn_completed" || value.Update.Prompt != prompt || !value.Update.Reason.valid() || value.Meta.TimestampMS > 253402300799999 {
		return TurnCompleted{}, incompatible()
	}
	if _, err := eventIndex(value.Meta.Event, session); err != nil {
		return TurnCompleted{}, err
	}
	if _, err := validateUsage(value.Update.Usage, model); err != nil {
		return TurnCompleted{}, err
	}
	return value, nil
}

type PromptCompleted struct {
	Session     domain.ID       `json:"sessionId"`
	Prompt      string          `json:"promptId"`
	Reason      StopReason      `json:"stopReason"`
	AgentResult json.RawMessage `json:"agentResult"`
}

func parsePromptCompleted(raw []byte, session domain.ID, prompt string) (PromptCompleted, error) {
	var value PromptCompleted
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || value.Session != session || value.Prompt != prompt || !value.Reason.valid() || !isNull(value.AgentResult) {
		return PromptCompleted{}, incompatible()
	}
	return value, nil
}

// matchCompletion correlates the original RPC result and two independent native
// terminal notifications. Their presence is not an inference retry or cleanup
// grant, and cannot supply ownership when the original queue binding is absent.
func matchCompletion(result PromptResult, turn TurnCompleted, prompt PromptCompleted, model string) error {
	if result.Meta.Session != turn.Session || result.Meta.Session != prompt.Session || result.Meta.Prompt != turn.Update.Prompt || result.Meta.Prompt != prompt.Prompt || result.Reason != turn.Update.Reason || result.Reason != prompt.Reason || result.Meta.Usage.Turns != turn.Update.Usage.Turns {
		return incompatible()
	}
	first, err := validateUsage(result.Meta.Usage, model)
	if err != nil {
		return err
	}
	second, err := validateUsage(turn.Update.Usage, model)
	if err != nil || first != second {
		return incompatible()
	}
	return nil
}
