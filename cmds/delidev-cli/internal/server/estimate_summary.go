package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func currencyEstimate(value domain.CurrencyEstimate) *pb.CurrencyEstimate {
	return &pb.CurrencyEstimate{Currency: string(value.Currency), KnownAmount: value.KnownAmount, CompleteResponses: value.CompleteResponses, PartialResponses: value.PartialResponses, UnavailableResponses: value.UnavailableResponses}
}
func estimateTotals(value domain.EstimateTotals) *pb.EstimateTotals {
	result := &pb.EstimateTotals{UnpricedResponses: value.UnpricedResponses}
	for _, value := range value.Currencies {
		result.Currencies = append(result.Currencies, currencyEstimate(value))
	}
	return result
}
func estimateMeasure(value domain.EstimateMeasure) *pb.EstimateMeasure {
	return &pb.EstimateMeasure{KnownAmount: value.KnownAmount, KnownTokens: value.KnownTokens, PricedResponses: value.PricedResponses, MissingUsageResponses: value.MissingUsageResponses, MissingPriceResponses: value.MissingPriceResponses, UnsupportedBreakdownResponses: value.UnsupportedBreakdownResponses, NotApplicableResponses: value.NotApplicableResponses}
}
func pricingUsage(value domain.PricingUsage) *pb.PricingUsage {
	return &pb.PricingUsage{Pricing: pricingVersion(&value.Pricing), Totals: currencyEstimate(value.Totals), Input: estimateMeasure(value.Input), CachedInput: estimateMeasure(value.CachedInput), Output: estimateMeasure(value.Output)}
}
