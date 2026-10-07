package store

import (
	"context"
	"fmt"

	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nativeAccountingFixture(t *testing.T, s *Store) (domain.ResponseUsageRecord, domain.ClaudeUsageRecord, domain.ID) {
	t.Helper()
	r := responseRecord(seedSearch(t, s, "source", domain.Archived))
	a, b, c, zero := domain.ClaudeUsageCount("13"), domain.ClaudeUsageCount("7"), domain.ClaudeUsageCount("5"), domain.ClaudeUsageCount("0")
	o := domain.ClaudeUsageRecord{ExecutionID: r.ExecutionID, AccountID: r.AccountID, ConnectionID: r.ConnectionID, ProviderID: r.ProviderID, ModelID: r.ModelID, Harness: domain.ClaudeCode, Version: domain.ClaudeProtocolVersion, ThreadID: r.ThreadID, TurnID: r.TurnID, Sequence: 3, Usage: domain.ClaudeUsageObservation{Source: domain.ClaudeInputResultUsage, NativeEventID: string(domain.NewID()), Result: &domain.ClaudeResultUsage{MainLoop: &domain.ClaudeProviderUsage{Input: &a, CacheRead: &b, CacheWrite: &c, Output: &zero, OutputDetail: &domain.ClaudeOutputTokenDetails{Thinking: &zero}}}}}
	return r, o, domain.NewID()
}
func retainNative(s *Store, request, source, input domain.ID, r domain.ResponseUsageRecord, o domain.ClaudeUsageRecord, rollback bool) (Result, error) {
	return s.Mutate(context.Background(), request, "fixture.native-input", o, func(tx *Tx) (any, error) {
		if err := tx.PutClaudeUsage(source, r.SessionID, r.ProjectID, o); err != nil {
			return nil, err
		}
		if err := tx.PutClaudeAccounting(source, input, r.SessionID, r.ProjectID, o); err != nil {
			return nil, err
		}
		if rollback {
			return nil, domain.Fail(domain.Conflict, "Fixture rollback.", "Discard all derived effects.")
		}
		return nil, nil
	})
}
func nativeSelection() domain.UsageSelection {
	return domain.UsageSelection{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), AccountingProfile: domain.NativeUnitsV1Accounting, Granularity: domain.UsageTimeGranularityDay, TimeZone: "UTC"}
}

func TestNativeAccountingCombinedDistinctPricingBound(t *testing.T) {
	s, _ := openTest(t)
	r, claude, _ := nativeAccountingFixture(t, s)
	price := preparePrice(t, s, r)
	retain := func(tx *Tx, index int) error {
		source, input, execution := domain.NewID(), domain.NewID(), domain.NewID()
		switch index % 3 {
		case 0:
			value := r
			value.ExecutionID, value.Usage.ResponseDigest = execution, fmt.Sprintf("%064x", index+1)
			_, _, err := tx.PutResponseUsage(source, value)
			return err
		case 1:
			value := claude
			value.ExecutionID, value.Usage.NativeEventID = execution, string(domain.NewID())
			if err := tx.PutClaudeUsage(source, r.SessionID, r.ProjectID, value); err != nil {
				return err
			}
			return tx.PutClaudeAccounting(source, input, r.SessionID, r.ProjectID, value)
		default:
			value := domain.OpenCodeUsageRecord{ExecutionID: execution, AccountID: r.AccountID, ConnectionID: r.ConnectionID, ProviderID: r.ProviderID, ModelID: r.ModelID, Harness: domain.OpenCode, Version: domain.OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeEstimate: "0", NativeID: fmt.Sprintf("prt_%012xABCDEFGHIJKLMN", index+1), NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "1", Output: "0", Reasoning: "0", CacheRead: "0", CacheWrite: "0"}}}
			if err := tx.PutOpenCodeUsage(source, r.SessionID, r.ProjectID, value); err != nil {
				return err
			}
			return tx.PutOpenCodeAccounting(source, input, r.SessionID, r.ProjectID, value)
		}
	}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.combined-pricing", nil, func(tx *Tx) (any, error) {
		// One original price is shared by all three families. Keep independent
		// category projections while counting this immutable identity once.
		for i := 0; i < maxUsageGroups+2; i++ {
			if i >= 3 {
				var err error
				price, err = tx.PutPricing(r.ModelID, price.Revision, domain.NewID(), pricingFixture())
				if err != nil {
					return nil, err
				}
			}
			if err := retain(tx, i); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, daily := range []bool{false, true} {
		selection := nativeSelection()
		if !daily {
			selection.Granularity, selection.TimeZone = domain.UsageTimeGranularityUnspecified, ""
		}
		value, err := readUsage(s, selection)
		if err != nil || len(value.NativeAccounting) != 2 {
			t.Fatal("500 distinct price versions were rejected", daily, err)
		}
		ids := map[domain.ID]bool{}
		rows := len(value.Pricing)
		for _, p := range value.Pricing {
			ids[p.Pricing.ID] = true
		}
		for _, family := range value.NativeAccounting {
			rows += len(family.Pricing)
			for _, p := range family.Pricing {
				ids[p.Pricing.ID] = true
			}
		}
		if len(ids) != maxUsageGroups || rows != maxUsageGroups+2 {
			t.Fatal("shared pricing identity or source projections changed", len(ids), rows)
		}
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.excess-pricing", nil, func(tx *Tx) (any, error) {
		if _, err := tx.PutPricing(r.ModelID, price.Revision, domain.NewID(), pricingFixture()); err != nil {
			return nil, err
		}
		return nil, retain(tx, maxUsageGroups+2)
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := readUsage(s, nativeSelection())
	if domain.SafeError(err).Code != domain.ResourceExhausted || !reflect.DeepEqual(value, domain.UsageSummary{}) {
		t.Fatal("501 distinct price versions returned a partial summary", err)
	}
}

func TestNativeAccountingModelGroupsWithoutDailyGranularity(t *testing.T) {
	s, _ := openTest(t)
	r, usage, input := nativeAccountingFixture(t, s)
	if _, err := retainNative(s, domain.NewID(), domain.NewID(), input, r, usage, false); err != nil {
		t.Fatal(err)
	}
	daily, err := readUsage(s, nativeSelection())
	if err != nil {
		t.Fatal(err)
	}
	selection := nativeSelection()
	selection.Granularity, selection.TimeZone = domain.UsageTimeGranularityUnspecified, ""
	summary, err := readUsage(s, selection)
	if err != nil || len(summary.NativeAccounting) != 2 || len(summary.NativeAccounting[0].Days) != 0 || len(summary.NativeAccounting[0].Models) != 1 || summary.NativeAccounting[0].Models[0].ModelID != r.ModelID || summary.NativeAccounting[0].Models[0].ProviderID != r.ProviderID || summary.NativeAccounting[0].Models[0].Totals.Units != 1 {
		t.Fatal("default granularity lost original model accounting", summary, err)
	}
	if !reflect.DeepEqual(summary.NativeAccounting[0].Models, daily.NativeAccounting[0].Models) {
		t.Fatal("daily granularity changed model attribution or counters")
	}
}

func TestNativeAccountingAtomicRetentionReplayRestartPriceAndBudget(t *testing.T) {
	s, root := openTest(t)
	r, o, input := nativeAccountingFixture(t, s)
	p := preparePrice(t, s, r)
	rate, zero := "1", "0"
	basis := pricingFixture()
	basis.InputPerMillion = &rate
	basis.OutputPerMillion = &zero
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.native-price", nil, func(tx *Tx) (any, error) { return tx.PutPricing(r.ModelID, p.Revision, domain.NewID(), basis) })
	if err != nil {
		t.Fatal(err)
	}
	request, source := domain.NewID(), domain.NewID()
	if _, err := retainNative(s, request, source, input, r, o, true); err == nil {
		t.Fatal("expected rollback")
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM native_accounting").Scan(&n); err != nil || n != 0 {
		t.Fatal("partial ledger", n, err)
	}
	if _, err := s.Get(context.Background(), domain.UsageKind, source); err == nil {
		t.Fatal("partial original source")
	}
	if _, err := retainNative(s, request, source, input, r, o, false); err != nil {
		t.Fatal(err)
	}
	if result, err := retainNative(s, request, source, input, r, o, false); err != nil || !result.Replayed {
		t.Fatal("receipt not replayed", result, err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		e, err := tx.SessionBudgetEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if e.KnownAmount != "0.000025" || e.CompleteResponses != 0 || e.CompleteNativeUnits != 1 {
			t.Fatal("native budget became a response", e)
		}
		return tx.RequireSessionBudget(r.SessionID, &domain.EstimatedCostBudget{Currency: "USD", Threshold: "0.000025"})
	}); domain.SafeError(err).Code != domain.BudgetReached {
		t.Fatal("budget failed to gate exact native subtotal", err)
	}
	legacy := nativeSelection()
	legacy.AccountingProfile = domain.AccountingProfileUnspecified
	if _, err := readUsage(s, legacy); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatal("response-only profile was accepted", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.native-reprice", nil, func(tx *Tx) (any, error) {
		active, err := tx.ActivePricing(r.ModelID)
		if err != nil {
			return nil, err
		}
		basis.Currency = "EUR"
		return tx.PutPricing(r.ModelID, active.Revision, domain.NewID(), basis)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if result, err := retainNative(s, request, source, input, r, o, false); err != nil || !result.Replayed {
		t.Fatal("restart lost replay", err)
	}
	if _, err := retainNative(s, domain.NewID(), domain.NewID(), input, r, o, false); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("restart lost source dedup", err)
	}
	v, err := readUsage(s, nativeSelection())
	if err != nil {
		t.Fatal(err)
	}
	a := &v.NativeAccounting[0]
	if a == nil || a.Totals.Units != 1 || a.Totals.Input.KnownTotal != "25" || a.Totals.Total.KnownTotal != "" || a.Totals.Currencies[0].KnownAmount != "0.000025" || a.Totals.Currencies[0].Currency != "USD" || len(a.Groups) != 1 || len(a.Models) != 1 || len(a.Days) == 0 || len(a.Pricing) != 1 {
		t.Fatal("wrong native summary", a)
	}
	filter := nativeSelection()
	filter.AccountID = domain.NewID()
	v, err = readUsage(s, filter)
	if err != nil || v.NativeAccounting[0].Totals.Units != 0 {
		t.Fatal("wrong original-account filter", err)
	}
	// Original response estimates still count only responses, while their known
	// subtotal combines with native units exactly once for the authoritative gate.
	if _, _, err := writeResponse(s, domain.NewID(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		e, err := tx.SessionBudgetEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if e.KnownAmount != "0.000025" || e.CompleteNativeUnits != 1 {
			t.Fatal(e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE native_accounting SET estimate=json_set(estimate,'$.known_amount','99')"); err != nil {
		t.Fatal(err)
	}
	v, err = readUsage(s, nativeSelection())
	if domain.SafeError(err).Code != domain.RecoveryRequired || v.NativeAccounting != nil || v.Totals.Responses != 0 {
		t.Fatal("corruption returned partial summary", v, err)
	}
}

func TestNativeBudgetCombinesAmountsWithoutResponseCountOrWriteContamination(t *testing.T) {
	s, _ := openTest(t)
	r, o, input := nativeAccountingFixture(t, s)
	p := preparePrice(t, s, r)
	rate, zero := "1", "0"
	basis := pricingFixture()
	basis.InputPerMillion = &rate
	basis.OutputPerMillion = &zero
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.mixed-price", nil, func(tx *Tx) (any, error) { return tx.PutPricing(r.ModelID, p.Revision, domain.NewID(), basis) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retainNative(s, domain.NewID(), domain.NewID(), input, r, o, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeResponse(s, domain.NewID(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		e, err := tx.SessionBudgetEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if e.KnownAmount != "0.000035" || e.CompleteNativeUnits != 1 || e.CompleteResponses != 1 {
			t.Fatal("combined lifetime evidence changed counts", e)
		}
		raw, err := tx.SessionEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if raw.KnownAmount != "0.00001" || raw.CompleteResponses != 1 {
			t.Fatal("response write absorbed the native subtotal", raw)
		}
		return tx.RequireSessionBudget(r.SessionID, &domain.EstimatedCostBudget{Currency: "USD", Threshold: "0.000035"})
	}); domain.SafeError(err).Code != domain.BudgetReached {
		t.Fatal("mixed exact threshold did not gate", err)
	}
}

func TestOpenCodeAccountingSourceDeduplicationAndAssistantExclusion(t *testing.T) {
	s, root := openTest(t)
	r, claude, input := nativeAccountingFixture(t, s)
	first := domain.OpenCodeUsageRecord{ExecutionID: r.ExecutionID, AccountID: r.AccountID, ConnectionID: r.ConnectionID, ProviderID: r.ProviderID, ModelID: r.ModelID, Harness: domain.OpenCode, Version: domain.OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "12", CacheRead: "7", CacheWrite: "3", Output: "8", Reasoning: "2"}, NativeEstimate: "999"}}
	_ = claude
	p := preparePrice(t, s, r)
	basis := pricingFixture()
	one, two := "1", "2"
	basis.InputPerMillion, basis.OutputPerMillion = &one, &two
	if _, err := s.Mutate(context.Background(), domain.NewID(), "fixture.price", nil, func(tx *Tx) (any, error) { return tx.PutPricing(r.ModelID, p.Revision, domain.NewID(), basis) }); err != nil {
		t.Fatal(err)
	}
	retain := func(request, source domain.ID, o domain.OpenCodeUsageRecord) (Result, error) {
		return s.Mutate(context.Background(), request, "fixture.step", o, func(tx *Tx) (any, error) {
			if err := tx.PutOpenCodeUsage(source, r.SessionID, r.ProjectID, o); err != nil {
				return nil, err
			}
			return nil, tx.PutOpenCodeAccounting(source, input, r.SessionID, r.ProjectID, o)
		})
	}
	request, source := domain.NewID(), domain.NewID()
	if _, err := retain(request, source, first); err != nil {
		t.Fatal(err)
	}
	if result, err := retain(request, source, first); err != nil || !result.Replayed {
		t.Fatal("receipt lost original step", result, err)
	}
	assistant := first
	assistant.Usage.Source, assistant.Usage.NativeID = domain.OpenCodeMessageUsage, first.Usage.NativeParentID
	if _, err := retain(domain.NewID(), domain.NewID(), assistant); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Usage.NativeID = "prt_01960dcbe1fbABCDEFGHIJKLMN"
	total := "9"
	second.Usage.Counts = domain.OpenCodeTokenCounts{Input: "5", Output: "4", Reasoning: "0", CacheRead: "0", CacheWrite: "0", Total: &total}
	if _, err := retain(domain.NewID(), domain.NewID(), second); err != nil {
		t.Fatal("second step shares an input legitimately", err)
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
	if _, err := retain(domain.NewID(), domain.NewID(), first); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("source duplicated after restart", err)
	}
	v, err := readUsage(s, nativeSelection())
	if err != nil || len(v.NativeAccounting) != 2 || v.NativeAccounting[1].Totals.Units != 2 || v.NativeAccounting[1].Totals.Currencies[0].KnownAmount != "0.000055" || v.NativeAccounting[1].Totals.Currencies[0].PartialUnits != 1 || v.Totals.Responses != 0 {
		t.Fatal("overlap or lost coverage", v, err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		e, err := tx.SessionBudgetEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if e.KnownAmount != "0.000055" || e.CompleteNativeUnits != 1 || e.PartialNativeUnits != 1 || e.CompleteResponses != 0 {
			t.Fatal("wrong lifetime estimate", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAccountingCombinedModelInventoryBound(t *testing.T) {
	s, _ := openTest(t)
	r, claude, _ := nativeAccountingFixture(t, s)
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.combined-model-inventory", nil, func(tx *Tx) (any, error) {
		for i := 0; i < domain.UsageModelGroupLimit; i++ {
			model, source, input := domain.NewID(), domain.NewID(), domain.NewID()
			switch i % 3 {
			case 0:
				value := r
				value.ModelID, value.Usage.ResponseDigest = model, fmt.Sprintf("%064x", i+1)
				if _, _, err := tx.PutResponseUsage(source, value); err != nil {
					return nil, err
				}
			case 1:
				value := claude
				value.ExecutionID, value.ModelID, value.Usage.NativeEventID = domain.NewID(), model, string(domain.NewID())
				if err := tx.PutClaudeUsage(source, r.SessionID, r.ProjectID, value); err != nil {
					return nil, err
				}
				if err := tx.PutClaudeAccounting(source, input, r.SessionID, r.ProjectID, value); err != nil {
					return nil, err
				}
			case 2:
				value := domain.OpenCodeUsageRecord{ExecutionID: r.ExecutionID, AccountID: r.AccountID, ConnectionID: r.ConnectionID, ProviderID: r.ProviderID, ModelID: model, Harness: domain.OpenCode, Version: domain.OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: fmt.Sprintf("prt_%012xABCDEFGHIJKLMN", i+1), NativeEstimate: "0", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "1", Output: "0", Reasoning: "0", CacheRead: "0", CacheWrite: "0"}}}
				if err := tx.PutOpenCodeUsage(source, r.SessionID, r.ProjectID, value); err != nil {
					return nil, err
				}
				if err := tx.PutOpenCodeAccounting(source, input, r.SessionID, r.ProjectID, value); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, daily := range []bool{false, true} {
		f := nativeSelection()
		if !daily {
			f.Granularity, f.TimeZone = domain.UsageTimeGranularityUnspecified, ""
		}
		v, err := readUsage(s, f)
		if err != nil || len(v.NativeAccounting) != 2 {
			t.Fatal("bounded combined inventory was rejected", err)
		}
		models := len(v.NativeAccounting[0].Models) + len(v.NativeAccounting[1].Models)
		if daily {
			models += len(v.Analytics.Models)
		}
		if daily && models != domain.UsageModelGroupLimit || !daily && models != 333 {
			t.Fatal("complete native model inventory was truncated", models)
		}
	}
	claude.ExecutionID, claude.ModelID, claude.Usage.NativeEventID = domain.NewID(), domain.NewID(), string(domain.NewID())
	if _, err := retainNative(s, domain.NewID(), domain.NewID(), domain.NewID(), r, claude, false); err != nil {
		t.Fatal(err)
	}
	for _, daily := range []bool{false, true} {
		f := nativeSelection()
		if !daily {
			f.Granularity, f.TimeZone = domain.UsageTimeGranularityUnspecified, ""
		}
		v, err := readUsage(s, f)
		if domain.SafeError(err).Code != domain.ResourceExhausted || v.NativeAccounting != nil || v.Analytics != nil || v.Totals.Responses != 0 {
			t.Fatal("over-bound inventory returned partial accounting", err, v)
		}
	}
}
