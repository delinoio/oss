package store

import (
	"context"
	"path/filepath"
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
	return domain.UsageSelection{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), AccountingProfile: domain.NativeInputAccountingV1, Granularity: domain.UsageTimeGranularityDay, TimeZone: "UTC"}
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
	if err := s.db.QueryRow("SELECT count(*) FROM native_accounting_units").Scan(&n); err != nil || n != 0 {
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
		e, err := tx.SessionEstimate(r.SessionID, "USD")
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
	legacy.AccountingProfile = domain.ResponseOnlyAccounting
	old, err := readUsage(s, legacy)
	if err != nil || old.NativeAccounting != nil || old.Totals.Responses != 0 || len(old.Estimates.Currencies) != 0 {
		t.Fatal("legacy response-only shape changed", old, err)
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
	a := v.NativeAccounting
	if a == nil || a.Totals.Units != 1 || a.Totals.Input.KnownTotal != "25" || a.Totals.Total.KnownTotal != "" || a.Totals.Currencies[0].KnownAmount != "0.000025" || a.Totals.Currencies[0].Currency != "USD" || len(a.Groups) != 1 || len(a.Models) != 1 || len(a.Days) == 0 || len(a.Pricing) != 1 {
		t.Fatal("wrong native summary", a)
	}
	filter := nativeSelection()
	filter.AccountID = domain.NewID()
	v, err = readUsage(s, filter)
	if err != nil || v.NativeAccounting.Totals.Units != 0 {
		t.Fatal("wrong original-account filter", err)
	}
	// Original response estimates still count only responses, while their known
	// subtotal combines with native units exactly once for the authoritative gate.
	if _, _, err := writeResponse(s, domain.NewID(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		e, err := tx.SessionEstimate(r.SessionID, "USD")
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
	if _, err := s.db.Exec("UPDATE native_accounting_units SET estimate=json_set(estimate,'$.known_amount','99')"); err != nil {
		t.Fatal(err)
	}
	v, err = readUsage(s, nativeSelection())
	if domain.SafeError(err).Code != domain.RecoveryRequired || v.NativeAccounting != nil || v.Totals.Responses != 0 {
		t.Fatal("corruption returned partial summary", v, err)
	}
}

func TestNativeAccountingMigrationPreservesRawObservationsWithoutBackfill(t *testing.T) {
	s, root := openTest(t)
	r, o, _ := nativeAccountingFixture(t, s)
	source := domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.historical-native", nil, func(tx *Tx) (any, error) { return nil, tx.PutClaudeUsage(source, r.SessionID, r.ProjectID, o) })
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(context.Background(), domain.UsageKind, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(dropNativeAccountingFixtureSchema + "PRAGMA user_version=24;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.Get(context.Background(), domain.UsageKind, source)
	if err != nil || string(before.Data) != string(after.Data) || before.Revision != after.Revision {
		t.Fatal("migration rewrote raw history", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.forbidden-backfill", nil, func(tx *Tx) (any, error) {
		return nil, tx.PutClaudeAccounting(source, domain.NewID(), r.SessionID, r.ProjectID, o)
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("historical raw source acquired a new price/receipt", err)
	}
	var n, version int
	if s.db.QueryRow("SELECT count(*) FROM native_accounting_units").Scan(&n) != nil || n != 0 || s.db.QueryRow("PRAGMA user_version").Scan(&version) != nil || version != 25 {
		t.Fatal("migration fabricated units", n, version)
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("no original migration backup", err)
	}
	if err := ValidateBackup(context.Background(), backups[0]); err != nil {
		t.Fatal(err)
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
		e, err := tx.SessionEstimate(r.SessionID, "USD")
		if err != nil {
			return err
		}
		if e.KnownAmount != "0.000035" || e.CompleteNativeUnits != 1 || e.CompleteResponses != 1 {
			t.Fatal("combined lifetime evidence changed counts", e)
		}
		raw, err := tx.responseSessionEstimate(r.SessionID, "USD")
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
