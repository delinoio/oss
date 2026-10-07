// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSubscriptionResponseAccountingPinsIndependentServiceAndPrice(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f := seedSearch(t, s, "native service usage", domain.Archived)
	record := responseRecord(f)
	record.ProviderID = ""
	record.SubscriptionService = domain.SubscriptionChatGPT
	var price PricingVersion
	amount := "1"
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.native-price", nil, func(tx *Tx) (any, error) {
		model := domain.Model{SourceKind: domain.SubscriptionModel, SubscriptionService: domain.SubscriptionChatGPT, Name: "Native", NativeID: "fixture", Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}
		if _, err := tx.Put(domain.ModelKind, record.ModelID, 0, "", "", model); err != nil {
			return nil, err
		}
		var err error
		price, err = tx.PutPricing(record.ModelID, 0, domain.NewID(), domain.TokenPricing{Currency: "USD", Source: "Explicit fixture basis", AsOf: "2026-10-03", InputMode: domain.UniformInputPrice, InputPerMillion: &amount, OutputPerMillion: &amount})
		return price, err
	})
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if _, _, err := writeResponse(s, id, record); err != nil {
		t.Fatal(err)
	}
	estimate, basis := readEstimate(t, s, id)
	if basis == nil || basis.ID != price.ID || basis.ProviderID != "" || basis.SubscriptionService != domain.SubscriptionChatGPT || estimate.Coverage != domain.EstimateComplete {
		t.Fatal("service estimate lost original identity", estimate, basis)
	}
	var summary domain.UsageSummary
	err = s.Read(ctx, func(tx *Tx) error {
		var err error
		summary, err = tx.UsageSummary(domain.UsageSelection{From: time.Now().Add(-time.Hour), Until: time.Now().Add(time.Hour), SubscriptionService: domain.SubscriptionChatGPT, AccountingProfile: domain.NativeUnitsV1Accounting})
		return err
	})
	if err != nil || summary.Totals.Responses != 1 || len(summary.Groups) != 1 || summary.Groups[0].ProviderID != "" || summary.Groups[0].SubscriptionService != domain.SubscriptionChatGPT {
		t.Fatal("service-only summary required a Provider", summary, err)
	}
	if _, err := s.db.Exec("UPDATE pricing_versions SET subscription_service='claude' WHERE id=?", price.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := func() (domain.ResponseEstimate, *PricingVersion, error) {
		var e domain.ResponseEstimate
		var p *PricingVersion
		err := s.Read(ctx, func(tx *Tx) error { var err error; e, p, err = tx.ResponseEstimate(id); return err })
		return e, p, err
	}(); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("cross-service price corruption accepted", err)
	}
}
