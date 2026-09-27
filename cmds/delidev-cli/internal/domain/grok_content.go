package domain

import (
	"strconv"
	"strings"
)

// Decimal strings preserve original unsigned native counters across Resource
// JSON and JavaScript. Context estimates are not model-response usage.
type GrokTextMetadata struct {
	EventID       string `json:"event_id"`
	ChunkID       string `json:"chunk_id"`
	ContextTokens string `json:"context_tokens"`
	TimestampMS   string `json:"timestamp_ms"`
	StreamStartMS string `json:"stream_start_ms"`
	TurnStartMS   string `json:"turn_start_ms"`
}

type GrokTextUpdate struct {
	ID              ID               `json:"id"`
	ResponseOrdinal uint32           `json:"response_ordinal"`
	Metadata        GrokTextMetadata `json:"metadata"`
	Text            string           `json:"text"`
}

// A product text span is anchored by its first original chunk event. Grok
// supplies no native message identity or commentary/final classification here.
type GrokTextContent struct {
	ResponseOrdinal uint32             `json:"response_ordinal"`
	Chunks          []GrokTextMetadata `json:"chunks"`
}

type GrokResponseCounts struct {
	Input         string `json:"input_tokens"`
	Output        string `json:"output_tokens"`
	CachedRead    string `json:"cache_read_input_tokens"`
	CacheCreation string `json:"cache_creation_input_tokens"`
	Reasoning     string `json:"reasoning_tokens"`
}

// The original response_completed notification has no response/event/prompt
// ID, total, price, duration or model. Ordinal is product ordering within the
// independently accepted original input, never a fabricated native identity.
type GrokResponseUsage struct {
	Ordinal uint32             `json:"ordinal"`
	Counts  GrokResponseCounts `json:"counts"`
}

type GrokUsageRecord struct {
	ExecutionID  ID                `json:"execution_id"`
	AccountID    ID                `json:"account_id"`
	ConnectionID ID                `json:"connection_id"`
	ProviderID   ID                `json:"provider_id"`
	ModelID      ID                `json:"model_id"`
	Harness      Harness           `json:"harness"`
	Version      string            `json:"native_version"`
	ThreadID     string            `json:"native_thread_id"`
	TurnID       string            `json:"native_turn_id"`
	Sequence     uint64            `json:"sequence"`
	Usage        GrokResponseUsage `json:"grok_observation"`
}

type GrokContentState struct {
	Responses     uint32 `json:"responses"`
	MessageID     ID     `json:"message_id,omitempty"`
	MessageBytes  uint32 `json:"message_bytes"`
	MessageChunks uint32 `json:"message_chunks"`
	TextBytes     uint32 `json:"text_bytes"`
	LastEvent     string `json:"last_event,omitempty"`
	LastChunk     string `json:"last_chunk,omitempty"`
}

func grokCount(s string) (uint64, bool) {
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil && strconv.FormatUint(v, 10) == s
}

func GrokEventIndex(event, session string) (uint64, error) {
	prefix := session + "-"
	if NativeIdentity(session).Validate(GrokBuild, NativeThreadIdentity) != nil || !strings.HasPrefix(event, prefix) {
		return 0, invalidGrokContent()
	}
	v, ok := grokCount(strings.TrimPrefix(event, prefix))
	if !ok {
		return 0, invalidGrokContent()
	}
	return v, nil
}

func (m GrokTextMetadata) Validate(session string) error {
	if _, err := GrokEventIndex(m.EventID, session); err != nil {
		return err
	}
	if n, ok := grokCount(m.ChunkID); !ok || n == 0 {
		return invalidGrokContent()
	}
	if _, ok := grokCount(m.ContextTokens); !ok {
		return invalidGrokContent()
	}
	for _, stamp := range []string{m.TimestampMS, m.StreamStartMS, m.TurnStartMS} {
		if n, ok := grokCount(stamp); !ok || n > 253402300799999 {
			return invalidGrokContent()
		}
	}
	return nil
}

func (v GrokTextUpdate) Validate(session string) error {
	if v.ID.Validate() != nil || v.ResponseOrdinal == 0 || v.ResponseOrdinal > 128 || Text(v.Text, "original Grok text", MaxMessageText, false) != nil {
		return invalidGrokContent()
	}
	return v.Metadata.Validate(session)
}

func (v GrokResponseUsage) Validate() error {
	if v.Ordinal == 0 || v.Ordinal > 128 {
		return invalidGrokContent()
	}
	for _, s := range []string{v.Counts.Input, v.Counts.Output, v.Counts.CachedRead, v.Counts.CacheCreation, v.Counts.Reasoning} {
		if _, ok := grokCount(s); !ok {
			return invalidGrokContent()
		}
	}
	return nil
}

// Pure transitions are rechecked independently at Worker and server boundaries;
// callers commit the returned copy only after their durable publication succeeds.
func (s GrokContentState) ObserveText(v GrokTextUpdate, session string) (GrokContentState, error) {
	if v.Validate(session) != nil || v.ResponseOrdinal != s.Responses+1 || s.MessageID != "" && s.MessageID != v.ID {
		return s, invalidGrokContent()
	}
	event, _ := GrokEventIndex(v.Metadata.EventID, session)
	chunk, _ := grokCount(v.Metadata.ChunkID)
	if s.LastEvent != "" {
		previous, err := GrokEventIndex(s.LastEvent, session)
		previousChunk, valid := grokCount(s.LastChunk)
		if err != nil || !valid || event <= previous || chunk <= previousChunk {
			return s, invalidGrokContent()
		}
	}
	if s.MessageChunks >= 1024 {
		return s, Fail(ResourceExhausted, "Original Grok chunk provenance reached its retention bound.", "Retain the original transcript for recovery; no metadata was truncated.")
	}
	if uint64(s.MessageBytes)+uint64(len(v.Text)) > MaxMessageText || uint64(s.TextBytes)+uint64(len(v.Text)) > 4<<20 {
		return s, Fail(ResourceExhausted, "Original Grok text reached its retention bound.", "Retain the original transcript for recovery; no text was truncated.")
	}
	s.MessageID, s.LastEvent, s.LastChunk = v.ID, v.Metadata.EventID, v.Metadata.ChunkID
	s.MessageChunks++
	s.MessageBytes += uint32(len(v.Text))
	s.TextBytes += uint32(len(v.Text))
	return s, nil
}

func (s GrokContentState) ObserveResponse(v GrokResponseUsage) (GrokContentState, error) {
	if v.Validate() != nil || v.Ordinal != s.Responses+1 {
		return s, invalidGrokContent()
	}
	s.Responses, s.MessageID, s.MessageBytes, s.MessageChunks = v.Ordinal, "", 0, 0
	return s, nil
}

func invalidGrokContent() error {
	return Fail(InvalidArgument, "The original Grok content observation is inconsistent.", "Retain exact original input ordering and native counters without inventing identities or totals.")
}
