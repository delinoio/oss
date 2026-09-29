package domain

import "regexp"

// A budget gates future execution acceptance using known token-price estimates.
// It neither cancels accepted work nor bounds actual provider billing.
type EstimatedCostBudget struct {
	Currency  Currency `json:"currency"`
	Threshold string   `json:"threshold"`
}

var budgetDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,15})?$`)
var estimateDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,127})(\.[0-9]{1,15})?$`)

func (b EstimatedCostBudget) Validate() error {
	if !currencyPattern.MatchString(string(b.Currency)) || !budgetDecimal.MatchString(b.Threshold) {
		return Fail(InvalidArgument, "Invalid estimated-cost budget.", "Use a three-letter uppercase currency and a nonnegative decimal threshold with at most fifteen fractional digits.")
	}
	return nil
}

type BudgetEvidence struct {
	Currency               Currency `json:"currency"`
	KnownAmount            string   `json:"known_amount"`
	CompleteResponses      uint64   `json:"complete_responses"`
	PartialResponses       uint64   `json:"partial_responses"`
	UnavailableResponses   uint64   `json:"unavailable_responses"`
	CompleteNativeUnits    uint64   `json:"complete_native_units,omitempty"`
	PartialNativeUnits     uint64   `json:"partial_native_units,omitempty"`
	UnavailableNativeUnits uint64   `json:"unavailable_native_units,omitempty"`
}

func (e BudgetEvidence) Validate() error {
	if (e.Currency != "" && !currencyPattern.MatchString(string(e.Currency))) || (e.KnownAmount != "" && !estimateDecimal.MatchString(e.KnownAmount)) || e.CompleteResponses > 1<<63-1 || e.PartialResponses > 1<<63-1 || e.UnavailableResponses > 1<<63-1 {
		return Fail(RecoveryRequired, "The retained session estimate is inconsistent.", "Preserve its original response and pricing evidence for reconciliation.")
	}
	if e.CompleteNativeUnits > 1<<63-1 || e.PartialNativeUnits > 1<<63-1 || e.UnavailableNativeUnits > 1<<63-1 {
		return Fail(RecoveryRequired, "The retained native estimate coverage is inconsistent.", "Preserve original input units and their historical price evidence.")
	}
	known := e.CompleteResponses > 0 || e.PartialResponses > 0 || e.CompleteNativeUnits > 0 || e.PartialNativeUnits > 0
	if known != (e.KnownAmount != "") || (e.Currency == "" && known) {
		return Fail(RecoveryRequired, "The retained session estimate is inconsistent.", "Preserve its original response and pricing evidence for reconciliation.")
	}
	return nil
}
func (e *BudgetEvidence) MergeNative(native BudgetEvidence) error {
	if e.Validate() != nil || native.Validate() != nil || e.Currency != native.Currency {
		return Fail(RecoveryRequired, "Inconsistent native budget evidence.", "Preserve original units and their immutable price bases.")
	}
	e.CompleteNativeUnits = native.CompleteResponses
	e.PartialNativeUnits = native.PartialResponses
	e.UnavailableNativeUnits = native.UnavailableResponses
	if native.KnownAmount != "" {
		e.KnownAmount = addAmount(e.KnownAmount, native.KnownAmount)
	}
	return e.Validate()
}
func (e *BudgetEvidence) Add(value ResponseEstimate) error {
	if e.Currency != value.Currency {
		return Fail(RecoveryRequired, "The estimate currency changed.", "Retain separate currency histories.")
	}
	switch value.Coverage {
	case EstimateComplete:
		e.CompleteResponses++
	case EstimatePartial:
		e.PartialResponses++
	case EstimateUnavailable:
		e.UnavailableResponses++
	default:
		return invalidPricing()
	}
	if value.KnownAmount != "" {
		if !estimateDecimal.MatchString(value.KnownAmount) {
			return invalidPricing()
		}
		e.KnownAmount = addAmount(e.KnownAmount, value.KnownAmount)
	}
	return e.Validate()
}
func (b EstimatedCostBudget) Reached(e BudgetEvidence) (bool, error) {
	if err := b.Validate(); err != nil {
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, err
	}
	if e.Currency != b.Currency || e.KnownAmount == "" {
		return false, nil
	}
	return scaledDecimal(e.KnownAmount, 15).Cmp(scaledDecimal(b.Threshold, 15)) >= 0, nil
}
func BudgetReachedError() error {
	return Fail(BudgetReached, "The session's known estimated-cost subtotal reached its budget.", "Review the retained estimate and change or remove the budget before starting another turn. Queued input and accepted work are retained; this is not a billing ceiling.")
}
