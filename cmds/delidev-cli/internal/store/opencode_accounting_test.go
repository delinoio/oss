// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
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
