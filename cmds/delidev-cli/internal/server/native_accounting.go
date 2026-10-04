// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeMeasure(v domain.NativeAccountingMeasure) *pb.NativeAccountingMeasure {
	return &pb.NativeAccountingMeasure{KnownTotal: v.KnownTotal, MeasuredUnits: v.MeasuredUnits, UnavailableUnits: v.UnavailableUnits}
}
func nativeTotals(v domain.NativeAccountingTotals) *pb.NativeAccountingTotals {
	w := &pb.NativeAccountingTotals{Kind: pb.AccountingUnitKind(v.Kind), Units: v.Units, Input: nativeMeasure(v.Input), CacheRead: nativeMeasure(v.CacheRead), CacheWrite: nativeMeasure(v.CacheWrite), Output: nativeMeasure(v.Output), Thinking: nativeMeasure(v.Thinking), Total: nativeMeasure(v.Total), UnpricedUnits: v.UnpricedUnits}
	for _, c := range v.Currencies {
		w.Currencies = append(w.Currencies, &pb.NativeCurrencyEstimate{Currency: string(c.Currency), KnownAmount: c.KnownAmount, CompleteUnits: c.CompleteUnits, PartialUnits: c.PartialUnits, UnavailableUnits: c.UnavailableUnits})
	}
	return w
}
func nativeGroup(v domain.NativeAccountingGroup) *pb.NativeAccountingGroup {
	return &pb.NativeAccountingGroup{SessionId: string(v.SessionID), ProjectId: string(v.ProjectID), AccountId: string(v.AccountID), ProviderId: string(v.ProviderID), ModelId: string(v.ModelID), Totals: nativeTotals(v.Totals)}
}
func nativeEstimateMeasure(v domain.NativeEstimateMeasure) *pb.NativeEstimateMeasure {
	return &pb.NativeEstimateMeasure{KnownAmount: v.KnownAmount, KnownTokens: v.KnownTokens, PricedUnits: v.PricedUnits, MissingUsageUnits: v.MissingUsageUnits, MissingPriceUnits: v.MissingPriceUnits, NotApplicableUnits: v.NotApplicableUnits}
}
func nativeSummary(v domain.NativeAccountingSummary) *pb.NativeAccountingSummary {
	w := &pb.NativeAccountingSummary{Totals: nativeTotals(v.Totals), Coverage: pb.UsageCoverage_USAGE_COVERAGE_OBSERVED_ROOT_ACCOUNTING_UNITS, ActualCost: pb.UsageCostState_USAGE_COST_STATE_UNAVAILABLE}
	for _, g := range v.Groups {
		w.Groups = append(w.Groups, nativeGroup(g))
	}
	for _, g := range v.Models {
		w.Models = append(w.Models, nativeGroup(g))
	}
	for _, d := range v.Days {
		w.Days = append(w.Days, &pb.NativeAccountingDay{FromUnixMs: d.FromUnixMS, UntilUnixMs: d.UntilUnixMS, Totals: nativeTotals(d.Totals)})
	}
	for _, p := range v.Pricing {
		w.Pricing = append(w.Pricing, &pb.NativeAccountingPricing{Pricing: pricingVersion(&p.Pricing), Totals: nativeTotals(p.Totals), Input: nativeEstimateMeasure(p.Input), CacheRead: nativeEstimateMeasure(p.CacheRead), CacheWrite: nativeEstimateMeasure(p.CacheWrite), Output: nativeEstimateMeasure(p.Output), Reasoning: nativeEstimateMeasure(p.Reasoning)})
	}
	return w
}
