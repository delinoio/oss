package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
)

func (o *inputObserver) part(raw []byte) (result *NativePart, repeated bool, problem error) {
	phase := "decode"
	defer func() {
		if problem != nil && o.logger != nil {
			o.logger.WarnContext(o.ctx, "opencode_owned_part_rejected", "owner_id", o.owner, "phase", phase, "code", "unsupported")
		}
	}()
	value, err := decodeNativePart(raw)
	if err != nil || value.SessionID != o.input.receipt.SessionID {
		fields, _ := object(raw)
		state, _ := object(fields["state"])
		timing, _ := object(state["time"])
		if o.logger != nil {
			o.logger.WarnContext(o.ctx, "opencode_part_decode_shape", "owner_id", o.owner, "task_error", scalar(fields["tool"], "task") && scalar(state["status"], "error"), "state_fields", len(state), "title_present", len(state["title"]) != 0, "output_present", len(state["output"]) != 0, "end_present", len(timing["end"]) != 0, "code", "unsupported")
		}
		return nil, false, observerProblem()
	}
	message := o.messages[value.MessageID]
	phase = "owner"
	if message == nil || o.attachments[value.ID] != "" {
		return nil, false, observerProblem()
	}
	raw = canonicalNative(raw)
	old := o.parts[value.ID]
	phase = "prior"
	if old != nil && (old.value.MessageID != value.MessageID || old.value.Kind != value.Kind) {
		return nil, false, observerProblem()
	}
	if old != nil && bytes.Equal(old.raw, raw) {
		if old.value.Text != nil && old.text != value.Text.Text {
			return nil, false, observerProblem()
		}
		copy, _ := decodeNativePart(raw)
		return &copy, true, nil
	}
	if old == nil && len(o.parts)+len(o.attachments) >= maxObservedParts {
		return nil, false, eventBound()
	}
	if message.value.User != nil {
		if value.ID != o.input.receipt.PartID || value.Kind != TextPartKind || value.Text.Timing != nil || value.Text.Synthetic != nil || value.Text.Ignored != nil || value.Text.Metadata != nil || sha256.Sum256([]byte(value.Text.Text)) != o.input.digest || old != nil {
			return nil, false, observerProblem()
		}
	} else {
		phase = "lifetime"
		if message.finalized || value.MessageID != o.progress.AssistantID {
			if o.logger != nil {
				o.logger.WarnContext(o.ctx, "opencode_part_lifetime_rejected", "owner_id", o.owner, "finalized", message.finalized, "current_message", value.MessageID == o.progress.AssistantID, "original_part", old != nil, "original_stop", o.stop != nil && o.stop.sent, "code", "unsupported")
			}
			return nil, false, observerProblem()
		}
		if old == nil && (value.Text != nil || value.Tool != nil) && message.openStep == "" {
			return nil, false, observerProblem()
		}
		switch value.Kind {
		case TextPartKind, ReasoningPartKind:
			phase = "text"
			if value.Text.Timing == nil || value.Text.Synthetic != nil || value.Text.Ignored != nil {
				return nil, false, observerProblem()
			}
			if old != nil && (old.value.Text.Timing.End != nil || old.value.Text.Timing.Start != value.Text.Timing.Start || !strings.HasPrefix(value.Text.Text, old.text)) {
				// Native completion plugins may rewrite text. This profile has
				// no such authority; retain uncertainty instead of losing deltas.
				return nil, false, observerProblem()
			}
		case ToolPartKind:
			phase = "tool"
			if err := o.tool(value, old); err != nil {
				return nil, false, err
			}
		case StepStartPartKind:
			phase = "step-start"
			if old != nil || message.openStep != "" {
				return nil, false, observerProblem()
			}
		case StepFinishPartKind:
			phase = "step-finish"
			if old != nil || message.openStep == "" {
				return nil, false, observerProblem()
			}
		case SnapshotPartKind, PatchPartKind:
			phase = "revision"
			if old != nil {
				return nil, false, observerProblem()
			}
		default:
			// Decoding media/child/retry/compaction forms does not establish
			// their execution owner, completion or restoration semantics.
			return nil, false, observerProblem()
		}
	}
	state := &observedPart{raw: raw, value: value}
	if value.Text != nil {
		state.text = value.Text.Text
	}
	o.parts[value.ID] = state
	if old == nil {
		message.parts = append(message.parts, value.ID)
	}
	if message.value.User != nil {
		o.progress.InputPartSeen = true
	}
	if value.Tool != nil {
		o.calls[value.Tool.CallID] = value.ID
		for _, attachment := range value.Tool.Attachments {
			o.attachments[attachment.ID] = value.ID
		}
	}
	if value.Kind == StepStartPartKind {
		message.openStep = value.ID
	}
	if value.Kind == StepFinishPartKind {
		message.openStep, message.lastStep = "", value.ID
	}
	copy, _ := decodeNativePart(raw)
	return &copy, false, nil
}

func (o *inputObserver) tool(value NativePart, old *observedPart) error {
	tool := value.Tool
	if tool.State == ToolError && tool.Title != nil {
		if tool.Name != "task" || old == nil || old.value.Tool.State != ToolRunning || o.stop == nil || !o.stop.sent || !interruptedTool(tool) || !reflect.DeepEqual(old.value.Tool.Title, tool.Title) {
			return observerProblem()
		}
		prior, err := object(old.value.Tool.Metadata)
		next, nextErr := object(tool.Metadata)
		if err != nil || nextErr != nil || len(next) != len(prior)+1 {
			return observerProblem()
		}
		delete(next, "interrupted")
		if !reflect.DeepEqual(prior, next) {
			return observerProblem()
		}
	}
	if tool.State == ToolCompleted || tool.State == ToolError {
		for _, interaction := range o.interactions {
			if interaction.value.Tool.CallID == tool.CallID && !interaction.closed {
				if o.stop == nil || !o.stop.sent || !interruptedTool(tool) {
					return observerProblem()
				}
			}
		}
	}
	for _, item := range []struct {
		raw json.RawMessage
		key string
	}{{tool.PartMetadata, "providerExecuted"}, {tool.Metadata, "interrupted"}} {
		fields, _ := object(item.raw)
		if raw, exists := fields[item.key]; exists {
			if _, valid := boolPointer(raw); !valid {
				return observerProblem()
			}
		}
	}
	if owner := o.calls[tool.CallID]; owner != "" && owner != value.ID {
		return observerProblem()
	}
	if tool.Timing != nil && tool.Timing.Compacted != nil {
		return observerProblem()
	}
	if old == nil {
		if tool.State != ToolPending {
			return observerProblem()
		}
	} else {
		prior := old.value.Tool
		if prior.CallID != tool.CallID || prior.Name != tool.Name || prior.State == ToolCompleted || prior.State == ToolError {
			if o.logger != nil {
				o.logger.WarnContext(o.ctx, "opencode_tool_transition_rejected", "owner_id", o.owner, "prior_state", prior.State, "next_state", tool.State, "original_call", prior.CallID == tool.CallID, "original_name", prior.Name == tool.Name, "original_stop", o.stop != nil && o.stop.sent, "code", "unsupported")
			}
			return observerProblem()
		}
		if prior.State == ToolPending {
			// Native cleanup can terminate a pending call before running. A
			// successful result still requires its applied running input.
			if tool.State == ToolCompleted {
				return observerProblem()
			}
		} else if tool.State == ToolPending || !bytes.Equal(canonicalNative(prior.Input), canonicalNative(tool.Input)) || prior.Timing.Start != tool.Timing.Start {
			return observerProblem()
		}
	}
	if len(o.parts)+len(o.attachments)+len(tool.Attachments)+1 > maxObservedParts {
		return eventBound()
	}
	for _, attachment := range tool.Attachments {
		if o.parts[attachment.ID] != nil || o.attachments[attachment.ID] != "" {
			return observerProblem()
		}
	}
	return nil
}

func (o *inputObserver) delta(fields map[string]json.RawMessage) (*NativeTextDelta, error) {
	messageID, valid := boundedString(fields["messageID"], 30, true)
	partID, ok := boundedString(fields["partID"], 30, true)
	text, good := boundedString(fields["delta"], maxHTTPBody, false)
	part := o.parts[partID]
	message := o.messages[messageID]
	if !valid || !ok || !good || !scalar(fields["field"], "text") || part == nil || message == nil || messageID != o.progress.AssistantID || message.value.Assistant == nil || message.finalized || part.value.MessageID != messageID || part.value.Text == nil || part.value.Text.Timing == nil || part.value.Text.Timing.End != nil {
		return nil, observerProblem()
	}
	if len(text) > maxHTTPBody-len(part.text) {
		return nil, eventBound()
	}
	part.text += text
	return &NativeTextDelta{MessageID: messageID, PartID: partID, Text: text}, nil
}

// Step and assistant observations overlap, so this equality only verifies the
// source's latest-step assignment. It does not add either snapshot to a ledger.
func sameUsage(a, b NativeUsage) bool { return reflect.DeepEqual(a, b) }
