package claude

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type CompactionTrigger string

const (
	ManualCompaction          CompactionTrigger = "manual"
	AutomaticCompaction       CompactionTrigger = "auto"
	CompactionObserved        LifecycleKind     = "compaction-observed"
	CompactionSummaryObserved LifecycleKind     = "compaction-summary-observed"
)

type compactionSummaryBinding struct {
	boundary string
	anchor   string
}

// NativeCompactionSummary retains the original synthetic user context. Its
// identity is supplied by the boundary, never inferred from summary text.
type NativeCompactionSummary struct {
	BoundaryID string
	Blocks     []NativeContentBlock `json:"-"`
}

type PreservedSegment struct {
	Head   string `json:"head_uuid"`
	Anchor string `json:"anchor_uuid"`
	Tail   string `json:"tail_uuid"`
}

type PreservedMessages struct {
	Anchor string   `json:"anchor_uuid"`
	IDs    []string `json:"uuids"`
	AllIDs []string `json:"all_uuids,omitempty"`
}

// NativeCompaction is an original context boundary, not completion of a user
// input or permission to delete/rewrite transcript history. Relinking claims
// need separate exact-file verification before they can authorize recovery.
type NativeCompaction struct {
	Trigger           CompactionTrigger  `json:"trigger"`
	Before            uint64             `json:"pre_tokens"`
	After             *uint64            `json:"post_tokens,omitempty"`
	DurationMS        *uint64            `json:"duration_ms,omitempty"`
	CumulativeDropped *uint64            `json:"cumulative_dropped_tokens,omitempty"`
	LogicalParent     *string            `json:"-"`
	Segment           *PreservedSegment  `json:"preserved_segment,omitempty"`
	Messages          *PreservedMessages `json:"preserved_messages,omitempty"`
	Native            json.RawMessage    `json:"-"`
}

func decodeCompaction(raw json.RawMessage) (*NativeCompaction, error) {
	var wire struct {
		Trigger           CompactionTrigger `json:"trigger"`
		Before            *uint64           `json:"pre_tokens"`
		After             *uint64           `json:"post_tokens"`
		DurationMS        *uint64           `json:"duration_ms"`
		CumulativeDropped *uint64           `json:"cumulative_dropped_tokens"`
		Segment           json.RawMessage   `json:"preserved_segment"`
		Messages          json.RawMessage   `json:"preserved_messages"`
	}
	var fields map[string]json.RawMessage
	if decodeNativeObject(raw, &wire) != nil || json.Unmarshal(raw, &fields) != nil || wire.Before == nil || (wire.Trigger != ManualCompaction && wire.Trigger != AutomaticCompaction) {
		return nil, lifecycleUncertain()
	}
	for _, key := range []string{"post_tokens", "duration_ms", "cumulative_dropped_tokens", "preserved_segment", "preserved_messages"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return nil, lifecycleUncertain()
		}
	}
	value := &NativeCompaction{Trigger: wire.Trigger, Before: *wire.Before, After: wire.After, DurationMS: wire.DurationMS, CumulativeDropped: wire.CumulativeDropped, Native: bytes.Clone(raw)}
	if len(wire.Segment) != 0 {
		var segment PreservedSegment
		if decodeNativeObject(wire.Segment, &segment) != nil || !nativeUUID(segment.Head) || !nativeUUID(segment.Tail) || !nativeUUID(segment.Anchor) || segment.Head == segment.Anchor || segment.Tail == segment.Anchor {
			return nil, lifecycleUncertain()
		}
		value.Segment = &segment
	}
	if len(wire.Messages) != 0 {
		var messages PreservedMessages
		if decodeNativeObject(wire.Messages, &messages) != nil || !nativeUUID(messages.Anchor) || len(messages.IDs) == 0 || len(messages.IDs) > maxStreamIdentities {
			return nil, lifecycleUncertain()
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(wire.Messages, &fields)
		if fields["all_uuids"] != nil && (messages.AllIDs == nil || len(messages.AllIDs) == 0 || len(messages.AllIDs) > maxStreamIdentities) {
			return nil, lifecycleUncertain()
		}
		for _, ids := range [][]string{messages.IDs, messages.AllIDs} {
			seen := map[string]bool{messages.Anchor: true}
			for _, id := range ids {
				if !nativeUUID(id) || seen[id] {
					return nil, lifecycleUncertain()
				}
				seen[id] = true
			}
		}
		value.Messages = &messages
	}
	return value, nil
}

func (b *ExecutionBinding) observeCompaction(raw json.RawMessage) (*NativeCompaction, error) {
	if !b.initialized || (b.finished && !b.continuing) || b.command != CommandStarted || b.content.active[""] != nil || b.pendingCompaction != nil {
		return nil, lifecycleUncertain()
	}
	var envelope struct {
		Type          string          `json:"type"`
		Subtype       string          `json:"subtype"`
		ID            string          `json:"uuid"`
		Session       domain.ID       `json:"session_id"`
		Metadata      json.RawMessage `json:"compact_metadata"`
		LogicalParent *string         `json:"logical_parent_uuid"`
	}
	if decodeNativeObject(raw, &envelope) != nil || envelope.Type != "system" || envelope.Subtype != "compact_boundary" || (envelope.LogicalParent != nil && !nativeUUID(*envelope.LogicalParent)) {
		return nil, lifecycleUncertain()
	}
	value, err := decodeCompaction(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	value.LogicalParent = envelope.LogicalParent
	anchor := ""
	if value.Segment != nil {
		anchor = value.Segment.Anchor
	}
	if value.Messages != nil {
		if anchor != "" && anchor != value.Messages.Anchor {
			return nil, lifecycleUncertain()
		}
		anchor = value.Messages.Anchor
	}
	if anchor != "" && anchor != envelope.ID {
		if b.seen[anchor] || anchor == string(b.input) {
			return nil, lifecycleUncertain()
		}
		b.pendingCompaction = &compactionSummaryBinding{boundary: envelope.ID, anchor: anchor}
	}
	if b.logger != nil {
		b.logger.Debug("Claude Code compaction observed", "owner_id", b.owner, "trigger", value.Trigger)
	}
	return value, nil
}

func (b *ExecutionBinding) observeCompactionSummary(raw json.RawMessage) (*NativeCompactionSummary, error) {
	value, err := decodeCompactionSummary(raw, b.session, b.pendingCompaction)
	if err == nil {
		b.pendingCompaction = nil
	}
	return value, err
}

func decodeCompactionSummary(raw json.RawMessage, session domain.ID, pending *compactionSummaryBinding) (*NativeCompactionSummary, error) {
	var envelope struct {
		Type      string          `json:"type"`
		ID        string          `json:"uuid"`
		Session   domain.ID       `json:"session_id"`
		Parent    json.RawMessage `json:"parent_tool_use_id"`
		Timestamp string          `json:"timestamp"`
		Synthetic bool            `json:"isSynthetic"`
		Message   json.RawMessage `json:"message"`
	}
	var message struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if pending == nil || decodeNativeObject(raw, &envelope) != nil || envelope.Type != "user" || envelope.ID != pending.anchor || envelope.Session != session || !envelope.Synthetic || !bytes.Equal(bytes.TrimSpace(envelope.Parent), []byte("null")) || decodeNativeObject(envelope.Message, &message) != nil || message.Role != "user" || len(message.Content) == 0 || len(message.Content) > 128 {
		return nil, lifecycleUncertain()
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err != nil {
		return nil, lifecycleUncertain()
	}
	value := &NativeCompactionSummary{BoundaryID: pending.boundary}
	for _, raw := range message.Content {
		block, err := decodeContentBlock(raw)
		if err != nil || block.Kind != TextBlock {
			return nil, lifecycleUncertain()
		}
		value.Blocks = append(value.Blocks, block)
	}
	return value, nil
}
