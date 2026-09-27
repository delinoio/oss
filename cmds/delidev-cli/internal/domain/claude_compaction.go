package domain

import (
	"bytes"
	"encoding/json"
	"strconv"
)

type ClaudeCompactionTrigger string

const (
	ClaudeAutomaticCompaction ClaudeCompactionTrigger = "auto"
	ClaudeManualCompaction    ClaudeCompactionTrigger = "manual"
)

type ClaudePreservedSegment struct {
	Head   string `json:"head_uuid"`
	Anchor string `json:"anchor_uuid"`
	Tail   string `json:"tail_uuid"`
}

type ClaudePreservedMessages struct {
	Anchor string   `json:"anchor_uuid"`
	IDs    []string `json:"uuids"`
	AllIDs []string `json:"all_uuids,omitempty"`
}

// These are original context observations, not usage, input acceptance or
// permission to replace the retained transcript with a generated summary.
type ClaudeCompactionBoundary struct {
	Trigger           ClaudeCompactionTrigger  `json:"trigger"`
	Before            ClaudeProgressCount      `json:"pre_tokens"`
	After             *ClaudeProgressCount     `json:"post_tokens,omitempty"`
	DurationMS        *ClaudeProgressCount     `json:"duration_ms,omitempty"`
	CumulativeDropped *ClaudeProgressCount     `json:"cumulative_dropped_tokens,omitempty"`
	LogicalParent     *string                  `json:"logical_parent_uuid,omitempty"`
	Segment           *ClaudePreservedSegment  `json:"preserved_segment,omitempty"`
	Messages          *ClaudePreservedMessages `json:"preserved_messages,omitempty"`
}

type ClaudeCompactionSummary struct {
	BoundaryID        string            `json:"boundary_native_id"`
	BoundaryMessageID ID                `json:"boundary_message_id"`
	Text              *string           `json:"text"`
	Blocks            []ClaudeTextBlock `json:"blocks"`
}

func validClaudeContextID(id string) bool {
	return NativeIdentity(id).Validate(ClaudeCode, NativeTurnIdentity) == nil
}

func (v ClaudeCompactionBoundary) Validate() error {
	if v.Trigger != ClaudeAutomaticCompaction && v.Trigger != ClaudeManualCompaction || v.LogicalParent != nil && !validClaudeContextID(*v.LogicalParent) {
		return invalidClaudeProgress()
	}
	for _, counter := range []*ClaudeProgressCount{&v.Before, v.After, v.DurationMS, v.CumulativeDropped} {
		if counter == nil {
			continue
		}
		n, err := strconv.ParseUint(string(*counter), 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != string(*counter) {
			return invalidClaudeProgress()
		}
	}
	if v.Segment != nil {
		s := v.Segment
		if !validClaudeContextID(s.Head) || !validClaudeContextID(s.Anchor) || !validClaudeContextID(s.Tail) || s.Head == s.Anchor || s.Tail == s.Anchor {
			return invalidClaudeProgress()
		}
	}
	if v.Messages != nil {
		m := v.Messages
		if !validClaudeContextID(m.Anchor) || len(m.IDs) == 0 || len(m.IDs) > 4096 || m.AllIDs != nil && len(m.AllIDs) == 0 || len(m.AllIDs) > 4096 || v.Segment != nil && v.Segment.Anchor != m.Anchor {
			return invalidClaudeProgress()
		}
		for _, ids := range [][]string{m.IDs, m.AllIDs} {
			seen := map[string]bool{m.Anchor: true}
			for _, id := range ids {
				if !validClaudeContextID(id) || seen[id] {
					return invalidClaudeProgress()
				}
				seen[id] = true
			}
		}
	}
	return nil
}

func (v ClaudeCompactionBoundary) SummaryAnchor() string {
	if v.Messages != nil {
		return v.Messages.Anchor
	}
	if v.Segment != nil {
		return v.Segment.Anchor
	}
	return ""
}

func (v ClaudeCompactionSummary) Validate() error {
	if !validClaudeContextID(v.BoundaryID) || v.BoundaryMessageID.Validate() != nil || v.Text != nil && v.Blocks != nil || v.Text == nil && (len(v.Blocks) == 0 || len(v.Blocks) > 128) {
		return invalidClaudeProgress()
	}
	size := 0
	if v.Text != nil {
		if Text(*v.Text, "native compaction summary", MaxMessageText, false) != nil {
			return invalidClaudeProgress()
		}
		size = len(*v.Text)
	}
	for _, block := range v.Blocks {
		if block.Kind != ClaudeText || block.Validate() != nil {
			return invalidClaudeProgress()
		}
		size += len(block.Text)
	}
	if size > MaxMessageText {
		return invalidClaudeProgress()
	}
	return nil
}

type ClaudeCompactionPending struct {
	Trigger           ClaudeCompactionTrigger `json:"trigger"`
	BoundaryID        string                  `json:"boundary_native_id"`
	BoundaryMessageID ID                      `json:"boundary_message_id"`
	AnchorID          string                  `json:"anchor_native_id"`
}

type ClaudeCompactionState struct {
	Pending *ClaudeCompactionPending `json:"pending,omitempty"`
}

func (s *ClaudeCompactionState) Closed() bool { return s == nil || s.Pending == nil }

// Apply after the original publication receipt commits. Summary text stays in
// its immutable observation, never in current ownership state.
func ApplyClaudeCompaction(prior *ClaudeCompactionState, event ExecutionClaudeProgress) (*ClaudeCompactionState, error) {
	if event.Validate() != nil {
		return nil, invalidClaudeProgress()
	}
	v := event.Observation
	next := &ClaudeCompactionState{}
	switch v.Kind {
	case ClaudeCompactionProgress:
		if !prior.Closed() {
			return nil, invalidClaudeProgress()
		}
		anchor := v.Compaction.SummaryAnchor()
		if anchor != "" && anchor != v.NativeEventID {
			next.Pending = &ClaudeCompactionPending{Trigger: v.Compaction.Trigger, BoundaryID: v.NativeEventID, BoundaryMessageID: event.ID, AnchorID: anchor}
		}
	case ClaudeCompactionSummaryProgress:
		if prior == nil || prior.Pending == nil || v.NativeEventID != prior.Pending.AnchorID || v.CompactionSummary.BoundaryID != prior.Pending.BoundaryID || v.CompactionSummary.BoundaryMessageID != prior.Pending.BoundaryMessageID || v.CompactionSummary.Text != nil && prior.Pending.Trigger != ClaudeManualCompaction {
			return nil, invalidClaudeProgress()
		}
	default:
		return nil, invalidClaudeProgress()
	}
	return next, nil
}

func rejectClaudeCompactionNull(raw []byte, keys ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return true
	}
	for _, key := range keys {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return true
		}
	}
	return false
}

func (v *ClaudeCompactionBoundary) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCompactionBoundary
	var decoded plain
	if Decode(raw, &decoded) != nil || rejectClaudeCompactionNull(raw, "post_tokens", "duration_ms", "cumulative_dropped_tokens", "logical_parent_uuid", "preserved_segment", "preserved_messages") {
		return invalidClaudeProgress()
	}
	*v = ClaudeCompactionBoundary(decoded)
	return v.Validate()
}
func (v *ClaudePreservedMessages) UnmarshalJSON(raw []byte) error {
	type plain ClaudePreservedMessages
	var decoded plain
	if Decode(raw, &decoded) != nil || rejectClaudeCompactionNull(raw, "all_uuids") {
		return invalidClaudeProgress()
	}
	*v = ClaudePreservedMessages(decoded)
	return nil
}
func (v *ClaudeCompactionSummary) UnmarshalJSON(raw []byte) error {
	type plain ClaudeCompactionSummary
	var decoded plain
	var fields map[string]json.RawMessage
	if Decode(raw, &decoded) != nil || json.Unmarshal(raw, &fields) != nil || len(fields["text"]) == 0 || len(fields["blocks"]) == 0 {
		return invalidClaudeProgress()
	}
	*v = ClaudeCompactionSummary(decoded)
	return v.Validate()
}
