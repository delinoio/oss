package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeCompactionPublicationFixture(t *testing.T, accepted bool) (*publicationFixture, domain.ExecutionEvent, domain.ExecutionEvent) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	seq := uint64(2)
	if accepted {
		f.publish(t, f.event(domain.ExecutionInputAccepted, seq))
		seq++
	}
	b := claudeProgressEvent(f, seq, accepted)
	anchor := string(domain.NewID())
	b.ClaudeProgress.Observation.Kind, b.ClaudeProgress.Observation.Status = domain.ClaudeCompactionProgress, nil
	b.ClaudeProgress.Observation.Compaction = &domain.ClaudeCompactionBoundary{Trigger: domain.ClaudeAutomaticCompaction, Before: "18446744073709551615", Messages: &domain.ClaudePreservedMessages{Anchor: anchor, IDs: []string{string(domain.NewID())}}}
	s := claudeProgressEvent(f, seq+1, accepted)
	s.ClaudeProgress.Observation.NativeEventID = anchor
	s.ClaudeProgress.Observation.Kind, s.ClaudeProgress.Observation.Status = domain.ClaudeCompactionSummaryProgress, nil
	s.ClaudeProgress.Observation.CompactionSummary = &domain.ClaudeCompactionSummary{BoundaryID: b.ClaudeProgress.Observation.NativeEventID, BoundaryMessageID: b.ClaudeProgress.ID, Blocks: []domain.ClaudeTextBlock{{Kind: domain.ClaudeText, Text: "Original context"}}}
	return f, b, s
}

func TestClaudeCompactionPreservesOriginalContextWithoutAcceptingInput(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		f, b, s := claudeCompactionPublicationFixture(t, accepted)
		before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		original, _ := store.Decode[domain.Session](before)
		for _, event := range []domain.ExecutionEvent{b, s} {
			receipt := f.publish(t, event)
			if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
				t.Fatal("context receipt lost", err)
			}
			row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			current, err := store.Decode[domain.Session](row)
			if err != nil || current.PendingInputs != original.PendingInputs || current.PendingInputBytes != original.PendingInputBytes || current.Outcome != original.Outcome || current.Execution.LatestUsageID != "" || current.Execution.ClaudeTerminal != nil {
				t.Fatal("context changed queue, usage or outcome", err)
			}
			closed := event.ClaudeProgress.Observation.Kind == domain.ClaudeCompactionSummaryProgress
			if current.Execution.ClaudeCompaction.Closed() != closed {
				t.Fatal("summary ownership lost")
			}
		}
	}
}

func TestClaudeCompactionRejectsForeignSummaryAtomically(t *testing.T) {
	for _, change := range []string{"anchor", "boundary", "reference", "mixed", "orphan", "overlap"} {
		t.Run(change, func(t *testing.T) {
			f, b, s := claudeCompactionPublicationFixture(t, false)
			if change != "orphan" {
				f.publish(t, b)
			} else {
				s.Sequence = b.Sequence
			}
			switch change {
			case "anchor":
				s.ClaudeProgress.Observation.NativeEventID = string(domain.NewID())
			case "boundary":
				s.ClaudeProgress.Observation.CompactionSummary.BoundaryID = string(domain.NewID())
			case "reference":
				s.ClaudeProgress.Observation.CompactionSummary.BoundaryMessageID = domain.NewID()
			case "mixed":
				s.ClaudeProgress.Observation.Status = &domain.ClaudeStatusObservation{}
			case "overlap":
				s.ClaudeProgress.Observation = b.ClaudeProgress.Observation
				s.ClaudeProgress.Observation.NativeEventID = string(domain.NewID())
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, s)); err == nil {
				t.Fatal("foreign context accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision {
				t.Fatal("context rejection partially committed")
			}
		})
	}
}
