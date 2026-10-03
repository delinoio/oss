// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func nativeUnitFixture() NativeAccountingUnit {
	input, read, write, zero := ClaudeUsageCount("13"), ClaudeUsageCount("7"), ClaudeUsageCount("5"), ClaudeUsageCount("0")
	return NativeAccountingUnit{Kind: ClaudeMainLoopInput, SourceID: NewID(), RequestID: NewID(), InputID: NewID(), SessionID: NewID(), Observation: ClaudeUsageRecord{ExecutionID: NewID(), AccountID: NewID(), ConnectionID: NewID(), ProviderID: NewID(), ModelID: NewID(), Harness: ClaudeCode, Version: ClaudeProtocolVersion, ThreadID: string(NewID()), TurnID: string(NewID()), Sequence: 3, Usage: ClaudeUsageObservation{Source: ClaudeInputResultUsage, NativeEventID: string(NewID()), Result: &ClaudeResultUsage{MainLoop: &ClaudeProviderUsage{Input: &input, CacheRead: &read, CacheWrite: &write, Output: &zero, OutputDetail: &ClaudeOutputTokenDetails{Thinking: &zero}}}}}}
}
func TestNativeInputAccountingExactScopeAndPricing(t *testing.T) {
	u := nativeUnitFixture()
	large := ClaudeUsageCount("9007199254740993")
	cost := ClaudeNativeUSD("999")
	u.Observation.Usage.Result.Models = map[string]ClaudeNativeModelUsage{"other": {Input: &large}}
	u.Observation.Usage.Result.NativeCostUSD = &cost
	input, output, read := "1", "0", "0.5"
	p := PricingVersion{ID: NewID(), ModelID: u.Observation.ModelID, ProviderID: u.Observation.ProviderID, Basis: TokenPricing{Currency: "USD", Source: "Fixture", AsOf: "2026-09-29", InputMode: UniformInputPrice, InputPerMillion: &input, OutputPerMillion: &output}}
	for _, split := range []bool{false, true} {
		p.Basis.InputMode = UniformInputPrice
		p.Basis.CachedInputPerMillion = nil
		want := "0.000025"
		coverage := EstimateComplete
		if split {
			p.Basis.InputMode = CachedInputPrice
			p.Basis.CachedInputPerMillion = &read
			want = "0.0000165"
			coverage = EstimatePartial
		}
		e, err := EstimateNativeInput(u, p)
		if err != nil || e.KnownAmount != want || e.Coverage != coverage {
			t.Fatalf("split=%v estimate=%+v error=%v", split, e, err)
		}
		if split && (e.CacheWrite.State != ComponentMissingPrice || *e.CacheWrite.Tokens != "5") {
			t.Fatal("cache write silently priced", e)
		}
		var totals NativeAccountingTotals
		totals.Add(u, e)
		if totals.Units != 1 || totals.Input.KnownTotal != "25" || totals.Output.KnownTotal != "0" || totals.Thinking.KnownTotal != "0" || totals.Total.KnownTotal != "" || totals.Total.UnavailableUnits != 1 {
			t.Fatal("overlapping or fabricated totals", totals)
		}
	}
}
func TestNativeInputAccountingNullZeroAndLargeCounters(t *testing.T) {
	u := nativeUnitFixture()
	m := u.Observation.Usage.Result.MainLoop
	for _, missing := range []string{"input", "read", "write", "output", "main"} {
		t.Run(missing, func(t *testing.T) {
			v := u
			copy := *m
			result := *u.Observation.Usage.Result
			result.MainLoop = &copy
			v.Observation.Usage.Result = &result
			switch missing {
			case "input":
				copy.Input = nil
			case "read":
				copy.CacheRead = nil
			case "write":
				copy.CacheWrite = nil
			case "output":
				copy.Output = nil
			case "main":
				result.MainLoop = nil
			}
			if v.Validate() != nil {
				t.Fatal("valid missing usage rejected")
			}
			c := v.Counts()
			if missing != "output" && c.Input != nil {
				t.Fatal("missing input category became zero")
			}
			if missing == "output" && c.Output != nil {
				t.Fatal("missing output became zero")
			}
		})
	}
	zero, large, max := ClaudeUsageCount("0"), ClaudeUsageCount("9007199254740993"), ClaudeUsageCount("9223372036854775807")
	m.Input = &large
	m.CacheRead = &zero
	m.CacheWrite = &zero
	if *u.Counts().Input != "9007199254740993" {
		t.Fatal("precision lost")
	}
	m.Input = &max
	m.CacheRead = &max
	m.CacheWrite = &max
	if *u.Counts().Input != "27670116110564327421" {
		t.Fatal("signed integer sum overflow")
	}
	rate := "1"
	p := PricingVersion{ID: NewID(), ModelID: u.Observation.ModelID, ProviderID: u.Observation.ProviderID, Basis: TokenPricing{Currency: "USD", Source: "Fixture", AsOf: "2026-09-29", InputMode: UniformInputPrice, InputPerMillion: &rate}}
	e, err := EstimateNativeInput(u, p)
	if err != nil || e.KnownAmount != "27670116110564.327421" {
		t.Fatal("large price lost precision", e, err)
	}
}

func TestOpenCodeAccountingPricesDisjointStepsWithUnavailableZeros(t *testing.T) {
	claude := nativeUnitFixture()
	a := claude.Attribution()
	total := "9"
	unit := NativeAccountingUnit{Kind: OpenCodeStep, SourceID: NewID(), RequestID: NewID(), SessionID: claude.SessionID, InputID: claude.InputID, OpenCode: &OpenCodeUsageRecord{ExecutionID: a.ExecutionID, AccountID: a.AccountID, ConnectionID: a.ConnectionID, ProviderID: a.ProviderID, ModelID: a.ModelID, Harness: OpenCode, Version: OpenCodeProtocolVersion, ThreadID: "ses_01960dcbe1faABCDEFGHIJKLMN", TurnID: "msg_01960dcbe1faABCDEFGHIJKLMN", Sequence: 2, Usage: OpenCodeUsageObservation{Source: OpenCodeStepUsage, NativeID: "prt_01960dcbe1faABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1faABCDEFGHIJKLMN", Counts: OpenCodeTokenCounts{Input: "12", CacheRead: "7", CacheWrite: "3", Output: "8", Reasoning: "2"}, NativeEstimate: "999"}}}
	input, output := "1", "2"
	price := PricingVersion{ID: NewID(), ModelID: a.ModelID, ProviderID: a.ProviderID, Basis: TokenPricing{Currency: "USD", Source: "Fixture", AsOf: "2026-09-29", InputMode: UniformInputPrice, InputPerMillion: &input, OutputPerMillion: &output}}
	var totals NativeAccountingTotals
	first, err := EstimateNativeInput(unit, price)
	if err != nil || first.KnownAmount != "0.000042" || first.Coverage != EstimateComplete || *unit.Counts().Input != "22" || *unit.Counts().Output != "10" || unit.Counts().Total != nil {
		t.Fatal("first step categories overlap or lose native provenance", first, err)
	}
	totals.Add(unit, first)
	unit.OpenCode.Usage.NativeID = "prt_01960dcbe1fbABCDEFGHIJKLMN"
	unit.OpenCode.Usage.Counts = OpenCodeTokenCounts{Input: "5", Output: "4", CacheRead: "0", CacheWrite: "0", Reasoning: "0", Total: &total}
	second, err := EstimateNativeInput(unit, price)
	if err != nil || second.KnownAmount != "0.000013" || second.Coverage != EstimatePartial || unit.Counts().Input != nil || unit.Counts().Thinking != nil {
		t.Fatal("coerced zero became measured evidence", second, err)
	}
	totals.Add(unit, second)
	if totals.Units != 2 || totals.Currencies[0].KnownAmount != "0.000055" || totals.Currencies[0].PartialUnits != 1 || totals.Input.KnownTotal != "22" || totals.Input.UnavailableUnits != 1 || totals.Output.KnownTotal != "10" || totals.Output.UnavailableUnits != 1 || totals.Total.KnownTotal != "9" || totals.Total.UnavailableUnits != 1 {
		t.Fatal("two steps lost incomplete coverage", totals)
	}
	total = "0"
	if unit.Counts().Total == nil || *unit.Counts().Total != "0" {
		t.Fatal("explicit native zero total became unavailable")
	}
	unit.OpenCode.Usage.Source = OpenCodeMessageUsage
	unit.OpenCode.Usage.NativeID = "msg_01960dcbe1faABCDEFGHIJKLMN"
	if unit.Validate() == nil {
		t.Fatal("assistant summary fabricated a step")
	}
}
