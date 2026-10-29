// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/pricing"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestJEVDecisionUsageKeepsParentReceipt(t *testing.T) {
	a, sc, key := dispatchFixture(t)
	ctx := context.Background()
	entries := map[string]*v1.PriceOverride{}
	for _, entry := range []struct{ model, rate string }{{"decision-model", "1"}, {"target-model", "4"}} {
		entries[entry.model] = &v1.PriceOverride{Id: entry.model, ProviderId: "custom", ModelId: entry.model, Rates: &v1.PriceRates{Currency: "USD", Input: ptr(entry.rate), Output: ptr(entry.rate)}}
	}
	p := admitRouting(t, a, key)
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := priceOverrides(raw)
	if err != nil {
		t.Fatal(err)
	}
	a.prices, err = pricing.NewSnapshot(overrides)
	if err != nil {
		t.Fatal(err)
	}
	decision := inference.UsageRecord{AccountingRole: inference.UsageAccountingNestedOperation, OperationID: "decision-1", RequestID: "decision-request", Provider: "custom", Model: "decision-model", Outcome: inference.OutcomeSuccess, Usage: inference.Usage{InputTokens: ptr(int64(2)), OutputTokens: ptr(int64(1))}}
	final := inference.UsageRecord{RequestID: "parent-request", Provider: "custom", Model: "target-model", Outcome: inference.OutcomeSuccess, Usage: inference.Usage{InputTokens: ptr(int64(20)), OutputTokens: ptr(int64(10))}}
	for _, record := range []inference.UsageRecord{decision, decision, final, final} {
		if err := a.Record(ctx, p, record); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := a.s.QueryUsage(ctx, sc, store.UsageFilter{UserID: sc.UserID})
	if err != nil || len(rows) != 2 {
		t.Fatalf("usage rows: %v, %v", rows, err)
	}
	byModel := map[string]store.Usage{}
	for _, row := range rows {
		byModel[row.Model] = row
	}
	d, f := byModel["decision-model"], byModel["target-model"]
	if d.InputTokens == nil || *d.InputTokens != 2 || f.InputTokens == nil || *f.InputTokens != 20 || d.Cost == nil || f.Cost == nil || *d.Cost == *f.Cost || !d.PriceKnown || !f.PriceKnown {
		t.Fatalf("separate priced rows: %+v %+v", d, f)
	}
	aggregate, err := a.s.QueryAggregate(ctx, sc, store.UsageFilter{UserID: sc.UserID})
	if err != nil || aggregate.Requests != 2 || aggregate.InputTokens != 22 || aggregate.OutputTokens != 11 || aggregate.MissingCost != 0 {
		t.Fatalf("aggregate: %+v, %v", aggregate, err)
	}
	wrong := p
	wrong.UserID = "other"
	if err := a.Record(ctx, wrong, decision); err == nil {
		t.Fatal("cross-user operation receipt accepted")
	}
	wrong = p
	wrong.MembershipGeneration++
	if err := a.Record(ctx, wrong, decision); err == nil {
		t.Fatal("cross-generation operation receipt accepted")
	}
}

func TestJEVDecisionUsagePreservesNativeCompletion(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "success"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
			p := admitRouting(t, a, key)
			ctx := context.Background()
			lease, err := a.current(p)
			if err != nil {
				t.Fatal(err)
			}
			version := mustNativeVersion(t, a, p, "a")
			hk := nativeHealthKey(store.SelectionCandidate{ID: "a", Version: version}, store.NativeHealthTransient, "")
			if err := a.s.UpdateNativeHealth(ctx, lease, hk, func(h store.NativeHealth) (store.NativeHealth, error) {
				h.Failures = 1
				h.Payload, err = json.Marshal(nativeHealthState{Failures: 1, LastFailure: time.Now().UnixMilli()})
				return h, err
			}); err != nil {
				t.Fatal(err)
			}
			permit, err := a.s.AcquireNativeHealthPermit(ctx, lease, hk, false)
			if err != nil {
				t.Fatal(err)
			}
			receipt := &nativeHealthReceipt{Account: "a", Version: version, Policy: defaultNativeHealthPolicy(), Permits: []store.NativeHealthPermit{permit}}
			if err := a.s.WithNativeHealthDispatch(ctx, lease, receipt.Permits, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			pin, err := a.s.GetNativeAffinity(ctx, lease, "native/openai/decision", "config", "session")
			if err != nil {
				t.Fatal(err)
			}
			a.native = map[string]nativeSelectionState{"decision": {Group: "native/openai/decision", Version: "config", Session: "session", Epoch: pin.Epoch, TargetProvider: "openai", Model: "decision-model", Candidates: map[string]string{"a": version}, Health: receipt}}
			a.versions = map[string]string{"a": version}
			// A nested decision must not consume the parent JEV completion state.
			parent := &jevRequestPin{}
			a.jev = parent
			record := inference.UsageRecord{AccountingRole: inference.UsageAccountingNestedOperation, OperationID: "native-decision", Provider: "openai", AccountID: "a", Model: "decision-model", Outcome: inference.OutcomeSuccess, Canceled: canceled, Usage: inference.Usage{InputTokens: ptr(int64(2)), OutputTokens: ptr(int64(1))}}
			for range 2 {
				if err := a.Record(ctx, p, record); err != nil {
					t.Fatal(err)
				}
			}
			if a.jev != parent {
				t.Fatal("nested decision finalized parent JEV state")
			}
			h, err := a.s.GetNativeHealth(ctx, lease, hk)
			if err != nil {
				t.Fatal(err)
			}
			health, err := decodeNativeHealth(h)
			if err != nil {
				t.Fatal(err)
			}
			pin, err = a.s.GetNativeAffinity(ctx, lease, "native/openai/decision", "config", "session")
			if err != nil {
				t.Fatal(err)
			}
			if !receipt.Finished {
				t.Fatal("native decision health receipt was not settled")
			}
			if canceled {
				if health.Failures != 1 || len(health.Samples) != 0 || pin.AccountID != "" {
					t.Fatalf("canceled decision changed health or affinity: %+v %+v", health, pin)
				}
			} else if health.Failures != 0 || len(health.Samples) != 1 || pin.AccountID != "a" {
				t.Fatalf("decision completion lost or replayed: %+v %+v", health, pin)
			}
			a.jev = nil
			if err := a.Record(ctx, p, inference.UsageRecord{Provider: "custom", Model: "other-target", Outcome: inference.OutcomeSuccess, Usage: inference.Usage{InputTokens: ptr(int64(5)), OutputTokens: ptr(int64(3))}}); err != nil {
				t.Fatal(err)
			}
			rows, _, err := a.s.QueryUsage(ctx, sc, store.UsageFilter{UserID: sc.UserID})
			if err != nil || len(rows) != 2 {
				t.Fatalf("independent decision and parent usage: %d %v", len(rows), err)
			}
		})
	}
}
