package domain

import (
	"encoding/json"
)

const ClaudeStopContextText = "[Request interrupted by user]"

type ClaudeStopContentEvidence string

const (
	ClaudeAbortedAssistant  ClaudeStopContentEvidence = "aborted-assistant"
	ClaudeRetryStreamClosed ClaudeStopContentEvidence = "closed-stream-before-retry"
)

// A product Stop joins its original native control, interrupted response,
// session-level result and owned cleanup. NativeInputID deliberately remains
// absent: InputID identifies the initiating input, not a correlated result.
type ClaudeStopObservation struct {
	ContentEvidence     ClaudeStopContentEvidence    `json:"content_evidence"`
	Retries             []ClaudeStopRetryObservation `json:"retries,omitempty"`
	BlockStopNativeID   string                       `json:"block_stop_native_id,omitempty"`
	MessageStopNativeID string                       `json:"message_stop_native_id,omitempty"`
	RequestID           ID                           `json:"request_id"`
	InputID             ID                           `json:"input_id"`
	MessageID           ID                           `json:"message_id"`
	NativeMessageID     string                       `json:"native_message_id"`
	InterruptedNativeID string                       `json:"interrupted_native_id,omitempty"`
	ContextNativeID     string                       `json:"context_native_id"`
	ResultNativeID      string                       `json:"result_native_id"`
	CommandNativeID     string                       `json:"command_native_id"`
	IdleNativeID        string                       `json:"idle_native_id"`
	Text                string                       `json:"text"`
	Context             string                       `json:"context"`
	NativeInputID       *ID                          `json:"native_input_id"`
	Kind                ClaudeResultKind             `json:"kind"`
	Reason              ClaudeTerminalReason         `json:"reason"`
	Error               bool                         `json:"is_error"`
	Command             ClaudeCommandCompletion      `json:"command"`
	Usage               *ClaudeResultUsage           `json:"usage"`
	PartialUsage        *ClaudeProviderUsage         `json:"partial_usage"`
	Acknowledged        bool                         `json:"acknowledged"`
	Idle                bool                         `json:"idle"`
	CleanupVerified     bool                         `json:"cleanup_verified"`
}

func invalidClaudeStop() error {
	return Fail(InvalidArgument, "Invalid original Claude Stop evidence.", "Preserve the original control, partial response, absent result input identity and independent cleanup.")
}

func (v ClaudeStopObservation) Validate() error {
	seen := map[string]bool{}
	for _, id := range []ID{v.RequestID, v.InputID, v.MessageID} {
		if id.Validate() != nil || seen[string(id)] {
			return invalidClaudeStop()
		}
		seen[string(id)] = true
	}
	for _, id := range []string{v.ContextNativeID, v.ResultNativeID, v.CommandNativeID, v.IdleNativeID} {
		if NativeIdentity(id).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[id] {
			return invalidClaudeStop()
		}
		seen[id] = true
	}
	if v.InterruptedNativeID != "" {
		if NativeIdentity(v.InterruptedNativeID).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[v.InterruptedNativeID] {
			return invalidClaudeStop()
		}
		seen[v.InterruptedNativeID] = true
	}
	if v.BlockStopNativeID != "" {
		if NativeIdentity(v.BlockStopNativeID).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[v.BlockStopNativeID] {
			return invalidClaudeStop()
		}
		seen[v.BlockStopNativeID] = true
	}
	if v.MessageStopNativeID != "" {
		if v.BlockStopNativeID == "" || NativeIdentity(v.MessageStopNativeID).Validate(ClaudeCode, NativeTurnIdentity) != nil || seen[v.MessageStopNativeID] {
			return invalidClaudeStop()
		}
		seen[v.MessageStopNativeID] = true
	}
	if len(v.Retries) > 128 {
		return invalidClaudeStop()
	}
	for _, r := range v.Retries {
		if r.Validate() != nil || seen[r.NativeEventID] {
			return invalidClaudeStop()
		}
		seen[r.NativeEventID] = true
	}
	switch v.ContentEvidence {
	case ClaudeAbortedAssistant:
		if v.InterruptedNativeID == "" {
			return invalidClaudeStop()
		}
	case ClaudeRetryStreamClosed:
		if v.InterruptedNativeID != "" || v.BlockStopNativeID == "" || v.MessageStopNativeID == "" || len(v.Retries) == 0 || v.PartialUsage != nil {
			return invalidClaudeStop()
		}
	default:
		return invalidClaudeStop()
	}
	if Text(v.NativeMessageID, "native provider message", 1024, true) != nil || seen[v.NativeMessageID] || Text(v.Text, "interrupted native response", MaxMessageText, false) != nil || v.Context != ClaudeStopContextText || v.NativeInputID != nil || v.Kind != ClaudeResultExecutionError || v.Reason != ClaudeAbortedStreaming || !v.Error || v.Command != ClaudeCommandCancelled || !v.Acknowledged || !v.Idle || !v.CleanupVerified || v.Usage == nil {
		return invalidClaudeStop()
	}
	raw, err := json.Marshal(v.Usage)
	var copy ClaudeResultUsage
	if err != nil || len(raw) > 1<<20 || Decode(raw, &copy) != nil {
		return invalidClaudeStop()
	}
	if v.PartialUsage != nil {
		raw, err := json.Marshal(v.PartialUsage)
		var copy ClaudeProviderUsage
		if err != nil || len(raw) > 1<<20 || Decode(raw, &copy) != nil {
			return invalidClaudeStop()
		}
	}
	return nil
}

// Complete is storage finality, not provider message_stop. The dedicated
// interrupted block state retains that distinction for every reader.
func InterruptClaudeContent(prior *ClaudeMessageContent, state MessageState, proof ClaudeStopObservation) (*ClaudeMessageContent, error) {
	if proof.Validate() != nil || prior == nil || state != MessageStreaming || len(prior.Blocks) != 1 || prior.StopReason != nil || prior.StopSequence != nil || prior.Interruption != nil {
		return nil, invalidClaudeStop()
	}
	block := prior.Blocks[0]
	if block.Index != 0 || block.State != ClaudeBlockStreaming || block.Block.Kind != ClaudeText || block.Block.Tool != nil || block.Block.Text != proof.Text || block.Citations != nil {
		return nil, invalidClaudeStop()
	}
	copy := *prior
	copy.Blocks = []ClaudeRetainedBlock{block}
	copy.Blocks[0].State = ClaudeBlockInterrupted
	native := proof.InterruptedNativeID
	if proof.ContentEvidence == ClaudeRetryStreamClosed {
		native = proof.MessageStopNativeID
	}
	copy.Interruption = &ClaudeContentInterruption{RequestID: proof.RequestID, NativeEventID: native, Evidence: proof.ContentEvidence}
	return &copy, nil
}

type ClaudeContentInterruption struct {
	Evidence      ClaudeStopContentEvidence `json:"evidence"`
	RequestID     ID                        `json:"request_id"`
	NativeEventID string                    `json:"native_event_id"`
}

// Retain the existing Stop wire shape while sharing exact native retry validation.
type ClaudeStopRetryObservation = ClaudeAPIRetryObservation
