// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"math/big"
	"reflect"
	"sort"
)

type NativeAccountingKind = AccountingUnitKind

const (
	ClaudeMainLoopInput AccountingUnitKind = 3
	OpenCodeStep        AccountingUnitKind = 4
)

// NativeAccountingUnit is an input unit, never a fabricated provider response.
// Only the original correlated result's main-loop primitives enter this ledger.
type NativeAccountingUnit struct {
	Kind        NativeAccountingKind `json:"kind"`
	SourceID    ID                   `json:"source_id"`
	RequestID   ID                   `json:"request_id"`
	SessionID   ID                   `json:"session_id"`
	ProjectID   ID                   `json:"project_id,omitempty"`
	InputID     ID                   `json:"input_id"`
	Observation ClaudeUsageRecord    `json:"observation,omitzero"`
	OpenCode    *OpenCodeUsageRecord `json:"opencode,omitempty"`
}

func (u NativeAccountingUnit) Validate() error {
	o := u.Attribution()
	if !ModelKeyMatchesSource(o.ModelID, o.ProviderID, o.SubscriptionService) {
		return invalidObservation()
	}
	for _, id := range []ID{u.SourceID, u.RequestID, u.SessionID, u.InputID, o.ExecutionID, o.AccountID, o.ConnectionID, o.ProviderID} {
		if id.Validate() != nil {
			return invalidClaudeUsage()
		}
	}
	if u.ProjectID != "" && u.ProjectID.Validate() != nil {
		return invalidClaudeUsage()
	}
	if u.Kind == OpenCodeStep {
		if u.OpenCode == nil || !reflect.ValueOf(u.Observation).IsZero() || o.Harness != OpenCode || (o.Version != "" && !ValidNativeVersionMetadata(o.Version)) || o.Sequence == 0 || NativeIdentity(o.ThreadID).Validate(OpenCode, NativeThreadIdentity) != nil || NativeIdentity(o.TurnID).Validate(OpenCode, NativeTurnIdentity) != nil || u.OpenCode.Usage.Source != OpenCodeStepUsage || u.OpenCode.Usage.Validate() != nil {
			return invalidObservation()
		}
		return nil
	}
	if u.OpenCode != nil || u.Kind != ClaudeMainLoopInput || o.Harness != ClaudeCode || (o.Version != "" && !ValidNativeVersionMetadata(o.Version)) || o.Sequence == 0 || NativeIdentity(o.ThreadID).Validate(ClaudeCode, NativeThreadIdentity) != nil || NativeIdentity(o.TurnID).Validate(ClaudeCode, NativeTurnIdentity) != nil || o.Usage.Source != ClaudeInputResultUsage || o.Usage.Validate() != nil {
		return invalidClaudeUsage()
	}
	return nil
}

// Input requires all three disjoint input primitives. Missing categories never
// become zero. The native result does not report total tokens.
func (u NativeAccountingUnit) Counts() NativeAccountingCounts {
	c := NativeAccountingCounts{}
	if u.Kind == OpenCodeStep {
		n := u.OpenCode.Usage.Counts
		positive := func(v string) *ClaudeUsageCount {
			if v == "0" {
				return nil
			}
			n := ClaudeUsageCount(v)
			return &n
		}
		c.Uncached, c.CacheRead, c.CacheWrite, c.NonReasoning, c.Thinking = positive(n.Input), positive(n.CacheRead), positive(n.CacheWrite), positive(n.Output), positive(n.Reasoning)
		if c.NonReasoning != nil && c.Thinking != nil {
			a, _ := new(big.Int).SetString(string(*c.NonReasoning), 10)
			b, _ := new(big.Int).SetString(string(*c.Thinking), 10)
			output := ClaudeUsageCount(a.Add(a, b).String())
			c.Output = &output
		}
		c.Total = n.Total
		if c.Uncached != nil && c.CacheRead != nil && c.CacheWrite != nil {
			value := new(big.Int)
			for _, n := range []*ClaudeUsageCount{c.Uncached, c.CacheRead, c.CacheWrite} {
				next, _ := new(big.Int).SetString(string(*n), 10)
				value.Add(value, next)
			}
			total := value.String()
			c.Input = &total
		}
		return c
	}
	m := u.Observation.Usage.Result.MainLoop
	if m == nil {
		return c
	}
	c.Uncached, c.CacheRead, c.CacheWrite, c.Output = m.Input, m.CacheRead, m.CacheWrite, m.Output
	if m.OutputDetail != nil {
		c.Thinking = m.OutputDetail.Thinking
	}
	if c.Uncached != nil && c.CacheRead != nil && c.CacheWrite != nil {
		v := new(big.Int)
		for _, count := range []*ClaudeUsageCount{c.Uncached, c.CacheRead, c.CacheWrite} {
			n, _ := new(big.Int).SetString(string(*count), 10)
			v.Add(v, n)
		}
		s := v.String()
		c.Input = &s
	}
	return c
}

type NativeAccountingCounts struct {
	Input, Total                                                    *string
	Uncached, CacheRead, CacheWrite, Output, Thinking, NonReasoning *ClaudeUsageCount
}

type NativeEstimateComponent struct {
	State  EstimateComponentState `json:"state"`
	Tokens *string                `json:"tokens"`
	Amount string                 `json:"amount"`
}
type NativeEstimate struct {
	PricingID   ID                      `json:"pricing_id,omitempty"`
	Currency    Currency                `json:"currency,omitempty"`
	KnownAmount string                  `json:"known_amount"`
	Coverage    EstimateCoverage        `json:"coverage"`
	Input       NativeEstimateComponent `json:"input"`
	CacheRead   NativeEstimateComponent `json:"cache_read"`
	CacheWrite  NativeEstimateComponent `json:"cache_write"`
	Output      NativeEstimateComponent `json:"output"`
	Reasoning   NativeEstimateComponent `json:"reasoning"`
}

func nativeCount(value *ClaudeUsageCount) *string {
	if value == nil {
		return nil
	}
	s := string(*value)
	return &s
}
func nativePriced(tokens *string, rate *string) NativeEstimateComponent {
	if tokens == nil {
		return NativeEstimateComponent{State: ComponentMissingUsage}
	}
	c := NativeEstimateComponent{State: ComponentMissingPrice, Tokens: tokens}
	if rate != nil {
		n, _ := new(big.Int).SetString(*tokens, 10)
		n.Mul(n, scaledDecimal(*rate, 9))
		c.State, c.Amount = ComponentPriced, decimalUnits(n, 15)
	}
	return c
}
func UnpricedNativeEstimate() NativeEstimate {
	c := NativeEstimateComponent{State: ComponentMissingPrice}
	return NativeEstimate{Coverage: EstimateUnavailable, Input: c, CacheRead: c, CacheWrite: c, Output: c, Reasoning: NativeEstimateComponent{State: ComponentNotApplicable}}
}
func EstimateNativeInput(unit NativeAccountingUnit, price PricingVersion) (NativeEstimate, error) {
	if unit.Validate() != nil || price.ID.Validate() != nil || price.Basis.Validate() != nil || price.ModelID != unit.Attribution().ModelID || price.ProviderID != unit.Attribution().ProviderID {
		return NativeEstimate{}, invalidPricing()
	}
	c := unit.Counts()
	e := NativeEstimate{PricingID: price.ID, Currency: price.Basis.Currency, Coverage: EstimateUnavailable, CacheRead: NativeEstimateComponent{State: ComponentNotApplicable}, CacheWrite: NativeEstimateComponent{State: ComponentNotApplicable}}
	e.Output = nativePriced(nativeCount(c.Output), price.Basis.OutputPerMillion)
	e.Reasoning = NativeEstimateComponent{State: ComponentNotApplicable}
	if unit.Kind == OpenCodeStep {
		e.Output = nativePriced(nativeCount(c.NonReasoning), price.Basis.OutputPerMillion)
		e.Reasoning = nativePriced(nativeCount(c.Thinking), price.Basis.OutputPerMillion)
	}
	if price.Basis.InputMode == UniformInputPrice && unit.Kind == OpenCodeStep {
		e.Input = nativePriced(nativeCount(c.Uncached), price.Basis.InputPerMillion)
		e.CacheRead = nativePriced(nativeCount(c.CacheRead), price.Basis.InputPerMillion)
		e.CacheWrite = nativePriced(nativeCount(c.CacheWrite), price.Basis.InputPerMillion)
	} else if price.Basis.InputMode == UniformInputPrice {
		e.Input = nativePriced(c.Input, price.Basis.InputPerMillion)
	} else {
		e.Input = nativePriced(nativeCount(c.Uncached), price.Basis.InputPerMillion)
		e.CacheRead = nativePriced(nativeCount(c.CacheRead), price.Basis.CachedInputPerMillion)
		// The current price schema has no cache-creation category. Even measured
		// zero remains explicitly unpriced under this split basis.
		e.CacheWrite = nativePriced(nativeCount(c.CacheWrite), nil)
	}
	total := new(big.Int)
	priced, missing := 0, 0
	for _, v := range []NativeEstimateComponent{e.Input, e.CacheRead, e.CacheWrite, e.Output, e.Reasoning} {
		if v.State == ComponentPriced {
			total.Add(total, scaledDecimal(v.Amount, 15))
			priced++
		} else if v.State != ComponentNotApplicable {
			missing++
		}
	}
	if priced > 0 {
		e.KnownAmount = decimalUnits(total, 15)
		e.Coverage = EstimateComplete
		if missing > 0 {
			e.Coverage = EstimatePartial
		}
	}
	return e, nil
}

type NativeAccountingMeasure struct {
	KnownTotal       string `json:"known_total"`
	MeasuredUnits    uint32 `json:"measured_units"`
	UnavailableUnits uint32 `json:"unavailable_units"`
}

func (m *NativeAccountingMeasure) Add(v *string) {
	if v == nil {
		m.UnavailableUnits++
		return
	}
	n, _ := new(big.Int).SetString(*v, 10)
	if m.KnownTotal != "" {
		old, _ := new(big.Int).SetString(m.KnownTotal, 10)
		n.Add(n, old)
	}
	m.KnownTotal = n.String()
	m.MeasuredUnits++
}

type NativeCurrencyEstimate struct {
	Currency         Currency `json:"currency"`
	KnownAmount      string   `json:"known_amount"`
	CompleteUnits    uint32   `json:"complete_units"`
	PartialUnits     uint32   `json:"partial_units"`
	UnavailableUnits uint32   `json:"unavailable_units"`
}
type NativeAccountingTotals struct {
	Kind                                                  NativeAccountingKind `json:"kind"`
	Units                                                 uint32               `json:"units"`
	Input, CacheRead, CacheWrite, Output, Thinking, Total NativeAccountingMeasure
	Currencies                                            []NativeCurrencyEstimate `json:"currencies"`
	UnpricedUnits                                         uint32                   `json:"unpriced_units"`
}

func (t *NativeAccountingTotals) Add(unit NativeAccountingUnit, e NativeEstimate) {
	c := unit.Counts()
	t.Kind = unit.Kind
	t.Units++
	t.Input.Add(c.Input)
	t.CacheRead.Add(nativeCount(c.CacheRead))
	t.CacheWrite.Add(nativeCount(c.CacheWrite))
	t.Output.Add(nativeCount(c.Output))
	t.Thinking.Add(nativeCount(c.Thinking))
	t.Total.Add(c.Total)
	if e.Currency == "" {
		t.UnpricedUnits++
		return
	}
	i := 0
	for i < len(t.Currencies) && t.Currencies[i].Currency != e.Currency {
		i++
	}
	if i == len(t.Currencies) {
		t.Currencies = append(t.Currencies, NativeCurrencyEstimate{Currency: e.Currency})
	}
	v := &t.Currencies[i]
	switch e.Coverage {
	case EstimateComplete:
		v.CompleteUnits++
	case EstimatePartial:
		v.PartialUnits++
	default:
		v.UnavailableUnits++
	}
	if e.KnownAmount != "" {
		v.KnownAmount = addAmount(v.KnownAmount, e.KnownAmount)
	}
	sort.Slice(t.Currencies, func(i, j int) bool { return t.Currencies[i].Currency < t.Currencies[j].Currency })
}

type NativeAccountingGroup struct {
	SessionID, ProjectID, AccountID, ProviderID, ModelID ID
	Totals                                               NativeAccountingTotals
}
type NativeAccountingDay struct {
	FromUnixMS, UntilUnixMS int64
	Totals                  NativeAccountingTotals
}
type NativeAccountingPricing struct {
	Pricing PricingVersion
	Totals  NativeAccountingTotals
	// Original per-category coverage, including the unpriced cache-write unit.
	Input, CacheRead, CacheWrite, Output, Reasoning NativeEstimateMeasure
}
type NativeEstimateMeasure struct {
	KnownAmount, KnownTokens                                              string
	PricedUnits, MissingUsageUnits, MissingPriceUnits, NotApplicableUnits uint32
}

func (m *NativeEstimateMeasure) Add(c NativeEstimateComponent) {
	switch c.State {
	case ComponentPriced:
		m.PricedUnits++
		m.KnownAmount = addAmount(m.KnownAmount, c.Amount)
		n, _ := new(big.Int).SetString(*c.Tokens, 10)
		if m.KnownTokens != "" {
			old, _ := new(big.Int).SetString(m.KnownTokens, 10)
			n.Add(n, old)
		}
		m.KnownTokens = n.String()
	case ComponentMissingUsage:
		m.MissingUsageUnits++
	case ComponentMissingPrice:
		m.MissingPriceUnits++
	case ComponentNotApplicable:
		m.NotApplicableUnits++
	}
}

type NativeAccountingSummary struct {
	Totals  NativeAccountingTotals
	Groups  []NativeAccountingGroup
	Days    []NativeAccountingDay
	Models  []NativeAccountingGroup
	Pricing []NativeAccountingPricing
}

// Attribution projects only immutable source identities; no native counters or
// synthetic Claude observation are created for an OpenCode accounting unit.
func (u NativeAccountingUnit) Attribution() ClaudeUsageRecord {
	if u.OpenCode == nil {
		return u.Observation
	}
	o := u.OpenCode
	return ClaudeUsageRecord{ExecutionID: o.ExecutionID, AccountID: o.AccountID, ConnectionID: o.ConnectionID, ProviderID: o.ProviderID, SubscriptionService: o.SubscriptionService, ModelID: o.ModelID, Harness: o.Harness, Version: o.Version, ThreadID: o.ThreadID, TurnID: o.TurnID, Sequence: o.Sequence}
}
