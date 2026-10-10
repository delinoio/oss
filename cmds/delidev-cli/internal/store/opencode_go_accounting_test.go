// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestOpenCodeAccountingGoServicePublicationPricingAndFiltering(t *testing.T) {
	s, root := openTest(t)
	r, _, input := nativeAccountingFixture(t, s)
	o := domain.OpenCodeUsageRecord{ExecutionID: r.ExecutionID, AccountID: r.AccountID, ConnectionID: r.ConnectionID, SubscriptionService: domain.SubscriptionOpenCodeGo, ModelID: (domain.ModelIdentity{SubscriptionService: domain.SubscriptionOpenCodeGo, NativeID: "fixture"}).Key(), Harness: domain.OpenCode, Version: domain.OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: domain.OpenCodeTokenCounts{Input: "12", CacheRead: "7", CacheWrite: "3", Output: "8", Reasoning: "2"}, NativeEstimate: "999"}}
	var price PricingVersion
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.service-price", nil, func(tx *Tx) (any, error) {
		var err error
		price, err = tx.PutPricing(o.ModelID, 0, domain.NewID(), pricingFixture())
		return price, err
	})
	if err != nil {
		t.Fatal(err)
	}
	retain := func(request, source domain.ID, value domain.OpenCodeUsageRecord) (Result, error) {
		return s.Mutate(context.Background(), request, "fixture.service-step", value, func(tx *Tx) (any, error) {
			if err := tx.PutOpenCodeUsage(source, r.SessionID, r.ProjectID, value); err != nil {
				return nil, err
			}
			return nil, tx.PutOpenCodeAccounting(source, input, r.SessionID, r.ProjectID, value)
		})
	}
	request, source := domain.NewID(), domain.NewID()
	if _, err := retain(request, source, o); err != nil {
		t.Fatal("service step rejected", err)
	}
	if result, err := retain(request, source, o); err != nil || !result.Replayed {
		t.Fatal("exact receipt changed", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	changed := o
	changed.Usage.Counts.Input = "99"
	if _, err := retain(domain.NewID(), domain.NewID(), changed); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("conflicting original step accepted", err)
	}
	for _, service := range []domain.SubscriptionService{"", domain.SubscriptionOpenCodeGo, domain.SubscriptionClaude} {
		f := nativeSelection()
		f.SubscriptionService = service
		summary, err := readUsage(s, f)
		if err != nil {
			t.Fatal(err)
		}
		got := summary.NativeAccounting[1]
		if service == domain.SubscriptionClaude {
			if got.Totals.Units != 0 {
				t.Fatal("foreign service saw original Go unit")
			}
			continue
		}
		if got.Totals.Units != 1 || len(got.Groups) != 1 || len(got.Models) != 1 || len(got.Pricing) != 1 || got.Totals.Input.KnownTotal != "22" || got.Totals.Output.KnownTotal != "10" || got.Totals.Currencies[0].KnownAmount != "0.000155" || summary.Totals.Responses != 0 {
			t.Fatal("counts/pricing/deduplication changed", got)
		}
		g := got.Groups[0]
		m := got.Models[0]
		p := got.Pricing[0].Pricing
		if g.ProviderID != "" || g.SubscriptionService != o.SubscriptionService || g.AccountID != o.AccountID || g.ModelID != o.ModelID || m.ProviderID != "" || m.SubscriptionService != o.SubscriptionService || m.ModelID != o.ModelID || p.ID != price.ID || p.ProviderID != "" || p.SubscriptionService != o.SubscriptionService {
			t.Fatal("original service identity lost", g, m, p)
		}
	}
	for _, filter := range []domain.UsageSelection{
		{ProviderID: r.ProviderID}, {AccountID: o.AccountID}, {ModelID: o.ModelID},
	} {
		f := nativeSelection()
		f.ProviderID, f.AccountID, f.ModelID = filter.ProviderID, filter.AccountID, filter.ModelID
		v, err := readUsage(s, f)
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(1)
		if filter.ProviderID != "" {
			want = 0
		}
		if v.NativeAccounting[1].Totals.Units != want {
			t.Fatal("exact original filter changed", filter)
		}
	}
	for _, invalid := range []struct {
		provider domain.ID
		service  domain.SubscriptionService
	}{{domain.NewID(), domain.SubscriptionOpenCodeGo}, {"", ""}, {"", domain.SubscriptionClaude}} {
		bad := o
		bad.ProviderID = invalid.provider
		bad.SubscriptionService = invalid.service
		bad.ModelID = (domain.ModelIdentity{ProviderID: bad.ProviderID, SubscriptionService: bad.SubscriptionService, NativeID: "fixture"}).Key()
		bad.Usage.NativeID = "prt_01960dcbe1fbABCDEFGHIJKLMN"
		rejectedSource := domain.NewID()
		if _, err := retain(domain.NewID(), rejectedSource, bad); err == nil {
			t.Fatal("invalid identity accepted")
		}
		if err := s.Read(context.Background(), func(tx *Tx) error { _, err := tx.Get(domain.UsageKind, rejectedSource); return err }); domain.SafeError(err).Code != domain.NotFound {
			t.Fatal("invalid accounting failed to roll back usage", err)
		}
	}
	if _, err := s.db.Exec("UPDATE pricing_versions SET subscription_service='claude' WHERE id=?", price.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := readUsage(s, nativeSelection()); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("cross-service retained price corruption accepted", err)
	}
}
