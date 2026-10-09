// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestPricingIdentitiesScanPastDuplicateAgentRows(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	common := domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: "common"}
	later := domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: "later"}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.duplicate-agent-prices", nil, func(tx *Tx) (any, error) {
		for i := 0; i < 5002; i++ {
			m := common
			if i == 5001 {
				m = later
			}
			a := domain.Agent{Name: "Fixture", Harness: domain.ClaudeCode, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: m, MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: domain.NewID(), Weight: 1}}}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
			if e := a.Validate(); e != nil {
				return nil, e
			}
			raw, e := json.Marshal(a)
			if e != nil {
				return nil, e
			}
			// Insert original valid bodies directly to isolate pricing discovery from
			// account eligibility and the ordinary configuration receipt API.
			_, e = tx.tx.ExecContext(ctx, "INSERT INTO entities(kind,id,revision,body,created_at,updated_at) VALUES('agent',?,1,?,0,0)", fmt.Sprintf("00000000-0000-4000-8000-%012d", i), raw)
			if e != nil {
				return nil, e
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		models, e := tx.PricingIdentities()
		if e != nil || len(models) != 2 || models[0] != common || models[1] != later {
			t.Fatal("later distinct Agent identity disappeared", models, e)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
