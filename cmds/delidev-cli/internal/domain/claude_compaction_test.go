package domain

import (
	"bytes"
	"encoding/json"
	"testing"
)

func compactionFixture() (ExecutionClaudeProgress, ExecutionClaudeProgress) {
	anchor := string(NewID())
	boundary := ExecutionClaudeProgress{ID: NewID(), Observation: ClaudeProgressObservation{NativeEventID: string(NewID()), Kind: ClaudeCompactionProgress, Compaction: &ClaudeCompactionBoundary{Trigger: ClaudeAutomaticCompaction, Before: "18446744073709551615", Messages: &ClaudePreservedMessages{Anchor: anchor, IDs: []string{string(NewID())}}}}}
	summary := ExecutionClaudeProgress{ID: NewID(), Observation: ClaudeProgressObservation{NativeEventID: anchor, Kind: ClaudeCompactionSummaryProgress, CompactionSummary: &ClaudeCompactionSummary{BoundaryID: boundary.Observation.NativeEventID, BoundaryMessageID: boundary.ID, Blocks: []ClaudeTextBlock{{Kind: ClaudeText, Text: "Original native context."}}}}}
	return boundary, summary
}

func TestClaudeCompactionKeepsExactCountsAndOriginalSummaryOwnership(t *testing.T) {
	b, s := compactionFixture()
	pending, err := ApplyClaudeCompaction(nil, b)
	if err != nil || pending.Closed() {
		t.Fatal("boundary lost original pending summary", err)
	}
	if _, err := ApplyClaudeCompaction(pending, b); err == nil {
		t.Fatal("overlapping boundary accepted")
	}
	closed, err := ApplyClaudeCompaction(pending, s)
	if err != nil || !closed.Closed() || pending.Closed() {
		t.Fatal("summary lost independent immutable transition", err)
	}
	if _, err := ApplyClaudeCompaction(closed, s); err == nil {
		t.Fatal("duplicate summary accepted")
	}
	if b.Observation.Compaction.Before != "18446744073709551615" {
		t.Fatal("context count rounded")
	}
}

func TestClaudeCompactionRejectsMixedMalformedAndForeignEvidence(t *testing.T) {
	for _, change := range []string{"negative", "overflow", "leading-zero", "trigger", "duplicate-preserved", "anchor-in-list", "conflicting-anchor", "empty-list", "logical-parent", "mixed", "foreign-boundary", "foreign-reference", "foreign-anchor", "orphan", "automatic-text", "mixed-text", "rich", "summary-overflow"} {
		t.Run(change, func(t *testing.T) {
			b, s := compactionFixture()
			pending, err := ApplyClaudeCompaction(nil, b)
			if err != nil {
				t.Fatal(err)
			}
			boundary := true
			switch change {
			case "negative":
				b.Observation.Compaction.Before = "-1"
			case "overflow":
				b.Observation.Compaction.Before = "18446744073709551616"
			case "leading-zero":
				b.Observation.Compaction.Before = "00"
			case "trigger":
				b.Observation.Compaction.Trigger = "unknown"
			case "duplicate-preserved":
				m := b.Observation.Compaction.Messages
				m.IDs = append(m.IDs, m.IDs[0])
			case "anchor-in-list":
				m := b.Observation.Compaction.Messages
				m.IDs = []string{m.Anchor}
			case "conflicting-anchor":
				b.Observation.Compaction.Segment = &ClaudePreservedSegment{Head: string(NewID()), Anchor: string(NewID()), Tail: string(NewID())}
			case "empty-list":
				b.Observation.Compaction.Messages.AllIDs = []string{}
			case "logical-parent":
				v := "foreign"
				b.Observation.Compaction.LogicalParent = &v
			case "mixed":
				b.Observation.Thinking = &ClaudeThinkingObservation{Tokens: "0", Delta: "0"}
			default:
				boundary = false
				switch change {
				case "foreign-boundary":
					s.Observation.CompactionSummary.BoundaryID = string(NewID())
				case "foreign-reference":
					s.Observation.CompactionSummary.BoundaryMessageID = NewID()
				case "foreign-anchor":
					s.Observation.NativeEventID = string(NewID())
				case "orphan":
					pending = nil
				case "automatic-text", "mixed-text":
					v := "Original context"
					s.Observation.CompactionSummary.Text = &v
					if change == "automatic-text" {
						s.Observation.CompactionSummary.Blocks = nil
					}
				case "rich":
					s.Observation.CompactionSummary.Blocks[0].Kind = ClaudeThinking
				case "summary-overflow":
					s.Observation.CompactionSummary.Blocks[0].Text = string(make([]byte, MaxMessageText+1))
				}
			}
			if boundary {
				_, err = ApplyClaudeCompaction(nil, b)
			} else {
				_, err = ApplyClaudeCompaction(pending, s)
			}
			if err == nil {
				t.Fatal("invalid native context accepted")
			}
		})
	}
}

func TestClaudeCompactionJSONPreservesRequiredAndOptionalPresence(t *testing.T) {
	b, s := compactionFixture()
	boundary, _ := json.Marshal(b.Observation.Compaction)
	for _, field := range []string{"post_tokens", "duration_ms", "cumulative_dropped_tokens", "logical_parent_uuid", "preserved_segment", "preserved_messages"} {
		raw := append([]byte(`{"`+field+`":null,`), boundary[1:]...)
		var decoded ClaudeCompactionBoundary
		if Decode(raw, &decoded) == nil {
			t.Fatal("explicit null context field accepted", field)
		}
	}
	summary, _ := json.Marshal(s.Observation.CompactionSummary)
	var decoded ClaudeCompactionSummary
	if Decode(bytes.Replace(summary, []byte(`"text":null,`), nil, 1), &decoded) == nil {
		t.Fatal("missing summary text discriminator accepted")
	}
	b.Observation.Compaction.Trigger = ClaudeManualCompaction
	pending, err := ApplyClaudeCompaction(nil, b)
	if err != nil {
		t.Fatal(err)
	}
	text := "Original manual context"
	s.Observation.CompactionSummary.Text = &text
	s.Observation.CompactionSummary.Blocks = nil
	if _, err := ApplyClaudeCompaction(pending, s); err != nil {
		t.Fatal("original manual text shape rejected", err)
	}
}
