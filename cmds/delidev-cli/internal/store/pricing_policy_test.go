// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestPricingIdentitiesRetainDefaultAutomaticActivePricesWithoutRoutes(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	m := domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: "Exact/Route-Retired"}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.default-active-price", nil, func(tx *Tx) (any, error) {
		return tx.PutAutomaticPricing(m.Key(), 0, domain.NewID(), pricingFixture(), domain.PriceProvenance{ProviderKey: "anthropic", ModelKey: m.NativeID, SnapshotSHA256: strings.Repeat("a", 64), RetrievedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		policy, e := tx.PricingPolicy(m)
		if e != nil || policy.Mode != domain.AutomaticPricing || policy.Revision != 0 {
			t.Fatal("fixture no longer has default automatic policy", policy, e)
		}
		rows, e := tx.List(Filter{Kind: domain.AgentKind, Limit: 10})
		if e != nil || len(rows) != 0 {
			t.Fatal("fixture unexpectedly has a live route", rows, e)
		}
		models, e := tx.PricingIdentities()
		if e != nil || len(models) != 1 || models[0] != m {
			t.Fatal("active identity disappeared without policy metadata or live routes", models, e)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.same-policy-identity", nil, func(tx *Tx) (any, error) { return tx.SetPricingPolicy(m, domain.AutomaticPricing, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		models, e := tx.PricingIdentities()
		if e != nil || len(models) != 1 || models[0] != m {
			t.Fatal("active and policy identities were not deduplicated", models, e)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
