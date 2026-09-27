package domain

import "encoding/json"

type ClaudeInterruptionKind string
type ClaudeInterruptionResultKind string
type ClaudeInterruptionReason string

const (
	ClaudeDenialContext     ClaudeInterruptionKind       = "denial-context"
	ClaudeDenialResult      ClaudeInterruptionKind       = "denial-session-result"
	ClaudeInterruptionError ClaudeInterruptionResultKind = "error_during_execution"
	ClaudeToolsAborted      ClaudeInterruptionReason     = "aborted_tools"
	ClaudeDenialContextText                              = "[Request interrupted by user for tool use]"
)

// The original session-level result does not identify a product input. Keep
// that absence explicit even though its initiating callback has an input owner.
type ClaudeInterruptionResult struct {
	Kind          ClaudeInterruptionResultKind `json:"kind"`
	Reason        ClaudeInterruptionReason     `json:"reason"`
	Error         bool                         `json:"is_error"`
	NativeInputID *ID                          `json:"native_input_id"`
	Usage         *ClaudeResultUsage           `json:"usage"`
}

type ClaudeInterruption struct {
	Kind               ClaudeInterruptionKind    `json:"kind"`
	NativeEventID      string                    `json:"native_event_id"`
	InteractionID      ID                        `json:"interaction_id"`
	ArrivalID          ID                        `json:"arrival_id"`
	ToolMessageID      ID                        `json:"tool_message_id"`
	ToolResultNativeID string                    `json:"tool_result_native_id"`
	Context            *string                   `json:"context"`
	Result             *ClaudeInterruptionResult `json:"result"`
}

type ExecutionClaudeInterruption struct {
	ID          ID                 `json:"id"`
	Observation ClaudeInterruption `json:"observation"`
}

// Only references are retained on execution progress. Conversation context and
// overlapping native usage stay in their original immutable message records.
type ClaudeInterruptionProgress struct {
	InteractionID ID `json:"interaction_id"`
	ContextID     ID `json:"context_id"`
	ResultID      ID `json:"result_id,omitempty"`
}

func invalidClaudeInterruption() error {
	return Fail(InvalidArgument, "Invalid original Claude interruption observation.", "Retain the original callback, separate context and absent input-result identity.")
}

func (u ExecutionClaudeInterruption) Validate() error {
	if u.ID.Validate() != nil {
		return invalidClaudeInterruption()
	}
	return u.Observation.Validate()
}

func (v ClaudeInterruption) Validate() error {
	if v.InteractionID.Validate() != nil || v.ArrivalID.Validate() != nil || v.ToolMessageID.Validate() != nil || NativeIdentity(v.NativeEventID).Validate(ClaudeCode, NativeTurnIdentity) != nil || NativeIdentity(v.ToolResultNativeID).Validate(ClaudeCode, NativeTurnIdentity) != nil || v.NativeEventID == v.ToolResultNativeID {
		return invalidClaudeInterruption()
	}
	switch v.Kind {
	case ClaudeDenialContext:
		if v.Context == nil || *v.Context != ClaudeDenialContextText || v.Result != nil {
			return invalidClaudeInterruption()
		}
	case ClaudeDenialResult:
		r := v.Result
		if v.Context != nil || r == nil || r.Kind != ClaudeInterruptionError || r.Reason != ClaudeToolsAborted || !r.Error || r.NativeInputID != nil || r.Usage == nil {
			return invalidClaudeInterruption()
		}
		raw, err := json.Marshal(r.Usage)
		var copy ClaudeResultUsage
		if err != nil || len(raw) > 1<<20 || Decode(raw, &copy) != nil {
			return invalidClaudeInterruption()
		}
	default:
		return invalidClaudeInterruption()
	}
	return nil
}
