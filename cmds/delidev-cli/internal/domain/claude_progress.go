package domain

import (
	"strconv"
)

type ClaudeProgressKind string
type ClaudeSessionStatus string
type ClaudeCompactResult string
type ClaudeProgressCount string

const (
	ClaudeStatusProgress   ClaudeProgressKind  = "session-status"
	ClaudeThinkingProgress ClaudeProgressKind  = "thinking-tokens-estimated"
	ClaudeAPIRetryProgress ClaudeProgressKind  = "api-retry"
	ClaudeRequesting       ClaudeSessionStatus = "requesting"
	ClaudeCompacting       ClaudeSessionStatus = "compacting"
	ClaudeCompactSucceeded ClaudeCompactResult = "success"
	ClaudeCompactFailed    ClaudeCompactResult = "failed"
)

type ClaudeStatusObservation struct {
	Status        *ClaudeSessionStatus  `json:"status"`
	Permission    *ClaudePermissionMode `json:"permission"`
	CompactResult *ClaudeCompactResult  `json:"compact_result"`
	CompactError  *string               `json:"compact_error"`
}

type ClaudeThinkingObservation struct {
	Tokens ClaudeProgressCount `json:"estimated_tokens"`
	Delta  ClaudeProgressCount `json:"estimated_tokens_delta"`
}

// InputAccepted describes observation order, not a native input acknowledgment.
// A session status may precede the original user replay.
type ClaudeProgressObservation struct {
	NativeEventID string                     `json:"native_event_id"`
	Kind          ClaudeProgressKind         `json:"kind"`
	InputAccepted bool                       `json:"input_accepted"`
	Status        *ClaudeStatusObservation   `json:"status"`
	Thinking      *ClaudeThinkingObservation `json:"thinking"`
	APIRetry      *ClaudeAPIRetryObservation `json:"api_retry,omitempty"`
}

type ExecutionClaudeProgress struct {
	ID          ID                        `json:"id"`
	Observation ClaudeProgressObservation `json:"observation"`
}

// The first observed native init/turn is pinned before input acceptance without
// populating ExecutionProgress.NativeTurnID or claiming input delivery.
type ClaudeProgressState struct {
	NativeTurnID      string                `json:"native_turn_id"`
	LatestStatusID    ID                    `json:"latest_status_id,omitempty"`
	LatestRetryID     ID                    `json:"latest_retry_id,omitempty"`
	LatestThinkingID  ID                    `json:"latest_thinking_id,omitempty"`
	Permission        *ClaudePermissionMode `json:"permission,omitempty"`
	PermissionChanged bool                  `json:"permission_changed,omitempty"`
}

func invalidClaudeProgress() error {
	return Fail(InvalidArgument, "Invalid native Claude progress observation.", "Preserve original status, exact estimates and independent input acceptance.")
}

func (u ExecutionClaudeProgress) Validate() error {
	if u.ID.Validate() != nil {
		return invalidClaudeProgress()
	}
	return u.Observation.Validate()
}

func (v ClaudeProgressObservation) Validate() error {
	if NativeIdentity(v.NativeEventID).Validate(ClaudeCode, NativeTurnIdentity) != nil {
		return invalidClaudeProgress()
	}
	switch v.Kind {
	case ClaudeStatusProgress:
		s := v.Status
		if s == nil || v.Thinking != nil || v.APIRetry != nil || s.Status != nil && *s.Status != ClaudeRequesting && *s.Status != ClaudeCompacting || s.Permission != nil && !s.Permission.Valid() || s.CompactResult != nil && *s.CompactResult != ClaudeCompactSucceeded && *s.CompactResult != ClaudeCompactFailed || s.CompactError != nil && Text(*s.CompactError, "native compact error", MaxMessageText, false) != nil {
			return invalidClaudeProgress()
		}
	case ClaudeThinkingProgress:
		if !v.InputAccepted || v.Thinking == nil || v.Status != nil || v.APIRetry != nil {
			return invalidClaudeProgress()
		}
		for _, counter := range []ClaudeProgressCount{v.Thinking.Tokens, v.Thinking.Delta} {
			value, err := strconv.ParseUint(string(counter), 10, 64)
			if err != nil || strconv.FormatUint(value, 10) != string(counter) {
				return invalidClaudeProgress()
			}
		}
	case ClaudeAPIRetryProgress:
		if v.Status != nil || v.Thinking != nil || v.APIRetry == nil || v.APIRetry.NativeEventID != v.NativeEventID || v.APIRetry.Validate() != nil {
			return invalidClaudeProgress()
		}
	default:
		return invalidClaudeProgress()
	}
	return nil
}
