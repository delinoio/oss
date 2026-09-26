package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func originalOpenCodeUsage() domain.OpenCodeUsageObservation {
	return domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "12", Output: "7", Reasoning: "3", CacheRead: "8", CacheWrite: "2"}, NativeEstimate: "0.001e+0"}
}

func TestOpenCodeUsagePublicationRetainsOverlapAndCannotEnterBillingLedger(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	value := originalOpenCodeUsage()
	e := f.event(domain.ExecutionOpenCodeUsageObserved, 3)
	e.ObservationID = domain.NewID()
	e.OpenCodeUsage = &value
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("usage replay was not the original receipt")
	}
	first := e.ObservationID
	e.Sequence = 4
	e.ObservationID = domain.NewID()
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("one native source acquired a second observation identity")
	}
	value.Counts.Input = "13"
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("native source was replaced with changed counters")
	}
	value.Counts.Input = "12"
	value.Source = domain.OpenCodeMessageUsage
	value.NativeID = value.NativeParentID
	f.publish(t, e)
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("overlapping step/message observations were merged or duplicated")
	}
	for _, row := range rows {
		record, err := store.Decode[domain.OpenCodeUsageRecord](row)
		if err != nil || record.ExecutionID != f.input.ExecutionID || record.AccountID != f.input.AccountID || record.ConnectionID != f.input.ConnectionID || record.ProviderID != f.input.Configuration.ProviderID || record.ModelID != f.input.Configuration.ModelID || record.Harness != domain.OpenCode || record.Version != domain.OpenCodeProtocolVersion || record.Usage.Validate() != nil || record.Usage.Counts.Total != nil {
			t.Fatal("native observation lost original attribution or invented total")
		}
		if _, err := f.service.Store.ResponseUsage(context.Background(), row.ID); err == nil {
			t.Fatal("overlapping native usage entered exact response accounting")
		}
	}
	r, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Execution.LatestUsageID != e.ObservationID || s.Execution.LatestResponseUsageID != "" || s.Execution.LastSequence != 4 || s.Outcome != domain.ExecutionRunning || first == e.ObservationID {
		t.Fatal("usage publication changed independent execution or billing state")
	}
}

func TestOpenCodeUsageRejectsForeignProfilesAndCannotConsumeSequence(t *testing.T) {
	for _, harness := range []domain.Harness{domain.Codex, domain.OpenCode} {
		t.Run(string(harness), func(t *testing.T) {
			f := newPublicationFixture(t)
			if harness == domain.OpenCode {
				f = newOpenCodePublicationFixture(t, domain.ExecuteMode)
			}
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			for _, bad := range []string{"profile", "parent", "counter", "estimate", "mixed"} {
				value := originalOpenCodeUsage()
				e := f.event(domain.ExecutionOpenCodeUsageObserved, 3)
				e.ObservationID = domain.NewID()
				e.OpenCodeUsage = &value
				switch bad {
				case "profile":
					if harness == domain.OpenCode {
						e.Kind = domain.ExecutionUsageObserved
						e.Usage = &domain.NativeTokenUsage{Total: reportedCounts(1), Last: reportedCounts(1)}
					}
				case "parent":
					value.NativeParentID = e.NativeTurnID
				case "counter":
					value.Counts.Input = "1.5"
				case "estimate":
					value.NativeEstimate = "USD 1"
				case "mixed":
					e.Usage = &domain.NativeTokenUsage{Total: reportedCounts(1), Last: reportedCounts(1)}
				}
				if _, err := f.call(f.requestEvent(t, e)); err == nil {
					t.Fatal("foreign or malformed usage was accepted", bad)
				}
			}
			r, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			s, err := store.Decode[domain.Session](r)
			if err != nil || s.Execution.LastSequence != 2 || s.Execution.LatestUsageID != "" {
				t.Fatal("rejected usage changed original sequence")
			}
		})
	}
}
