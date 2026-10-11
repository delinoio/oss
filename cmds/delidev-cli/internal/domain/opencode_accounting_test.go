// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func openCodeAccountingFixture() NativeAccountingUnit {
	provider := NewID()
	return NativeAccountingUnit{Kind: OpenCodeStep, SourceID: NewID(), RequestID: NewID(), InputID: NewID(), SessionID: NewID(), OpenCode: &OpenCodeUsageRecord{ExecutionID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ProviderID: provider, ModelID: (ModelIdentity{ProviderID: provider, NativeID: "fixture"}).Key(), Harness: OpenCode, Version: OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 3, Usage: OpenCodeUsageObservation{Source: OpenCodeStepUsage, NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: OpenCodeTokenCounts{Input: "12", CacheRead: "7", CacheWrite: "3", Output: "8", Reasoning: "2"}, NativeEstimate: "999"}}}
}

func TestOpenCodeAccountingSourceIdentity(t *testing.T) {
	for _, service := range []SubscriptionService{"", SubscriptionOpenCodeGo, SubscriptionClaude, SubscriptionChatGPT, SubscriptionGrok, "foreign"} {
		for _, provider := range []ID{NewID(), "", "invalid"} {
			u := openCodeAccountingFixture()
			u.OpenCode.ProviderID, u.OpenCode.SubscriptionService = provider, service
			u.OpenCode.ModelID = (ModelIdentity{ProviderID: provider, SubscriptionService: service, NativeID: "fixture"}).Key()
			want := service == "" && provider.Validate() == nil || service == SubscriptionOpenCodeGo && provider == ""
			if got := u.Validate() == nil; got != want {
				t.Fatalf("provider=%q service=%q valid=%v want=%v", provider, service, got, want)
			}
			if want {
				a := u.Attribution()
				if a.ProviderID != provider || a.SubscriptionService != service || a.AccountID != u.OpenCode.AccountID || a.ModelID != u.OpenCode.ModelID {
					t.Fatal("original source attribution changed", a)
				}
				u.OpenCode.ModelID = (ModelIdentity{ProviderID: NewID(), NativeID: "fixture"}).Key()
				if u.Validate() == nil {
					t.Fatal("foreign model source accepted")
				}
			}
		}
	}
	claude := nativeUnitFixture()
	claude.Observation.ProviderID = ""
	claude.Observation.SubscriptionService = SubscriptionClaude
	claude.Observation.ModelID = (ModelIdentity{SubscriptionService: SubscriptionClaude, NativeID: "fixture"}).Key()
	if claude.Validate() == nil {
		t.Fatal("OpenCode Go exception expanded to Claude accounting")
	}
}

func TestOpenCodeAccountingPriceRetainsServiceIdentity(t *testing.T) {
	u := openCodeAccountingFixture()
	u.OpenCode.ProviderID = ""
	u.OpenCode.SubscriptionService = SubscriptionOpenCodeGo
	u.OpenCode.ModelID = (ModelIdentity{SubscriptionService: SubscriptionOpenCodeGo, NativeID: "fixture"}).Key()
	one, two := "1", "2"
	p := PricingVersion{ID: NewID(), ModelID: u.OpenCode.ModelID, SubscriptionService: SubscriptionOpenCodeGo, Basis: TokenPricing{Currency: "USD", Source: "Fixture", AsOf: "2026-09-29", InputMode: UniformInputPrice, InputPerMillion: &one, OutputPerMillion: &two}}
	e, err := EstimateNativeInput(u, p)
	if err != nil || e.KnownAmount != "0.000042" || e.Coverage != EstimateComplete || *u.Counts().Input != "22" || *u.Counts().Output != "10" {
		t.Fatal("service source changed exact counts or prices", e, err)
	}
	for _, service := range []SubscriptionService{"", SubscriptionClaude, "foreign"} {
		bad := p
		bad.SubscriptionService = service
		if _, err := EstimateNativeInput(u, bad); err == nil {
			t.Fatal("cross-service historical price accepted", service)
		}
	}
	p.ProviderID = NewID()
	if _, err := EstimateNativeInput(u, p); err == nil {
		t.Fatal("mixed price identity accepted")
	}
}
