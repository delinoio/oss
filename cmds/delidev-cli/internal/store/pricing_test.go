package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func pricingFixture() domain.TokenPricing {
	input, output := "2.5", "10"
	return domain.TokenPricing{Currency: "USD", Source: "Explicit test source", AsOf: "2026-09-25", InputMode: domain.UniformInputPrice, InputPerMillion: &input, OutputPerMillion: &output, Exclusions: []string{"Token estimate only; provider fees excluded."}}
}
func preparePrice(t *testing.T, s *Store, record domain.ResponseUsageRecord) PricingVersion {
	t.Helper()
	var price PricingVersion
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.price", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProviderKind, record.ProviderID, 0, "", "", domain.Provider{Name: "Fixture"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.ModelKind, record.ModelID, 0, "", "", domain.Model{Name: "Fixture", ProviderID: record.ProviderID, NativeID: "fixture", Manual: true}); err != nil {
			return nil, err
		}
		var err error
		price, err = tx.PutPricing(record.ModelID, 0, domain.NewID(), pricingFixture())
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return price
}
func readEstimate(t *testing.T, s *Store, id domain.ID) (domain.ResponseEstimate, *PricingVersion) {
	t.Helper()
	var estimate domain.ResponseEstimate
	var price *PricingVersion
	err := s.Read(context.Background(), func(tx *Tx) error { var err error; estimate, price, err = tx.ResponseEstimate(id); return err })
	if err != nil {
		t.Fatal(err)
	}
	return estimate, price
}
func TestPricingVersionsCannotRewriteHistoricalEstimate(t *testing.T) {
	s, root := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	first := preparePrice(t, s, record)
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	estimate, price := readEstimate(t, s, id)
	if estimate.KnownAmount != "0.000065" || estimate.Coverage != domain.EstimateComplete || price == nil || price.ID != first.ID || estimate.PricingID != first.ID {
		t.Fatal("wrong historical basis", estimate, price)
	}
	secondBasis := pricingFixture()
	rate := "20"
	secondBasis.InputPerMillion = &rate
	secondBasis.Currency = "EUR"
	var second PricingVersion
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.reprice", nil, func(tx *Tx) (any, error) {
		var err error
		second, err = tx.PutPricing(record.ModelID, first.Revision, domain.NewID(), secondBasis)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Sequence++
	if retained, replayed, err := writeResponse(s, domain.NewID(), record); err != nil || !replayed || retained != id {
		t.Fatal("replayed response repriced", err)
	}
	after, basis := readEstimate(t, s, id)
	beforeJSON, _ := json.Marshal(estimate)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) || basis.ID != first.ID {
		t.Fatal("new price changed old cost")
	}
	record.Usage.ResponseDigest = strings.Repeat("b", 64)
	next := domain.NewID()
	if _, _, err := writeResponse(s, next, record); err != nil {
		t.Fatal(err)
	}
	value, current := readEstimate(t, s, next)
	if value.Currency != "EUR" || value.KnownAmount != "0.00024" || current.ID != second.ID {
		t.Fatal("future observation did not retain selected basis", value)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, basis = readEstimate(t, s, id)
	if after.KnownAmount != estimate.KnownAmount || basis.ID != first.ID {
		t.Fatal("restart lost historical basis")
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.stale-price", nil, func(tx *Tx) (any, error) {
		return tx.PutPricing(record.ModelID, first.Revision, domain.NewID(), pricingFixture())
	})
	assertCode(t, err, domain.Conflict)
}
func TestMissingPriceRemainsUnknownAfterLaterConfiguration(t *testing.T) {
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	preparePrice(t, s, record)
	value, basis := readEstimate(t, s, id)
	if value.KnownAmount != "" || basis != nil || value.Coverage != domain.EstimateUnavailable {
		t.Fatal("new prices backfilled unpriced history")
	}
}

func TestPricingPublicationRollbackProviderIsolationAndConfigurationDeletion(t *testing.T) {
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	price := preparePrice(t, s, record)
	id := domain.NewID()
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.price-publication-rollback", nil, func(tx *Tx) (any, error) {
		if _, _, err := tx.PutResponseUsage(id, record); err != nil {
			return nil, err
		}
		return nil, domain.Fail(domain.Conflict, "rollback", "retry")
	})
	assertCode(t, err, domain.Conflict)
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM response_estimates WHERE usage_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed publication retained pricing", err)
	}
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	other := record
	other.ProviderID = domain.NewID()
	other.Usage.ResponseDigest = strings.Repeat("c", 64)
	otherID := domain.NewID()
	if _, _, err := writeResponse(s, otherID, other); err != nil {
		t.Fatal(err)
	}
	unpriced, basis := readEstimate(t, s, otherID)
	if basis != nil || unpriced.KnownAmount != "" {
		t.Fatal("another provider's basis repriced immutable execution")
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.remove-priced-model", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ModelKind, record.ModelID, 1) })
	if err != nil {
		t.Fatal(err)
	}
	retained, basis := readEstimate(t, s, id)
	if basis == nil || basis.ID != price.ID || retained.KnownAmount != "0.000065" {
		t.Fatal("configuration deletion lost historical basis")
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM active_pricing WHERE model_id=?", record.ModelID).Scan(&count); err != nil || count != 0 {
		t.Fatal("removed model kept active selection", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.remove-priced-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, record.SessionID, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM response_estimates").Scan(&count); err != nil || count != 0 {
		t.Fatal("session deletion retained estimates", err)
	}
}

func TestEstimateSummaryRejectsCorruptDerivedEvidenceAndReadMutation(t *testing.T) {
	s, _ := openTest(t)
	record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
	price := preparePrice(t, s, record)
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		_, err := tx.PutPricing(record.ModelID, price.Revision, domain.NewID(), pricingFixture())
		return err
	}); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("read wrote pricing", err)
	}
	good, err := readUsage(s, usageWindow())
	if err != nil || len(good.Pricing) != 1 || good.Estimates.Currencies[0].KnownAmount != "0.000065" {
		t.Fatal("missing estimate", err)
	}
	if _, err = s.db.Exec("UPDATE response_estimates SET body=json_set(body,'$.known_amount','123') WHERE usage_id=?", id); err != nil {
		t.Fatal(err)
	}
	partial, err := readUsage(s, usageWindow())
	assertCode(t, err, domain.RecoveryRequired)
	if partial.Totals.Responses != 0 || len(partial.Estimates.Currencies) > 0 {
		t.Fatal("corruption returned partial totals")
	}
}

func TestCompactionPricingRetainsNullableCountersAndOriginalBasis(t *testing.T) {
	for _, field := range []string{"input-only", "output-only", "missing", "unknown-cache-split"} {
		t.Run(field, func(t *testing.T) {
			s, root := openTest(t)
			record := responseRecord(seedSearch(t, s, "source", domain.NotArchived))
			record.CompactionSourceTurn = domain.NativeIdentity(record.TurnID)
			record.TurnID = ""
			record.Usage.Source = domain.CompactionHTTPResponse
			record.Usage.Counts = nil
			count := int64(10)
			if field == "input-only" || field == "unknown-cache-split" {
				record.Usage.Counts = &domain.NativeTokenCounts{Input: &count}
			} else if field == "output-only" {
				record.Usage.Counts = &domain.NativeTokenCounts{Output: &count}
			}
			price := preparePrice(t, s, record)
			if field == "unknown-cache-split" {
				basis := pricingFixture()
				basis.InputMode = domain.CachedInputPrice
				rate := "1"
				basis.CachedInputPerMillion = &rate
				_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.split", nil, func(tx *Tx) (any, error) {
					var err error
					price, err = tx.PutPricing(record.ModelID, price.Revision, domain.NewID(), basis)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			id := domain.NewID()
			if _, _, err := writeResponse(s, id, record); err != nil {
				t.Fatal("nullable compaction was rejected after HTTP completion", err)
			}
			value, selected := readEstimate(t, s, id)
			if selected == nil || selected.ID != price.ID {
				t.Fatal("lost original compaction price")
			}
			switch field {
			case "input-only":
				if value.KnownAmount != "0.000025" || value.Coverage != domain.EstimatePartial || value.Output.State != domain.ComponentMissingUsage {
					t.Fatal(value)
				}
			case "output-only":
				if value.KnownAmount != "0.0001" || value.Coverage != domain.EstimatePartial || value.Input.State != domain.ComponentMissingUsage {
					t.Fatal(value)
				}
			default:
				if value.KnownAmount != "" || value.Coverage != domain.EstimateUnavailable {
					t.Fatal("unavailable split became spend", value)
				}
			}
			before, _ := json.Marshal(value)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			s, err = Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if retained, replayed, err := writeResponse(s, domain.NewID(), record); err != nil || !replayed || retained != id {
				t.Fatal("compaction replay charged again", err)
			}
			after, selected := readEstimate(t, s, id)
			raw, _ := json.Marshal(after)
			if selected.ID != price.ID || string(raw) != string(before) {
				t.Fatal("restart rewrote nullable pricing")
			}
		})
	}
}
