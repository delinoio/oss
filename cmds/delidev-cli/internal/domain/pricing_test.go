package domain

import (
	"math"
	"testing"
)

func priceText(value string) *string { return &value }
func priceFixture() TokenPricing {
	return TokenPricing{Currency: "USD", Source: "Explicit fixture rate card", AsOf: "2026-09-25", InputMode: CachedInputPrice, InputPerMillion: priceText("2.5"), CachedInputPerMillion: priceText("0.25"), OutputPerMillion: priceText("10")}
}
func priceCounts() NativeTokenCounts {
	input, cached, write, output, reasoning, total := int64(1000), int64(800), int64(0), int64(200), int64(100), int64(1200)
	return NativeTokenCounts{Input: &input, Cached: &cached, CacheWrite: &write, Output: &output, Reasoning: &reasoning, Total: &total}
}
func TestEstimateResponseExactNativeSubsetPricing(t *testing.T) {
	basis := priceFixture()
	counts := priceCounts()
	result, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || result.KnownAmount != "0.0027" || result.Coverage != EstimateComplete || result.Input.Amount != "0.0005" || result.CachedInput.Amount != "0.0002" || result.Output.Amount != "0.002" || *result.Output.Tokens != 200 {
		t.Fatalf("wrong subset arithmetic: %+v %v", result, err)
	}
	// Output already includes reasoning; input/output total is never charged.
	*counts.Reasoning = 180
	*counts.Total = 50000
	same, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || same.KnownAmount != result.KnownAmount {
		t.Fatal("overlapping details were charged twice", err)
	}
	basis.InputMode = UniformInputPrice
	basis.CachedInputPerMillion = nil
	uniform, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || uniform.KnownAmount != "0.0045" || uniform.CachedInput.State != ComponentNotApplicable {
		t.Fatal("uniform basis charged cached subset again", err)
	}
}
func TestEstimatePreservesMissingPricesUsageAndUnsupportedWriteBreakdown(t *testing.T) {
	basis := priceFixture()
	counts := priceCounts()
	basis.InputPerMillion = nil
	result, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || result.Coverage != EstimatePartial || result.KnownAmount != "0.0022" || result.Input.State != ComponentMissingPrice || result.Input.Amount != "" {
		t.Fatal("missing price became zero", result, err)
	}
	unavailable, err := EstimateResponse(NewID(), basis, nil)
	if err != nil || unavailable.KnownAmount != "" || unavailable.Coverage != EstimateUnavailable {
		t.Fatal("missing usage became zero", err)
	}
	for _, change := range []string{"unavailable-write", "nonzero-write", "invalid-subset"} {
		counts = priceCounts()
		switch change {
		case "unavailable-write":
			counts.CacheWrite = nil
		case "nonzero-write":
			*counts.CacheWrite = 10
		case "invalid-subset":
			*counts.Cached = 1001
		}
		result, err = EstimateResponse(NewID(), basis, &counts)
		if err != nil || result.KnownAmount != "0.002" || result.Coverage != EstimatePartial || result.Input.State != ComponentUnsupportedBreakdown {
			t.Fatal("unsupported breakdown was invented", change, result, err)
		}
	}
}
func TestEstimateDecimalPrecisionZeroAndIndependentCurrency(t *testing.T) {
	basis := priceFixture()
	basis.InputMode = UniformInputPrice
	basis.CachedInputPerMillion = nil
	basis.OutputPerMillion = priceText("0")
	basis.InputPerMillion = priceText("0.000000001")
	counts := priceCounts()
	*counts.Input = math.MaxInt64
	result, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || result.KnownAmount != "9223.372036854775807" || result.Output.Amount != "0" || result.Coverage != EstimateComplete {
		t.Fatal("lost integer/decimal precision", result, err)
	}
	basis.Currency = "EUR"
	other, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || other.Currency != "EUR" || other.KnownAmount != result.KnownAmount {
		t.Fatal("currency was converted or discarded", err)
	}
	*counts.Input = 0
	zero, err := EstimateResponse(NewID(), basis, &counts)
	if err != nil || zero.KnownAmount != "0" {
		t.Fatal("measured zero became unavailable", err)
	}
}
func TestPricingRejectsAmbiguousOrUnboundedBasis(t *testing.T) {
	for _, bad := range []string{"", "-1", "+1", "1e2", "01", ".1", "1.", "1.0000000001", "1000000000000000000", " 1", "NaN"} {
		basis := priceFixture()
		basis.InputPerMillion = &bad
		if basis.Validate() == nil {
			t.Fatal("accepted invalid rate", bad)
		}
	}
	for _, change := range []string{"currency", "date", "source", "mode", "uniform-cache", "empty"} {
		basis := priceFixture()
		switch change {
		case "currency":
			basis.Currency = "usd"
		case "date":
			basis.AsOf = "2026-02-30"
		case "source":
			basis.Source = ""
		case "mode":
			basis.InputMode = "guess"
		case "uniform-cache":
			basis.InputMode = UniformInputPrice
		case "empty":
			basis.InputPerMillion = nil
			basis.CachedInputPerMillion = nil
			basis.OutputPerMillion = nil
		}
		if basis.Validate() == nil {
			t.Fatal("accepted invalid basis", change)
		}
	}
}
