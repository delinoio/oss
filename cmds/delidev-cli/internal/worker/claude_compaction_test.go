package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeCompactionFixture(t *testing.T) (*ClaudeBindingPublisher, *openCodeBindingRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	b, rpc, o, _ := claudeProgressFixture(t)
	anchor := string(domain.NewID())
	n := ^uint64(0)
	o.Kind, o.Progress, o.InputID = claude.CompactionObserved, nil, ""
	o.Compaction = &claude.NativeCompaction{Trigger: claude.AutomaticCompaction, Before: n, After: &n, Messages: &claude.PreservedMessages{Anchor: anchor, IDs: []string{string(domain.NewID())}}}
	text := "Original native context"
	s := claude.LifecycleObservation{Kind: claude.CompactionSummaryObserved, SessionID: o.SessionID, TurnID: o.TurnID, NativeID: anchor, Summary: &claude.NativeCompactionSummary{BoundaryID: o.NativeID, Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &text}}}}
	return b, rpc, o, s
}

func TestClaudeCompactionOwnershipWaitsForOriginalReceipt(t *testing.T) {
	b, rpc, o, s := claudeCompactionFixture(t)
	ctx := context.Background()
	for _, obs := range []claude.LifecycleObservation{o, s} {
		closed := b.compaction.Closed()
		rpc.lose = true

		if handled, err := b.PublishCompactionObservation(ctx, obs); !handled || err == nil {
			t.Fatal("lost compaction receipt accepted")
		}
		if b.compaction.Closed() != closed {
			t.Fatal("unacknowledged compaction changed ownership")
		}
		last := len(rpc.events) - 1
		rpc.lose = false
		if err := b.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rpc.events[last], rpc.events[last+1]) || rpc.requests[last] != rpc.requests[last+1] {
			t.Fatal("context receipt changed")
		}
		if b.compaction.Closed() == closed || b.stage != claudeSessionBound {
			t.Fatal("compaction changed input acceptance or lost state")
		}
		var e domain.ExecutionEvent
		if domain.Decode(rpc.events[last], &e) != nil || e.ClaudeProgress.Observation.InputAccepted {
			t.Fatal("context accepted original input")
		}
		if e.ClaudeProgress.Observation.Compaction != nil && *e.ClaudeProgress.Observation.Compaction.After != "18446744073709551615" {
			t.Fatal("context count rounded")
		}
	}
}

func TestClaudeCompactionRejectsForeignMixedAndReusedObservations(t *testing.T) {
	for _, change := range []string{"input", "accepted", "session", "turn", "mixed", "foreign-summary", "orphan", "duplicate", "rich"} {
		t.Run(change, func(t *testing.T) {
			b, rpc, o, s := claudeCompactionFixture(t)
			switch change {
			case "input":
				o.InputID = b.journal.InputID
			case "accepted":
				o.Accepted = true
			case "session":
				o.SessionID = domain.NewID()
			case "turn":
				o.TurnID = string(domain.NewID())
			case "mixed":
				o.Progress = &claude.NativeProgressObservation{Kind: claude.SessionStatusObserved}
			case "foreign-summary", "duplicate", "rich":
				if _, err := b.PublishCompactionObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
				if change == "duplicate" {
					break
				}
				o = s
				if change == "foreign-summary" {
					o.Summary.BoundaryID = string(domain.NewID())
				} else {
					o.Summary.Blocks[0].Kind = claude.ThinkingBlock
				}
			case "orphan":
				o = s
			}
			before := len(rpc.events)
			if change == "session" {
				_, err := b.PublishCompactionObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if handled, err := b.PublishCompactionObservation(context.Background(), o); !handled || err == nil || len(rpc.events) != before {
				t.Fatal("invalid context was published")
			}
		})
	}
}
