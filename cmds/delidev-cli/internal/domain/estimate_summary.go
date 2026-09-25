package domain

import (
	"math/big"
	"sort"
	"time"
)

type PricingVersion struct {
	ID         ID           `json:"id"`
	ModelID    ID           `json:"model_id"`
	ProviderID ID           `json:"provider_id"`
	Revision   uint64       `json:"revision"`
	CreatedAt  time.Time    `json:"created_at"`
	Basis      TokenPricing `json:"basis"`
}

type CurrencyEstimate struct {
	Currency             Currency `json:"currency"`
	KnownAmount          string   `json:"known_amount"`
	CompleteResponses    uint32   `json:"complete_responses"`
	PartialResponses     uint32   `json:"partial_responses"`
	UnavailableResponses uint32   `json:"unavailable_responses"`
}

func (t *CurrencyEstimate) Add(value ResponseEstimate) {
	switch value.Coverage {
	case EstimateComplete:
		t.CompleteResponses++
	case EstimatePartial:
		t.PartialResponses++
	default:
		t.UnavailableResponses++
	}
	if value.KnownAmount != "" {
		t.KnownAmount = addAmount(t.KnownAmount, value.KnownAmount)
	}
}
func addAmount(current, amount string) string {
	sum := new(big.Int)
	if current != "" {
		sum = scaledDecimal(current, 15)
	}
	sum.Add(sum, scaledDecimal(amount, 15))
	return decimalUnits(sum, 15)
}

type EstimateTotals struct {
	Currencies        []CurrencyEstimate `json:"currencies"`
	UnpricedResponses uint32             `json:"unpriced_responses"`
}

func (t *EstimateTotals) Add(value ResponseEstimate) {
	if value.Currency == "" {
		t.UnpricedResponses++
		return
	}
	for i := range t.Currencies {
		if t.Currencies[i].Currency == value.Currency {
			t.Currencies[i].Add(value)
			return
		}
	}
	next := CurrencyEstimate{Currency: value.Currency}
	next.Add(value)
	t.Currencies = append(t.Currencies, next)
	sort.Slice(t.Currencies, func(i, j int) bool { return t.Currencies[i].Currency < t.Currencies[j].Currency })
}

// Component totals describe only the retained pricing basis. Overlapping native
// totals and reasoning subsets are never additional priced categories.
type EstimateMeasure struct {
	KnownAmount                   string `json:"known_amount"`
	KnownTokens                   string `json:"known_tokens"`
	PricedResponses               uint32 `json:"priced_responses"`
	MissingUsageResponses         uint32 `json:"missing_usage_responses"`
	MissingPriceResponses         uint32 `json:"missing_price_responses"`
	UnsupportedBreakdownResponses uint32 `json:"unsupported_breakdown_responses"`
	NotApplicableResponses        uint32 `json:"not_applicable_responses"`
}

func (m *EstimateMeasure) Add(value EstimateComponent) {
	switch value.State {
	case ComponentPriced:
		m.PricedResponses++
		m.KnownAmount = addAmount(m.KnownAmount, value.Amount)
		var count big.Int
		if m.KnownTokens != "" {
			count.SetString(m.KnownTokens, 10)
		}
		count.Add(&count, big.NewInt(*value.Tokens))
		m.KnownTokens = count.String()
	case ComponentMissingUsage:
		m.MissingUsageResponses++
	case ComponentMissingPrice:
		m.MissingPriceResponses++
	case ComponentUnsupportedBreakdown:
		m.UnsupportedBreakdownResponses++
	case ComponentNotApplicable:
		m.NotApplicableResponses++
	}
}

type PricingUsage struct {
	Pricing     PricingVersion   `json:"pricing"`
	Totals      CurrencyEstimate `json:"totals"`
	Input       EstimateMeasure  `json:"input"`
	CachedInput EstimateMeasure  `json:"cached_input"`
	Output      EstimateMeasure  `json:"output"`
}

func (p *PricingUsage) Add(value ResponseEstimate) {
	p.Totals.Currency = value.Currency
	p.Totals.Add(value)
	p.Input.Add(value.Input)
	p.CachedInput.Add(value.CachedInput)
	p.Output.Add(value.Output)
}
