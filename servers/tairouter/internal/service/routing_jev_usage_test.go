// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"testing"

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
