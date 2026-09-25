package domain

import (
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Currency is an explicit denomination, never a conversion instruction. User
// pricing supplies its currency; the server never combines different values.
type Currency string

type InputPricingMode string

const (
	UniformInputPrice InputPricingMode = "uniform-input"
	CachedInputPrice  InputPricingMode = "cached-input-discount"
)

type TokenPricing struct {
	Currency              Currency         `json:"currency"`
	Source                string           `json:"source"`
	AsOf                  string           `json:"as_of"`
	InputMode             InputPricingMode `json:"input_mode"`
	InputPerMillion       *string          `json:"input_per_million"`
	CachedInputPerMillion *string          `json:"cached_input_per_million"`
	OutputPerMillion      *string          `json:"output_per_million"`
	Exclusions            []string         `json:"exclusions"`
}

var priceDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,9})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func (p TokenPricing) Validate() error {
	if !currencyPattern.MatchString(string(p.Currency)) || Text(p.Source, "pricing source", 2048, true) != nil || (p.InputMode != UniformInputPrice && p.InputMode != CachedInputPrice) || len(p.Exclusions) > 16 {
		return invalidPricing()
	}
	date, err := time.Parse("2006-01-02", p.AsOf)
	if err != nil || date.Format("2006-01-02") != p.AsOf || date.Year() < 1970 {
		return invalidPricing()
	}
	if p.InputPerMillion == nil && p.CachedInputPerMillion == nil && p.OutputPerMillion == nil {
		return invalidPricing()
	}
	if p.InputMode == UniformInputPrice && p.CachedInputPerMillion != nil {
		return invalidPricing()
	}
	for _, value := range []*string{p.InputPerMillion, p.CachedInputPerMillion, p.OutputPerMillion} {
		if value != nil && !priceDecimal.MatchString(*value) {
			return invalidPricing()
		}
	}
	for _, value := range p.Exclusions {
		if Text(value, "pricing exclusion", 512, true) != nil {
			return invalidPricing()
		}
	}
	return nil
}
func invalidPricing() error {
	return Fail(InvalidArgument, "Invalid token-pricing basis.", "Provide a currency, source, as-of date, explicit input mode and nonnegative decimal rates with at most nine fractional digits; keep missing rates unavailable.")
}

type EstimateComponentState string

const (
	ComponentPriced               EstimateComponentState = "priced"
	ComponentMissingUsage         EstimateComponentState = "missing-usage"
	ComponentMissingPrice         EstimateComponentState = "missing-price"
	ComponentNotApplicable        EstimateComponentState = "not-applicable"
	ComponentUnsupportedBreakdown EstimateComponentState = "unsupported-breakdown"
)

type EstimateComponent struct {
	State  EstimateComponentState `json:"state"`
	Tokens *int64                 `json:"tokens"`
	Amount string                 `json:"amount"`
}

type EstimateCoverage string

const (
	EstimateComplete    EstimateCoverage = "complete-token-basis"
	EstimatePartial     EstimateCoverage = "partial-token-basis"
	EstimateUnavailable EstimateCoverage = "unavailable"
)

// ResponseEstimate is only a token-price calculation. Overall telemetry may
// remain incomplete even when all categories in this response's basis are priced.
type ResponseEstimate struct {
	PricingID   ID                `json:"pricing_id,omitempty"`
	Currency    Currency          `json:"currency,omitempty"`
	KnownAmount string            `json:"known_amount"`
	Coverage    EstimateCoverage  `json:"coverage"`
	Input       EstimateComponent `json:"input"`
	CachedInput EstimateComponent `json:"cached_input"`
	Output      EstimateComponent `json:"output"`
}

// Token rates have nine fractional digits per million, so amounts have at most
// fifteen. Integer arithmetic retains exact values without rounding each event.
func scaledDecimal(value string, scale int) *big.Int {
	parts := strings.Split(value, ".")
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := parts[0] + fraction + strings.Repeat("0", scale-len(fraction))
	result, _ := new(big.Int).SetString(digits, 10)
	return result
}
func decimalUnits(value *big.Int, scale int) string {
	digits := value.String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale+1-len(digits)) + digits
	}
	result := strings.TrimRight(digits[:len(digits)-scale]+"."+digits[len(digits)-scale:], "0")
	return strings.TrimSuffix(result, ".")
}
func pricedComponent(tokens *int64, rate *string) EstimateComponent {
	if tokens == nil {
		return EstimateComponent{State: ComponentMissingUsage}
	}
	count := *tokens
	value := EstimateComponent{State: ComponentMissingPrice, Tokens: &count}
	if rate == nil {
		return value
	}
	amount := scaledDecimal(*rate, 9)
	amount.Mul(amount, big.NewInt(count))
	value.State, value.Amount = ComponentPriced, decimalUnits(amount, 15)
	return value
}

func EstimateResponse(id ID, price TokenPricing, counts *NativeTokenCounts) (ResponseEstimate, error) {
	result := ResponseEstimate{PricingID: id, Currency: price.Currency, Input: EstimateComponent{State: ComponentMissingUsage}, CachedInput: EstimateComponent{State: ComponentNotApplicable}, Output: EstimateComponent{State: ComponentMissingUsage}, Coverage: EstimateUnavailable}
	if id.Validate() != nil || price.Validate() != nil {
		return result, invalidPricing()
	}
	if counts != nil && (NativeTokenUsage{Total: *counts, Last: *counts}).Validate() != nil {
		return result, invalidObservation()
	}
	if counts == nil {
		counts = &NativeTokenCounts{}
	}
	result.Output = pricedComponent(counts.Output, price.OutputPerMillion)
	if price.InputMode == UniformInputPrice {
		// A uniform rate explicitly prices the native reported input total. Cache
		// subsets never acquire an additional charge under this selected basis.
		result.Input = pricedComponent(counts.Input, price.InputPerMillion)
	} else {
		result.CachedInput = EstimateComponent{State: ComponentMissingUsage}
		if counts.Input != nil && counts.Cached != nil {
			// The pinned Responses profile establishes cache-read as an input subset.
			// It does not establish nonzero cache-write pricing semantics. Retain only
			// the output subtotal when write usage is missing/nonzero or subsets differ.
			if counts.CacheWrite == nil || *counts.CacheWrite != 0 || *counts.Cached > *counts.Input {
				result.Input.State, result.CachedInput.State = ComponentUnsupportedBreakdown, ComponentUnsupportedBreakdown
			} else {
				uncached := *counts.Input - *counts.Cached
				result.Input = pricedComponent(&uncached, price.InputPerMillion)
				result.CachedInput = pricedComponent(counts.Cached, price.CachedInputPerMillion)
			}
		}
	}
	total := new(big.Int)
	priced, missing := 0, 0
	for _, component := range []EstimateComponent{result.Input, result.CachedInput, result.Output} {
		if component.State == ComponentPriced {
			total.Add(total, scaledDecimal(component.Amount, 15))
			priced++
		} else if component.State != ComponentNotApplicable {
			missing++
		}
	}
	if priced > 0 {
		result.KnownAmount = decimalUnits(total, 15)
		result.Coverage = EstimateComplete
		if missing > 0 {
			result.Coverage = EstimatePartial
		}
	}
	return result, nil
}
