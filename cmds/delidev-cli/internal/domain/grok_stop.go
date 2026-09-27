package domain

import "encoding/hex"

type GrokStopKind string
type GrokCancellationCategory string
type GrokRetryKind string
type GrokRetryError string

const (
	GrokMidTurnAbort GrokCancellationCategory = "MidTurnAbort"
	GrokRetrying     GrokRetryKind            = "retrying"
	GrokHTTPRetry    GrokRetryError           = "http"
)

type GrokStopRetry struct {
	NativeEventID string         `json:"native_event_id"`
	TimestampMS   string         `json:"timestamp_ms"`
	Kind          GrokRetryKind  `json:"kind"`
	Error         GrokRetryError `json:"error"`
	Attempt       string         `json:"attempt"`
	MaxRetries    string         `json:"max_retries"`
}

const (
	GrokInterruptedText     GrokStopKind = "interrupted-text"
	GrokCompletedDuringStop GrokStopKind = "completed-during-stop"
)

type GrokStopCompletion struct {
	Counts        GrokResponseCounts `json:"counts"`
	TotalTokens   string             `json:"total_tokens"`
	ModelCalls    string             `json:"model_calls"`
	APIDurationMS string             `json:"api_duration_ms"`
	Turns         string             `json:"turns"`
}

// A Stop owns one already accepted text input. Interrupted context is separate
// from absent usage; a raced completion retains its original counts. Neither
// variant supplies a native closed-history digest or continuation checkpoint.
type GrokStopObservation struct {
	Kind           GrokStopKind             `json:"kind"`
	RequestID      ID                       `json:"request_id"`
	InputID        ID                       `json:"input_id"`
	InputRequestID ID                       `json:"input_request_id"`
	MessageID      ID                       `json:"message_id"`
	NativeEventID  string                   `json:"native_event_id"`
	TimestampMS    string                   `json:"timestamp_ms"`
	ElapsedMS      string                   `json:"elapsed_ms"`
	Model          string                   `json:"model"`
	Category       GrokCancellationCategory `json:"category,omitempty"`
	Retries        []GrokStopRetry          `json:"retries,omitempty"`
	ContextTokens  *string                  `json:"context_tokens,omitempty"`
	Completed      *GrokStopCompletion      `json:"completed,omitempty"`
	OutputDigest   string                   `json:"output_digest"`
	TextChunks     uint32                   `json:"text_chunks"`
	Delivered      bool                     `json:"delivered"`
	Idle           bool                     `json:"idle"`
	CleanupJoined  bool                     `json:"cleanup_joined"`
}

func (v GrokStopObservation) Outcome() ExecutionOutcome {
	if v.Kind == GrokInterruptedText {
		return ExecutionStopped
	}
	if v.Kind == GrokCompletedDuringStop {
		return ExecutionSucceeded
	}
	return ""
}

func (v GrokStopObservation) Validate(thread string) error {
	seen := map[ID]bool{}
	for _, id := range []ID{v.RequestID, v.InputID, v.InputRequestID, v.MessageID} {
		if id.Validate() != nil || seen[id] || string(id) == thread {
			return invalidGrokContent()
		}
		seen[id] = true
	}
	if Text(v.Model, "original Grok model", 256, true) != nil || !v.Delivered || !v.Idle || !v.CleanupJoined || v.TextChunks == 0 || v.TextChunks > 1024 {
		return invalidGrokContent()
	}
	if _, err := GrokEventIndex(v.NativeEventID, thread); err != nil {
		return err
	}
	terminal, _ := GrokEventIndex(v.NativeEventID, thread)
	if len(v.Retries) > 3 {
		return invalidGrokContent()
	}
	var previous uint64
	for i, retry := range v.Retries {
		index, err := GrokEventIndex(retry.NativeEventID, thread)
		attempt, countOK := grokCount(retry.Attempt)
		timestamp, timeOK := grokCount(retry.TimestampMS)
		if err != nil || !countOK || attempt != uint64(i+1) || retry.MaxRetries != "3" || retry.Kind != GrokRetrying || retry.Error != GrokHTTPRetry || !timeOK || timestamp > 253402300799999 || index >= terminal || i > 0 && index <= previous {
			return invalidGrokContent()
		}
		previous = index
	}
	if n, ok := grokCount(v.TimestampMS); !ok || n > 253402300799999 {
		return invalidGrokContent()
	}
	if _, ok := grokCount(v.ElapsedMS); !ok {
		return invalidGrokContent()
	}
	digest, err := hex.DecodeString(v.OutputDigest)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != v.OutputDigest {
		return invalidGrokContent()
	}
	switch v.Kind {
	case GrokInterruptedText:
		if v.Category != GrokMidTurnAbort || v.ContextTokens == nil || v.Completed != nil {
			return invalidGrokContent()
		}
		if _, ok := grokCount(*v.ContextTokens); !ok {
			return invalidGrokContent()
		}
	case GrokCompletedDuringStop:
		c := v.Completed
		if v.Category != "" || v.ContextTokens != nil || c == nil || c.ModelCalls != "1" || c.Turns != "1" || (GrokResponseUsage{Ordinal: 1, Counts: c.Counts}).Validate() != nil {
			return invalidGrokContent()
		}
		for _, value := range []string{c.TotalTokens, c.APIDurationMS} {
			if _, ok := grokCount(value); !ok {
				return invalidGrokContent()
			}
		}
	default:
		return invalidGrokContent()
	}
	return nil
}

// MessageComplete means retained storage finality here, not response_completed.
type GrokTextInterruption struct {
	RequestID     ID     `json:"request_id"`
	NativeEventID string `json:"native_event_id"`
}
