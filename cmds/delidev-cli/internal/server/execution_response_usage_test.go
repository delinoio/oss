package server

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestResponseUsagePublicationDeduplicatesAndPinsAttribution(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	first, second := domain.NewID(), domain.NewID()
	counts := reportedCounts(20)
	e := f.event(domain.ExecutionResponseUsageObserved, 3)
	e.ObservationID, e.ResponseUsage = first, &domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing}
	receipt := f.publish(t, e)
	if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
		t.Fatal("lost-response retry failed", err)
	}
	e.Sequence, e.ObservationID = 4, second
	f.publish(t, e)
	record, err := f.service.Store.ResponseUsage(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	v := record.Record
	if v.SessionID != f.input.SessionID || v.ExecutionID != f.input.ExecutionID || v.AccountID != f.input.AccountID || v.ConnectionID != f.input.ConnectionID || v.ProviderID != f.input.Configuration.ProviderID || v.ModelID != f.input.Configuration.ModelID || v.ThreadID != string(f.thread) || v.TurnID != string(f.turn) || v.Sequence != 3 || *v.Usage.Counts.Total != 20 {
		t.Fatal("original attribution changed")
	}
	if _, err := f.service.Store.ResponseUsage(context.Background(), second); err == nil {
		t.Fatal("duplicate response charged again")
	}
	e.Sequence, e.ObservationID = 5, domain.NewID()
	changed := reportedCounts(40)
	e.ResponseUsage.Counts = &changed
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("conflicting identity replaced evidence")
	}
	e.ResponseUsage.Counts = nil
	e.ResponseUsage.ResponseDigest = strings.Repeat("b", 64)
	f.publish(t, e)
	missing, err := f.service.Store.ResponseUsage(context.Background(), e.ObservationID)
	if err != nil || missing.Record.Usage.Counts != nil {
		t.Fatal("missing became zero", err)
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](r)
	if err != nil || session.Execution.LatestResponseUsageID != e.ObservationID || session.Execution.LatestUsageID != "" || session.Outcome != domain.ExecutionRunning {
		t.Fatal("usage changed authority or cumulative observations", err)
	}
	terminal := f.event(domain.ExecutionTurnFinished, 6)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	e.Sequence, e.ObservationID = 7, domain.NewID()
	e.ResponseUsage.ResponseDigest = strings.Repeat("c", 64)
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("terminal usage revived publication")
	}
}

func TestMalformedResponseUsageCannotConsumeSequence(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	for _, bad := range []string{"missing", "digest", "case", "counts", "cost", "turn", "mixed"} {
		e := f.event(domain.ExecutionResponseUsageObserved, 3)
		e.ObservationID, e.ResponseUsage = domain.NewID(), &domain.NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: domain.UsageCostMissing}
		switch bad {
		case "missing":
			e.ResponseUsage = nil
		case "digest":
			e.ResponseUsage.ResponseDigest = "raw-response"
		case "case":
			e.ResponseUsage.ResponseDigest = strings.Repeat("A", 64)
		case "counts":
			e.ResponseUsage.Counts = &domain.NativeTokenCounts{}
		case "cost":
			e.ResponseUsage.CostEvidence = "USD"
		case "turn":
			e.NativeTurnID = string(domain.NewID())
		case "mixed":
			e.Usage = &domain.NativeTokenUsage{Total: reportedCounts(20), Last: reportedCounts(20)}
		}
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("accepted malformed response", bad)
		}
	}
	e := f.event(domain.ExecutionNoticeObserved, 3)
	e.Notice = domain.NativeWarning
	f.publish(t, e)
}
