// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tokenprices"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExactAutomaticPricingRetainsManualAndImmutableHistory(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &Service{Store: db}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	m := domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: "claude-exact"}
	input, output := "1", "5"
	basis := domain.TokenPricing{Currency: domain.Currency("USD"), Source: tokenprices.URL, AsOf: "2026-10-09", InputMode: domain.UniformInputPrice, InputPerMillion: &input, OutputPerMillion: &output}
	checked := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	snapshot := tokenprices.Snapshot{State: tokenprices.Current, Checked: checked, Catalog: tokenprices.Catalog{Digest: strings.Repeat("a", 64), References: map[string]tokenprices.Reference{"anthropic\x00claude-exact": {Provider: "anthropic", Model: "claude-exact", Basis: &basis}}}}
	var original, manual domain.ID
	mutate := func(f func(*store.Tx) error) {
		t.Helper()
		_, e := db.Mutate(ctx, domain.NewID(), "fixture.prices", struct{}{}, func(tx *store.Tx) (any, error) { return struct{}{}, f(tx) })
		if e != nil {
			t.Fatal(e)
		}
	}
	mutate(func(tx *store.Tx) error {
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if e != nil {
			return e
		}
		if p == nil || p.Provenance == nil || p.Revision != 1 {
			t.Fatal("missing exact automatic price")
		}
		original = p.ID
		return nil
	})
	// A daily retrieval date alone does not create a new immutable price version.
	snapshot.Checked = checked.Add(24 * time.Hour)
	basis.AsOf = "2026-10-10"
	snapshot.Catalog.Digest = strings.Repeat("b", 64)
	mutate(func(tx *store.Tx) error {
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if p == nil || p.ID != original {
			t.Fatal("unchanged rate duplicated")
		}
		return e
	})
	mutate(func(tx *store.Tx) error {
		if _, e := tx.SetPricingPolicy(m, domain.ManualPricing, 0); e != nil {
			return e
		}
		p, e := tx.PutPricing(m.Key(), 1, domain.NewID(), basis)
		manual = p.ID
		return e
	})
	snapshot.Catalog.References = map[string]tokenprices.Reference{} // successful no-match
	mutate(func(tx *store.Tx) error {
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if p == nil || p.ID != manual {
			t.Fatal("refresh overwrote Manual")
		}
		return e
	})
	mutate(func(tx *store.Tx) error {
		if _, e := tx.SetPricingPolicy(m, domain.AutomaticPricing, 1); e != nil {
			return e
		}
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if p != nil {
			t.Fatal("no-match retained obsolete Automatic basis")
		}
		old, e := tx.Pricing(original)
		if old.ID != original || old.Revision != 1 {
			t.Fatal("past price changed")
		}
		return e
	})
	// Reappearance after no-match uses a new version; the old active revision is
	// not reused, and no matching by aliases or another provider occurs.
	snapshot.Catalog.References["openai\x00claude-exact"] = tokenprices.Reference{Provider: "openai", Model: "claude-exact", Basis: &basis}
	mutate(func(tx *store.Tx) error {
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if p != nil {
			t.Fatal("cross-source alias adopted")
		}
		return e
	})
	snapshot.Catalog.References["anthropic\x00claude-exact"] = tokenprices.Reference{Provider: "anthropic", Model: "claude-exact", Basis: &basis}
	mutate(func(tx *store.Tx) error {
		if e := s.applyReference(tx, m, snapshot); e != nil {
			return e
		}
		p, e := tx.RetainedActivePricing(m.Key())
		if p == nil || p.Revision != 3 || p.ID == original || p.ID == manual {
			t.Fatal("retained revision reused")
		}
		return e
	})
}

func TestAutomaticPricingUsesOnlyReviewedUnchangedPresetNamespaces(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.unmapped-local-provider", nil, func(tx *store.Tx) (any, error) {
		for _, preset := range providers.Presets() {
			if preset.ID == domain.PresetOllama {
				return tx.Put(domain.ProviderKind, domain.NewID(), 0, "", "", preset.Provider)
			}
		}
		return nil, domain.Fail(domain.NotFound, "Missing local preset fixture.", "Preserve the original local preset.")
	})
	if err != nil {
		t.Fatal(err)
	}
	var gemini store.Record
	err = db.Read(ctx, func(tx *store.Tx) error {
		rows, err := tx.List(store.Filter{Kind: domain.ProviderKind, Limit: 100})
		if err != nil {
			return err
		}
		expected := map[domain.ProviderPresetID]string{domain.PresetGemini: "google", domain.PresetVercel: "vercel", domain.PresetOpenAI: "openai", domain.PresetOllama: "", domain.PresetTogetherAI: "", domain.PresetHuggingFace: ""}
		seen := 0
		for _, row := range rows {
			p, err := store.Decode[domain.Provider](row)
			if err != nil {
				return err
			}
			want, checked := expected[*p.PresetID]
			if !checked {
				continue
			}
			seen++
			got, _, err := priceNamespace(tx, domain.ModelIdentity{ProviderID: row.ID, NativeID: "Exact/Native"})
			if err != nil || got != want {
				t.Fatalf("preset %s namespace=%q want=%q err=%v", *p.PresetID, got, want, err)
			}
			if *p.PresetID == domain.PresetGemini {
				gemini = row
			}
		}
		if seen != len(expected) {
			t.Fatalf("missing reviewed preset fixtures: %d", seen)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.changed-pricing-endpoint", nil, func(tx *store.Tx) (any, error) {
		p, err := store.Decode[domain.Provider](gemini)
		if err != nil {
			return nil, err
		}
		p.Endpoint = "https://custom.example/v1"
		if _, err = tx.Put(domain.ProviderKind, gemini.ID, gemini.Revision, "", "", p); err != nil {
			return nil, err
		}
		got, _, err := priceNamespace(tx, domain.ModelIdentity{ProviderID: gemini.ID, NativeID: "Exact/Native"})
		if err != nil || got != "" {
			t.Fatal("edited endpoint adopted managed price namespace", got, err)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
}
