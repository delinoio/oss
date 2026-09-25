package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func reportedCounts(total int64) domain.NativeTokenCounts {
	input, cached, output, reasoning := int64(11), int64(4), int64(7), int64(3)
	return domain.NativeTokenCounts{Input: &input, Cached: &cached, Output: &output, Reasoning: &reasoning, Total: &total}
}

func TestExecutionUsageObservationsRemainImmutableWithoutCounterSummation(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	first, second := domain.NewID(), domain.NewID()
	e := f.event(domain.ExecutionUsageObserved, 3)
	e.ObservationID, e.Usage = first, &domain.NativeTokenUsage{Total: reportedCounts(30), Last: reportedCounts(20)}
	receipt := f.publish(t, e)
	if r, err := f.call(receipt); err != nil || !r.Msg.Replayed || r.Msg.AcknowledgedSequence != 3 {
		t.Fatalf("usage lost-response retry changed: %v", err)
	}
	e = f.event(domain.ExecutionUsageObserved, 4)
	e.ObservationID, e.Usage = second, &domain.NativeTokenUsage{Total: reportedCounts(2), Last: reportedCounts(1)}
	f.publish(t, e)
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("replayed usage was repeated or counter reset overwritten: %v", err)
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionUsageObservation](row)
		if err != nil || value.AccountID != f.input.AccountID || value.ConnectionID != f.input.ConnectionID || value.ExecutionID != f.input.ExecutionID || value.ProviderID != f.input.Configuration.ProviderID || value.ModelID != f.input.Configuration.ModelID || value.Harness != domain.Codex || value.Version != domain.CodexProtocolVersion || value.ThreadID != string(f.thread) || value.TurnID != string(f.turn) || value.Usage.Total.CacheWrite != nil || value.Usage.ContextWindow != nil {
			t.Fatal("usage attribution or unavailability was fabricated")
		}
		expected := int64(30)
		if row.ID == second {
			expected = 2
		}
		if *value.Usage.Total.Total != expected {
			t.Fatal("cumulative observations were charged, merged or rewritten")
		}
	}
	// A second identity may retain an identical native observation, but it is
	// still not a charge. Reusing an existing observation ID may never rewrite
	// historical attribution or consume a new event sequence.
	e = f.event(domain.ExecutionUsageObserved, 5)
	e.ObservationID, e.Usage = first, &domain.NativeTokenUsage{Total: reportedCounts(900), Last: reportedCounts(900)}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("usage observation was overwritten")
	}
	e = f.event(domain.ExecutionNoticeObserved, 5)
	e.Notice = domain.NativeWarning
	notice := f.publish(t, e)
	if r, err := f.call(notice); err != nil || !r.Msg.Replayed {
		t.Fatalf("notice replay failed: %v", err)
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Execution.LatestUsageID != second || s.Execution.NoticeCount != 1 || s.Execution.LastNotice != domain.NativeWarning || s.Outcome != domain.ExecutionRunning || s.PendingInputs != 0 {
		t.Fatal("observations changed execution authority or repeated counters")
	}
}

func TestExecutionObservationValidationDoesNotConsumeSequence(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	for _, bad := range []string{"missing-counter", "negative", "mixed-notice", "wrong-turn", "unsupported-notice", "message-with-usage"} {
		e := f.event(domain.ExecutionUsageObserved, 3)
		e.ObservationID, e.Usage = domain.NewID(), &domain.NativeTokenUsage{Total: reportedCounts(30), Last: reportedCounts(20)}
		switch bad {
		case "missing-counter":
			e.Usage.Total.Input = nil
		case "negative":
			*e.Usage.Last.Total = -1
		case "mixed-notice":
			e.Notice = domain.NativeWarning
		case "wrong-turn":
			e.NativeTurnID = string(domain.NewID())
		case "unsupported-notice":
			e.Kind, e.ObservationID, e.Usage, e.Notice = domain.ExecutionNoticeObserved, "", nil, "raw-diagnostic"
		case "message-with-usage":
			e.Message = &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "foreign", Role: domain.AssistantMessage}
		}
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatalf("accepted malformed observation: %s", bad)
		}
	}
	e := f.event(domain.ExecutionTurnFinished, 3)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
	e = f.event(domain.ExecutionUsageObserved, 4)
	e.ObservationID, e.Usage = domain.NewID(), &domain.NativeTokenUsage{Total: reportedCounts(30), Last: reportedCounts(20)}
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("post-terminal usage revived closed execution publication")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatal("rejected observations left usage records")
	}
}
