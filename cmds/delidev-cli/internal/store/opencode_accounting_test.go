// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func openCodeAccountingRecord(r domain.ResponseUsageRecord) domain.OpenCodeUsageRecord {
	return domain.OpenCodeUsageRecord{ExecutionID: r.ExecutionID, AccountID: r.AccountID, ConnectionID: r.ConnectionID, ProviderID: r.ProviderID, SubscriptionService: r.SubscriptionService, ModelID: r.ModelID, Harness: domain.OpenCode, Version: domain.OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "12", CacheRead: "7", CacheWrite: "3", Output: "8", Reasoning: "2"}, NativeEstimate: "999"}}
}

func retainOpenCodeAccounting(s *Store, request, source, input domain.ID, r domain.ResponseUsageRecord, o domain.OpenCodeUsageRecord, rollback bool) (Result, error) {
	return s.Mutate(context.Background(), request, "fixture.opencode-accounting", o, func(tx *Tx) (any, error) {
		if err := tx.PutOpenCodeUsage(source, r.SessionID, r.ProjectID, o); err != nil {
			return nil, err
		}
		if err := tx.PutOpenCodeAccounting(source, input, r.SessionID, r.ProjectID, o); err != nil {
			return nil, err
		}
		if rollback {
			return nil, domain.Fail(domain.Conflict, "Fixture rollback.", "Discard the observation and derived accounting.")
		}
		return nil, nil
	})
}

func TestOpenCodeAccountingAPIAndGoAtomicReplayHistory(t *testing.T) {
	for _, service := range []domain.SubscriptionService{"", domain.SubscriptionOpenCodeGo} {
		t.Run(string(service), func(t *testing.T) {
			s, root := openTest(t)
			r, _, input := nativeAccountingFixture(t, s)
			if service != "" {
				r.ProviderID, r.SubscriptionService = "", service
				r.ModelID = (domain.ModelIdentity{SubscriptionService: service, NativeID: "fixture"}).Key()
			}
			o := openCodeAccountingRecord(r)
			one, two := "1", "2"
			basis := pricingFixture()
			basis.InputPerMillion, basis.OutputPerMillion = &one, &two
			var price PricingVersion
			if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.opencode-price", nil, func(tx *Tx) (any, error) {
				var err error
				price, err = tx.PutPricing(r.ModelID, 0, domain.NewID(), basis)
				return nil, err
			}); err != nil {
				t.Fatal(err)
			}
			request, source := domain.NewID(), domain.NewID()
			if _, err := retainOpenCodeAccounting(s, request, source, input, r, o, true); err == nil {
				t.Fatal("expected rollback")
			}
			if _, err := s.Get(context.Background(), domain.UsageKind, source); err == nil {
				t.Fatal("rollback retained original observation")
			}
			var n int
			if err := s.db.QueryRow("SELECT count(*) FROM native_accounting").Scan(&n); err != nil || n != 0 {
				t.Fatal("rollback retained derived unit", n, err)
			}
			if _, err := retainOpenCodeAccounting(s, request, source, input, r, o, false); err != nil {
				t.Fatal("valid original step rejected", err)
			}
			basis.Currency = "EUR"
			if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.opencode-reprice", nil, func(tx *Tx) (any, error) {
				return tx.PutPricing(r.ModelID, price.Revision, domain.NewID(), basis)
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			s, err = Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if result, err := retainOpenCodeAccounting(s, request, source, input, r, o, false); err != nil || !result.Replayed {
				t.Fatal("restart lost exact receipt", result, err)
			}
			changed := o
			changed.Usage.Counts.Input = "13"
			if _, err := retainOpenCodeAccounting(s, request, source, input, r, changed, false); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("receipt accepted conflicting counts", err)
			}
			if _, err := retainOpenCodeAccounting(s, domain.NewID(), domain.NewID(), input, r, changed, false); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("native source accepted conflicting unit", err)
			}
			selection := nativeSelection()
			selection.SubscriptionService = service
			v, err := readUsage(s, selection)
			if err != nil || len(v.NativeAccounting) != 2 {
				t.Fatal("accounting read failed", err)
			}
			a := v.NativeAccounting[1]
			if a.Totals.Units != 1 || a.Totals.Input.KnownTotal != "22" || a.Totals.Output.KnownTotal != "10" || len(a.Groups) != 1 || len(a.Models) != 1 || len(a.Pricing) != 1 || a.Pricing[0].Pricing.ID != price.ID || a.Pricing[0].Pricing.SubscriptionService != service || a.Pricing[0].Totals.Currencies[0].Currency != "USD" || a.Totals.Currencies[0].KnownAmount != "0.000042" {
				t.Fatal("historical source, price or counts changed", a)
			}
			g, m := a.Groups[0], a.Models[0]
			if g.AccountID != r.AccountID || g.ProviderID != r.ProviderID || g.ModelID != r.ModelID || g.SubscriptionService != service || m.ProviderID != r.ProviderID || m.SubscriptionService != service || m.ModelID != r.ModelID {
				t.Fatal("group lost original source identity", g, m)
			}
			if service != "" {
				if _, err := s.db.Exec("UPDATE pricing_versions SET subscription_service=? WHERE id=?", domain.SubscriptionClaude, price.ID); err != nil {
					t.Fatal(err)
				}
				v, err = readUsage(s, selection)
				if domain.SafeError(err).Code != domain.RecoveryRequired || v.NativeAccounting != nil {
					t.Fatal("cross-service historical price returned a partial result", err)
				}
			}
		})
	}
}

func TestOpenCodeAccountingRejectsInvalidSourceAtomically(t *testing.T) {
	for _, service := range []domain.SubscriptionService{"", domain.SubscriptionOpenCodeGo, domain.SubscriptionClaude, "foreign"} {
		for _, mixed := range []bool{false, true} {
			s, _ := openTest(t)
			r, _, input := nativeAccountingFixture(t, s)
			o := openCodeAccountingRecord(r)
			o.ProviderID, o.SubscriptionService = "", service
			if mixed {
				o.ProviderID = r.ProviderID
			}
			if service == domain.SubscriptionOpenCodeGo && !mixed || service == "" && mixed {
				continue
			}
			o.ModelID = (domain.ModelIdentity{ProviderID: o.ProviderID, SubscriptionService: service, NativeID: "fixture"}).Key()
			source := domain.NewID()
			if _, err := retainOpenCodeAccounting(s, domain.NewID(), source, input, r, o, false); err == nil {
				t.Fatal("invalid source accepted", service, mixed)
			}
			if _, err := s.Get(context.Background(), domain.UsageKind, source); err == nil {
				t.Fatal("invalid source left an observation", service, mixed)
			}
			var n int
			if err := s.db.QueryRow("SELECT count(*) FROM native_accounting").Scan(&n); err != nil || n != 0 {
				t.Fatal("invalid source left accounting", n, err)
			}
		}
	}
}

func TestOpenCodeAccountingServiceFilterKeepsOnlyOriginalGroupsAndPrices(t *testing.T) {
	s, _ := openTest(t)
	r, _, input := nativeAccountingFixture(t, s)
	for _, service := range []domain.SubscriptionService{"", domain.SubscriptionOpenCodeGo} {
		value := r
		if service != "" {
			value.ProviderID, value.SubscriptionService = "", service
			value.ModelID = (domain.ModelIdentity{SubscriptionService: service, NativeID: "fixture"}).Key()
			value.ExecutionID = domain.NewID()
		}
		if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.filtered-price", nil, func(tx *Tx) (any, error) {
			return tx.PutPricing(value.ModelID, 0, domain.NewID(), pricingFixture())
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := retainOpenCodeAccounting(s, domain.NewID(), domain.NewID(), input, value, openCodeAccountingRecord(value), false); err != nil {
			t.Fatal(err)
		}
	}
	for _, service := range []domain.SubscriptionService{"", domain.SubscriptionOpenCodeGo, domain.SubscriptionClaude} {
		selection := nativeSelection()
		selection.SubscriptionService = service
		v, err := readUsage(s, selection)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if service == "" {
			want = 2
		} else if service == domain.SubscriptionOpenCodeGo {
			want = 1
		}
		a := v.NativeAccounting[1]
		if int(a.Totals.Units) != want || len(a.Groups) != want || len(a.Models) != want || len(a.Pricing) != want {
			t.Fatal("service filter leaked groups or price versions", service, a)
		}
		for _, p := range a.Pricing {
			if service != "" && p.Pricing.SubscriptionService != service {
				t.Fatal("foreign service price leaked", p)
			}
		}
	}
}

func TestOpenCodeAccountingGoWithoutPricingRemainsUnpriced(t *testing.T) {
	s, _ := openTest(t)
	r, _, input := nativeAccountingFixture(t, s)
	r.ProviderID, r.SubscriptionService = "", domain.SubscriptionOpenCodeGo
	r.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionOpenCodeGo, NativeID: "fixture"}).Key()
	o := openCodeAccountingRecord(r)
	if _, err := retainOpenCodeAccounting(s, domain.NewID(), domain.NewID(), input, r, o, false); err != nil {
		t.Fatal("normal Go usage required an API provider or rate card", err)
	}
	selection := nativeSelection()
	selection.SubscriptionService = domain.SubscriptionOpenCodeGo
	v, err := readUsage(s, selection)
	if err != nil || len(v.NativeAccounting) != 2 {
		t.Fatal("unpriced Go accounting read failed", err)
	}
	a := v.NativeAccounting[1]
	if a.Totals.Units != 1 || a.Totals.UnpricedUnits != 1 || len(a.Pricing) != 0 || len(a.Totals.Currencies) != 0 || a.Totals.Input.KnownTotal != "22" || a.Groups[0].ProviderID != "" || a.Groups[0].SubscriptionService != domain.SubscriptionOpenCodeGo {
		t.Fatal("unpriced Go accounting invented provider or price evidence", a)
	}
}

// goAccountingSource rewrites a response fixture into the closed OpenCode Go
// source identity: service-bound, no API Provider.
func goAccountingSource(r domain.ResponseUsageRecord) domain.ResponseUsageRecord {
	r.ProviderID, r.SubscriptionService = "", domain.SubscriptionOpenCodeGo
	r.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionOpenCodeGo, NativeID: "fixture"}).Key()
	r.ExecutionID = domain.NewID()
	return r
}

// cloneOpenCodeAccounting bulk-copies one valid retained OpenCode unit (source
// observation and derived accounting row) so bound tests do not pay for the
// per-publication native-source scan. Each copy gets its own source identity and
// optionally its own historical price; the immutable body and estimate stay valid.
func cloneOpenCodeAccounting(t *testing.T, s *Store, template domain.ID, count int, price func(index int) domain.ID) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	entity, err := tx.Prepare(`INSERT INTO entities(id,kind,revision,session_id,project_id,body,created_at,updated_at) SELECT ?,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE id=?`)
	if err != nil {
		t.Fatal(err)
	}
	defer entity.Close()
	unit, err := tx.Prepare(`INSERT INTO native_accounting(id,source_id,session_id,project_id,execution_id,input_id,kind,account_id,provider_id,model_key,body,pricing_id,estimate,created_at) SELECT ?,?,session_id,project_id,execution_id,input_id,kind,account_id,provider_id,model_key,json_set(body,'$.source_id',?),COALESCE(?,pricing_id),CASE WHEN ? IS NULL THEN estimate ELSE json_set(estimate,'$.pricing_id',?) END,created_at FROM native_accounting WHERE source_id=?`)
	if err != nil {
		t.Fatal(err)
	}
	defer unit.Close()
	for i := 0; i < count; i++ {
		id := domain.NewID()
		var priceID any
		if price != nil {
			priceID = price(i)
		}
		if _, err := entity.Exec(id, template); err != nil {
			t.Fatal(err)
		}
		if _, err := unit.Exec(id, id, id, priceID, priceID, priceID, template); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func goOnlySelection(daily bool) domain.UsageSelection {
	selection := nativeSelection()
	selection.SubscriptionService = domain.SubscriptionOpenCodeGo
	if !daily {
		selection.Granularity, selection.TimeZone = domain.UsageTimeGranularityUnspecified, ""
	}
	return selection
}

func requireOnlyGoUnit(t *testing.T, v domain.UsageSummary, goModel domain.ID, prices int) {
	t.Helper()
	if len(v.NativeAccounting) != 2 {
		t.Fatal("native families missing", v.NativeAccounting)
	}
	a := v.NativeAccounting[1]
	if a.Totals.Units != 1 || len(a.Groups) != 1 || len(a.Models) != 1 || len(a.Pricing) != prices || a.Groups[0].SubscriptionService != domain.SubscriptionOpenCodeGo || a.Groups[0].ProviderID != "" || a.Groups[0].ModelID != goModel || a.Models[0].ModelID != goModel {
		t.Fatal("Go selection leaked excluded groups, models or prices", a)
	}
	for _, p := range a.Pricing {
		if p.Pricing.SubscriptionService != domain.SubscriptionOpenCodeGo {
			t.Fatal("excluded historical price leaked", p)
		}
	}
}

func TestOpenCodeAccountingGoSelectionIgnoresExcludedRowsOverReadBound(t *testing.T) {
	s, _ := openTest(t)
	r, _, input := nativeAccountingFixture(t, s)
	api := openCodeAccountingRecord(r)
	apiSource := domain.NewID()
	if _, err := retainOpenCodeAccounting(s, domain.NewID(), apiSource, input, r, api, false); err != nil {
		t.Fatal(err)
	}
	// An excluded-only history over the row bound is the normal empty Go summary.
	cloneOpenCodeAccounting(t, s, apiSource, maxUsageResponses, nil)
	for _, daily := range []bool{true, false} {
		v, err := readUsage(s, goOnlySelection(daily))
		if err != nil || len(v.NativeAccounting) != 2 || v.NativeAccounting[1].Totals.Units != 0 || len(v.NativeAccounting[1].Groups) != 0 || len(v.NativeAccounting[1].Models) != 0 || len(v.NativeAccounting[1].Pricing) != 0 {
			t.Fatal("excluded-only history over the row bound was not an empty Go summary", daily, err)
		}
	}
	goRecord := goAccountingSource(r)
	goSource := domain.NewID()
	if _, err := retainOpenCodeAccounting(s, domain.NewID(), goSource, input, goRecord, openCodeAccountingRecord(goRecord), false); err != nil {
		t.Fatal(err)
	}
	for _, daily := range []bool{true, false} {
		v, err := readUsage(s, goOnlySelection(daily))
		if err != nil {
			t.Fatal("25001 valid excluded API rows consumed the Go read bound", daily, err)
		}
		requireOnlyGoUnit(t, v, goRecord.ModelID, 0)
		if !daily && len(v.NativeAccounting[1].Days) != 0 {
			t.Fatal("unspecified granularity gained daily buckets")
		}
	}
	// Account and model selectors compose with the service predicate.
	selector := goOnlySelection(true)
	selector.AccountID, selector.ModelID = goRecord.AccountID, goRecord.ModelID
	if v, err := readUsage(s, selector); err != nil {
		t.Fatal(err)
	} else {
		requireOnlyGoUnit(t, v, goRecord.ModelID, 0)
	}
	// The unfiltered selection still exceeds the bound with no partial aggregate.
	for _, daily := range []bool{true, false} {
		unfiltered := nativeSelection()
		if !daily {
			unfiltered.Granularity, unfiltered.TimeZone = domain.UsageTimeGranularityUnspecified, ""
		}
		v, err := readUsage(s, unfiltered)
		if domain.SafeError(err).Code != domain.ResourceExhausted || !reflect.DeepEqual(v, domain.UsageSummary{}) {
			t.Fatal("unfiltered oversized selection returned a partial summary", daily, err)
		}
	}
	// An oversized matching Go selection also fails closed.
	cloneOpenCodeAccounting(t, s, goSource, maxUsageResponses, nil)
	v, err := readUsage(s, goOnlySelection(true))
	if domain.SafeError(err).Code != domain.ResourceExhausted || !reflect.DeepEqual(v, domain.UsageSummary{}) {
		t.Fatal("oversized matching Go selection returned a partial summary", err)
	}
}

func TestOpenCodeAccountingGoSelectionIgnoresExcludedHistoricalPricesOverBound(t *testing.T) {
	s, _ := openTest(t)
	r, _, input := nativeAccountingFixture(t, s)
	price := preparePrice(t, s, r)
	prices := []domain.ID{price.ID}
	if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.excluded-prices", nil, func(tx *Tx) (any, error) {
		for len(prices) < maxUsageGroups+1 {
			var err error
			price, err = tx.PutPricing(r.ModelID, price.Revision, domain.NewID(), pricingFixture())
			if err != nil {
				return nil, err
			}
			prices = append(prices, price.ID)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	// The template is retained while the first price is active; copies rebind to
	// their own immutable price version with the same basis and estimate.
	apiSource := domain.NewID()
	if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.api-template", nil, func(tx *Tx) (any, error) {
		o := openCodeAccountingRecord(r)
		if err := tx.PutOpenCodeUsage(apiSource, r.SessionID, r.ProjectID, o); err != nil {
			return nil, err
		}
		return nil, tx.PutOpenCodeAccounting(apiSource, input, r.SessionID, r.ProjectID, o)
	}); err != nil {
		t.Fatal(err)
	}
	cloneOpenCodeAccounting(t, s, apiSource, len(prices), func(i int) domain.ID { return prices[i] })
	goRecord := goAccountingSource(r)
	var goPrice PricingVersion
	if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.go-price", nil, func(tx *Tx) (any, error) {
		var err error
		goPrice, err = tx.PutPricing(goRecord.ModelID, 0, domain.NewID(), pricingFixture())
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := retainOpenCodeAccounting(s, domain.NewID(), domain.NewID(), input, goRecord, openCodeAccountingRecord(goRecord), false); err != nil {
		t.Fatal(err)
	}
	for _, daily := range []bool{true, false} {
		v, err := readUsage(s, goOnlySelection(daily))
		if err != nil {
			t.Fatal("more than 500 excluded historical prices consumed the Go price bound", daily, err)
		}
		requireOnlyGoUnit(t, v, goRecord.ModelID, 1)
		if v.NativeAccounting[1].Pricing[0].Pricing.ID != goPrice.ID {
			t.Fatal("Go selection returned a foreign price", v.NativeAccounting[1].Pricing)
		}
	}
	unfiltered := nativeSelection()
	v, err := readUsage(s, unfiltered)
	if domain.SafeError(err).Code != domain.ResourceExhausted || !reflect.DeepEqual(v, domain.UsageSummary{}) {
		t.Fatal("unfiltered selection with excess distinct prices returned a partial summary", err)
	}
}

func TestOpenCodeAccountingGoSelectionStillFailsClosedOnSelectedCorruption(t *testing.T) {
	for _, column := range []string{"estimate", "body", "relational"} {
		t.Run(column, func(t *testing.T) {
			s, _ := openTest(t)
			r, _, input := nativeAccountingFixture(t, s)
			goRecord := goAccountingSource(r)
			source := domain.NewID()
			if _, err := retainOpenCodeAccounting(s, domain.NewID(), source, input, goRecord, openCodeAccountingRecord(goRecord), false); err != nil {
				t.Fatal(err)
			}
			switch column {
			case "estimate":
				_, err := s.db.Exec(`UPDATE native_accounting SET estimate=json_set(estimate,'$.input.known_amount','1') WHERE source_id=?`, source)
				if err != nil {
					t.Fatal(err)
				}
			case "body":
				_, err := s.db.Exec(`UPDATE native_accounting SET body=json_set(body,'$.input_id',?) WHERE source_id=?`, domain.NewID(), source)
				if err != nil {
					t.Fatal(err)
				}
			default:
				// A relational column that disagrees with the immutable body.
				_, err := s.db.Exec(`UPDATE native_accounting SET account_id=? WHERE source_id=?`, domain.NewID(), source)
				if err != nil {
					t.Fatal(err)
				}
			}
			v, err := readUsage(s, goOnlySelection(true))
			if domain.SafeError(err).Code != domain.RecoveryRequired || !reflect.DeepEqual(v, domain.UsageSummary{}) {
				t.Fatal("selected corrupt record was not rejected", column, err)
			}
		})
	}
}
