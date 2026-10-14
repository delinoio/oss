// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeReviewUsageRetainsUnknownAndDeduplicatesAfterRestart(t *testing.T) {
	ctx := context.Background()
	s, root := openTest(t)
	record := responseRecord(seedSearch(t, s, "native review", domain.NotArchived))
	record.Purpose = domain.NativeCodeReviewUsage
	record.Usage.Counts = nil
	job, id := domain.NewID(), domain.NewID()
	write := func(record domain.ResponseUsageRecord) (domain.ID, bool, error) {
		var retained domain.ID
		var duplicate bool
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.native-review-usage", record, func(tx *Tx) (any, error) {
			var err error
			retained, duplicate, err = tx.PutNativeCodeReviewUsage(job, id, record)
			return nil, err
		})
		return retained, duplicate, err
	}
	if _, duplicate, err := write(record); err != nil || duplicate {
		t.Fatal("original receipt", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record.Sequence++
	if retained, duplicate, err := write(record); err != nil || !duplicate || retained != id {
		t.Fatal("restart replay changed receipt", err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		values, err := tx.NativeCodeReviewUsage(job)
		if err != nil {
			return err
		}
		if len(values) != 1 || values[0].Record.Usage.Counts != nil || values[0].Record.Sequence != 3 || values[0].Estimate.Coverage != domain.EstimateUnavailable {
			t.Fatal("unknown counters invented zero or replay changed source")
		}
		total, err := tx.SessionBudgetEstimate(record.SessionID, "")
		if err == nil && total.UnavailableResponses != 1 {
			t.Fatal("auxiliary replay charged budget twice")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := record
	changed.TurnID = string(domain.NewID())
	if _, _, err := write(changed); err == nil {
		t.Fatal("foreign turn reused original response")
	}
	ordinary := record
	ordinary.Purpose = domain.ConversationUsage
	if _, _, err := writeResponse(s, domain.NewID(), ordinary); err == nil {
		t.Fatal("auxiliary response charged ordinary history")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM response_usage").Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy purpose table widened", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.remove-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, record.SessionID, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Read(ctx, func(tx *Tx) error {
		values, err := tx.NativeCodeReviewUsage(job)
		if err == nil && len(values) != 0 {
			t.Fatal("deleted session retained auxiliary receipt")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type reviewPricingFixture struct {
	t      *testing.T
	s      *Store
	record domain.ResponseUsageRecord
	job    domain.ID
}

func newReviewPricingFixture(t *testing.T) *reviewPricingFixture {
	t.Helper()
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "native review pricing", domain.NotArchived))
	record.Purpose = domain.NativeCodeReviewUsage
	return &reviewPricingFixture{t: t, s: s, record: record, job: domain.NewID()}
}

// setPricingTimes moves every retained price activation and clear instant into the
// past so a test can place observations before and after them deterministically.
func (f *reviewPricingFixture) setPricingTimes(activated time.Time, cleared time.Time) {
	f.t.Helper()
	if _, err := f.s.db.Exec("UPDATE pricing_versions SET created_at=?", activated.UnixMilli()); err != nil {
		f.t.Fatal(err)
	}
	if !cleared.IsZero() {
		if _, err := f.s.db.Exec("UPDATE metadata SET value=json_set(value,'$.cleared_at',?) WHERE key LIKE 'pricing-cleared-v1:%'", cleared.UnixMilli()); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *reviewPricingFixture) activate(expected uint64, rate string) PricingVersion {
	f.t.Helper()
	basis := pricingFixture()
	basis.InputPerMillion = &rate
	var price PricingVersion
	_, err := f.s.Mutate(context.Background(), domain.NewID(), "fixture.activate", nil, func(tx *Tx) (any, error) {
		var err error
		price, err = tx.PutPricing(f.record.ModelID, expected, domain.NewID(), basis)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return price
}

func (f *reviewPricingFixture) clear() {
	f.t.Helper()
	_, err := f.s.Mutate(context.Background(), domain.NewID(), "fixture.clear", nil, func(tx *Tx) (any, error) {
		return nil, tx.ClearActivePricing(domain.ModelIdentity{ProviderID: f.record.ProviderID, NativeID: "fixture"})
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *reviewPricingFixture) publish(record domain.ResponseUsageRecord, id domain.ID) (domain.ID, bool) {
	f.t.Helper()
	var retained domain.ID
	var duplicate bool
	_, err := f.s.Mutate(context.Background(), domain.NewID(), "fixture.native-review-usage", record, func(tx *Tx) (any, error) {
		var err error
		retained, duplicate, err = tx.PutNativeCodeReviewUsage(f.job, id, record)
		return nil, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return retained, duplicate
}

func (f *reviewPricingFixture) receipt(id domain.ID) NativeCodeReviewUsage {
	f.t.Helper()
	var found NativeCodeReviewUsage
	err := f.s.Read(context.Background(), func(tx *Tx) error {
		values, err := tx.NativeCodeReviewUsage(f.job)
		for _, value := range values {
			if value.ID == id {
				found = value
			}
		}
		return err
	})
	if err != nil || found.ID != id {
		f.t.Fatal("receipt unavailable", err)
	}
	return found
}

func (f *reviewPricingFixture) budget(currency domain.Currency) domain.BudgetEvidence {
	f.t.Helper()
	var total domain.BudgetEvidence
	err := f.s.Read(context.Background(), func(tx *Tx) error {
		var err error
		total, err = tx.NativeCodeReviewEstimate(f.record.SessionID, currency)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return total
}

func TestNativeReviewDelayedFirstPublicationUsesObservationTimePrice(t *testing.T) {
	f := newReviewPricingFixture(t)
	first := preparePrice(t, f.s, f.record)
	now := time.Now().UTC().Truncate(time.Millisecond)
	f.setPricingTimes(now.Add(-3*time.Hour), time.Time{})
	observed := now.Add(-2 * time.Hour)
	second := f.activate(first.Revision, "20")

	record := f.record
	record.ObservedAt = observed
	id := domain.NewID()
	if retained, duplicate := f.publish(record, id); duplicate || retained != id {
		t.Fatal("first delayed insert")
	}
	receipt := f.receipt(id)
	if !receipt.CreatedAt.Equal(observed) || !receipt.Record.ObservedAt.Equal(observed) {
		t.Fatal("receipt did not keep the original observation time", receipt.CreatedAt)
	}
	if receipt.Pricing == nil || receipt.Pricing.ID != first.ID || receipt.Estimate.PricingID != first.ID || receipt.Estimate.KnownAmount != "0.000065" {
		t.Fatal("delayed insert did not use the version eligible at observation", receipt.Pricing)
	}
	total := f.budget("USD")
	if total.CompleteResponses != 1 || total.KnownAmount != "0.000065" {
		t.Fatal("session budget must include the observation-time estimate once", total)
	}

	// Replay with the newest price active and a different observation time.
	f.setPricingTimes(now.Add(-3*time.Hour), time.Time{})
	third := f.activate(second.Revision, "30")
	if third.ID == first.ID {
		t.Fatal("fixture did not activate a newer price")
	}
	replay := record
	replay.Sequence++
	replay.ObservedAt = now
	if retained, duplicate := f.publish(replay, domain.NewID()); !duplicate || retained != id {
		t.Fatal("replay changed receipt identity")
	}
	replayed := f.receipt(id)
	if !replayed.CreatedAt.Equal(observed) || replayed.Pricing.ID != first.ID || replayed.Estimate.KnownAmount != "0.000065" {
		t.Fatal("replay repriced or moved the receipt")
	}
	if again := f.budget("USD"); again != total {
		t.Fatal("replay charged the session budget again", again)
	}
}

func TestNativeReviewReceiptCommittedBeforeOutageIsNeverRepriced(t *testing.T) {
	f := newReviewPricingFixture(t)
	first := preparePrice(t, f.s, f.record)
	record := f.record
	record.ObservedAt = time.Now().UTC().Truncate(time.Millisecond)
	id := domain.NewID()
	f.publish(record, id)
	before := f.receipt(id)
	budget := f.budget("USD")
	if before.Pricing == nil || before.Pricing.ID != first.ID {
		t.Fatal("prompt publication must use the live price")
	}
	f.activate(first.Revision, "20")
	if retained, duplicate := f.publish(record, domain.NewID()); !duplicate || retained != id {
		t.Fatal("committed receipt did not replay")
	}
	after := f.receipt(id)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) || f.budget("USD") != budget {
		t.Fatal("earlier receipt was repriced")
	}
}

func TestNativeReviewUnavailablePriceStaysUnavailableAfterPriceAppears(t *testing.T) {
	f := newReviewPricingFixture(t)
	price := preparePrice(t, f.s, f.record)
	now := time.Now().UTC().Truncate(time.Millisecond)
	// The only price became eligible after the observation.
	f.setPricingTimes(now.Add(-time.Hour), time.Time{})
	record := f.record
	record.ObservedAt = now.Add(-2 * time.Hour)
	id := domain.NewID()
	f.publish(record, id)
	receipt := f.receipt(id)
	if receipt.Pricing != nil || receipt.Estimate.Coverage != domain.EstimateUnavailable || receipt.Estimate.PricingID != "" {
		t.Fatal("observation with no eligible price must stay unavailable", receipt.Estimate, price.ID)
	}
	budget := f.budget("")
	if budget.UnavailableResponses != 1 || budget.CompleteResponses != 0 {
		t.Fatal("unavailable estimate was not counted once", budget)
	}
	replay := record
	replay.Sequence++
	if retained, duplicate := f.publish(replay, domain.NewID()); !duplicate || retained != id {
		t.Fatal("replay changed receipt identity")
	}
	if again := f.receipt(id); again.Pricing != nil || again.Estimate.Coverage != domain.EstimateUnavailable || f.budget("") != budget {
		t.Fatal("a later price repriced an unavailable receipt")
	}
}

func TestNativeReviewClearedPriceIsNotEligibleAfterClear(t *testing.T) {
	f := newReviewPricingFixture(t)
	first := preparePrice(t, f.s, f.record)
	f.clear()
	now := time.Now().UTC().Truncate(time.Millisecond)
	f.setPricingTimes(now.Add(-4*time.Hour), now.Add(-2*time.Hour))
	f.activate(0, "20")
	if _, err := f.s.db.Exec("UPDATE pricing_versions SET created_at=? WHERE id<>?", now.Add(-time.Hour).UnixMilli(), first.ID); err != nil {
		t.Fatal(err)
	}

	before := f.record
	before.ObservedAt = now.Add(-3 * time.Hour)
	beforeID := domain.NewID()
	f.publish(before, beforeID)
	if got := f.receipt(beforeID); got.Pricing == nil || got.Pricing.ID != first.ID {
		t.Fatal("price active before the clear must be eligible", got.Pricing)
	}

	gap := f.record
	gap.Usage.ResponseDigest = strings.Repeat("b", 64)
	gap.ObservedAt = now.Add(-90 * time.Minute)
	gapID := domain.NewID()
	f.publish(gap, gapID)
	if got := f.receipt(gapID); got.Pricing != nil || got.Estimate.Coverage != domain.EstimateUnavailable {
		t.Fatal("observation between clear and next activation must be unavailable", got.Pricing)
	}

	later := f.record
	later.Usage.ResponseDigest = strings.Repeat("c", 64)
	later.ObservedAt = now.Add(-30 * time.Minute)
	laterID := domain.NewID()
	f.publish(later, laterID)
	if got := f.receipt(laterID); got.Pricing == nil || got.Pricing.ID == first.ID {
		t.Fatal("observation after the next activation must use the new version", got.Pricing)
	}
}

func TestNativeReviewObservationFallbacksAndClamp(t *testing.T) {
	f := newReviewPricingFixture(t)
	first := preparePrice(t, f.s, f.record)

	legacy := f.record
	legacyID := domain.NewID()
	f.publish(legacy, legacyID)
	got := f.receipt(legacyID)
	if !got.Record.ObservedAt.IsZero() || got.Pricing == nil || got.Pricing.ID != first.ID || time.Since(got.CreatedAt) > time.Minute {
		t.Fatal("a publication without an observation time must use commit time and the live price")
	}

	future := f.record
	future.Usage.ResponseDigest = strings.Repeat("d", 64)
	future.ObservedAt = time.Now().UTC().Add(24 * time.Hour)
	futureID := domain.NewID()
	f.publish(future, futureID)
	if got := f.receipt(futureID); got.CreatedAt.After(time.Now().UTC()) || got.Record.ObservedAt.After(time.Now().UTC()) {
		t.Fatal("a future observation must be clamped to the commit time", got.CreatedAt)
	}
}

func TestNativeReviewPricingLegacyClearRemainsUnknownAfterReactivationAndRestart(t *testing.T) {
	f := newReviewPricingFixture(t)
	first := preparePrice(t, f.s, f.record)
	f.clear()
	if _, err := f.s.db.Exec("DELETE FROM metadata WHERE key=?", pricingClearedKey(first.ID)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	f.setPricingTimes(now.Add(-4*time.Hour), time.Time{})
	second := f.activate(0, "20")
	if _, err := f.s.db.Exec("UPDATE pricing_versions SET created_at=? WHERE id=?", now.Add(-time.Hour).UnixMilli(), second.ID); err != nil {
		t.Fatal(err)
	}
	var marker string
	if err := f.s.db.QueryRow("SELECT value FROM metadata WHERE key=?", pricingUnknownClearKey(first.ID)).Scan(&marker); err != nil || marker != `{"version":1}` {
		t.Fatal("reactivation did not retain the unknown legacy clear", err, marker)
	}

	gap := f.record
	gap.ObservedAt = now.Add(-90 * time.Minute)
	gapID := domain.NewID()
	f.publish(gap, gapID)
	if got := f.receipt(gapID); got.Pricing != nil || got.Estimate.Coverage != domain.EstimateUnavailable || got.Estimate.PricingID != "" {
		t.Fatal("legacy gap acquired an unsupported price", got)
	}
	budget := f.budget("")
	if budget.UnavailableResponses != 1 || budget.CompleteResponses != 0 {
		t.Fatal("legacy gap did not contribute one unavailable budget response", budget)
	}

	root := f.s.root
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.s = reopened
	if err := f.s.Read(context.Background(), func(tx *Tx) error {
		price, err := tx.PricingAt(f.record.ModelID, gap.ObservedAt)
		if err == nil && price != nil {
			t.Fatal("restart lost the unknown-clear boundary")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	third := f.activate(second.Revision, "30")
	if third.ID == second.ID {
		t.Fatal("third activation did not create a new price")
	}
	replay := gap
	replay.Sequence++
	replay.ObservedAt = now
	if retained, duplicate := f.publish(replay, domain.NewID()); !duplicate || retained != gapID {
		t.Fatal("legacy gap replay changed receipt identity")
	}
	if got := f.receipt(gapID); got.Pricing != nil || got.Estimate.Coverage != domain.EstimateUnavailable || f.budget("") != budget {
		t.Fatal("later activation repriced or recharged the legacy gap", got)
	}

	after := f.record
	after.Usage.ResponseDigest = strings.Repeat("e", 64)
	after.ObservedAt = now.Add(-30 * time.Minute)
	afterID := domain.NewID()
	f.publish(after, afterID)
	if got := f.receipt(afterID); got.Pricing == nil || got.Pricing.ID != second.ID {
		t.Fatal("observation after reactivation did not select its eligible price", got.Pricing)
	}
	if err := f.s.Read(context.Background(), func(tx *Tx) error {
		price, err := tx.PricingAt(f.record.ModelID, gap.ObservedAt)
		if err == nil && price != nil {
			t.Fatal("subsequent activation lost the unknown-clear boundary")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReviewPricingFreshResolverKeepsObservationTimePricing(t *testing.T) {
	for _, mode := range []string{"replace", "clear"} {
		t.Run(mode, func(t *testing.T) {
			f := newReviewPricingFixture(t)
			first := preparePrice(t, f.s, f.record)
			now := time.Now().UTC().Truncate(time.Millisecond)
			f.setPricingTimes(now.Add(-2*time.Hour), time.Time{})
			var replacement PricingVersion
			f.s.SetAutomaticPricing(func(tx *Tx, model domain.ModelIdentity) error {
				if mode == "clear" {
					return tx.ClearActivePricing(model)
				}
				basis := pricingFixture()
				rate := "20"
				basis.InputPerMillion = &rate
				var err error
				replacement, err = tx.PutPricing(model.Key(), first.Revision, domain.NewID(), basis)
				return err
			})
			record := f.record
			record.ObservedAt = now.Add(-time.Second)
			id := domain.NewID()
			f.publish(record, id)
			got := f.receipt(id)
			if got.Pricing == nil || got.Pricing.ID != first.ID || got.Estimate.PricingID != first.ID || got.Estimate.KnownAmount != "0.000065" {
				t.Fatal("resolver mutation changed the observation-time estimate", got)
			}
			if total := f.budget("USD"); total.CompleteResponses != 1 || total.KnownAmount != "0.000065" {
				t.Fatal("resolver mutation changed the budget contribution", total)
			}
			var active *PricingVersion
			if err := f.s.Read(context.Background(), func(tx *Tx) error {
				var err error
				active, err = tx.RetainedActivePricing(f.record.ModelID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "replace" && (active == nil || active.ID != replacement.ID) || mode == "clear" && active != nil {
				t.Fatal("resolver mutation did not persist independently", active)
			}
			if mode == "clear" {
				afterClear := f.record
				afterClear.Usage.ResponseDigest = strings.Repeat("f", 64)
				afterClear.ObservedAt = time.Now().UTC().Truncate(time.Millisecond)
				afterID := domain.NewID()
				f.publish(afterClear, afterID)
				if next := f.receipt(afterID); next.Pricing != nil || next.Estimate.Coverage != domain.EstimateUnavailable {
					t.Fatal("observation after resolver clear borrowed an old price", next)
				}
			}
			budget := f.budget("USD")
			replay := record
			replay.Sequence++
			replay.ObservedAt = time.Now().UTC()
			if retained, duplicate := f.publish(replay, domain.NewID()); !duplicate || retained != id {
				t.Fatal("resolver receipt replay changed identity")
			}
			if again := f.receipt(id); again.Pricing == nil || again.Pricing.ID != first.ID || again.Estimate.KnownAmount != "0.000065" || f.budget("USD") != budget {
				t.Fatal("resolver receipt replay repriced or recharged", again)
			}
		})
	}
}

func TestNativeReviewPricingFreshResolverFirstUseOnlyWithoutPriorHistory(t *testing.T) {
	for _, priorClear := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-use", true: "prior-clear"}[priorClear], func(t *testing.T) {
			f := newReviewPricingFixture(t)
			if priorClear {
				preparePrice(t, f.s, f.record)
				f.clear()
				f.setPricingTimes(time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour))
			}
			var created PricingVersion
			f.s.SetAutomaticPricing(func(tx *Tx, model domain.ModelIdentity) error {
				basis := pricingFixture()
				rate := "20"
				basis.InputPerMillion = &rate
				var err error
				created, err = tx.PutPricing(model.Key(), 0, domain.NewID(), basis)
				return err
			})
			record := f.record
			record.ObservedAt = time.Now().UTC().Truncate(time.Millisecond).Add(-time.Second)
			id := domain.NewID()
			f.publish(record, id)
			got := f.receipt(id)
			if priorClear {
				if got.Pricing != nil || got.Estimate.Coverage != domain.EstimateUnavailable {
					t.Fatal("prior cleared history borrowed a new live price", got)
				}
			} else if got.Pricing == nil || got.Pricing.ID != created.ID || got.Estimate.PricingID != created.ID {
				t.Fatal("genuine first use lost live resolution", got)
			}
		})
	}
}
