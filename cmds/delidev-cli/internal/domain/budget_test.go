package domain

import "testing"

func TestBudgetUsesExactSameCurrencyKnownSubtotal(t *testing.T) {
	budget := EstimatedCostBudget{Currency: "USD", Threshold: "9223.372036854775807"}
	total := BudgetEvidence{Currency: "USD", KnownAmount: "9223.372036854775807", PartialResponses: 1}
	if reached, err := budget.Reached(total); err != nil || !reached {
		t.Fatal("exact inclusive threshold", err)
	}
	total.KnownAmount = "9223.372036854775806"
	if reached, err := budget.Reached(total); err != nil || reached {
		t.Fatal("fraction rounded into gate", err)
	}
	total.Currency = "EUR"
	if reached, err := budget.Reached(total); err != nil || reached {
		t.Fatal("currency converted", err)
	}
	budget.Threshold = "0"
	if reached, err := budget.Reached(BudgetEvidence{Currency: "USD", UnavailableResponses: 5}); err != nil || reached {
		t.Fatal("unknown converted to zero", err)
	}
	if reached, err := budget.Reached(BudgetEvidence{Currency: "USD", KnownAmount: "0", CompleteResponses: 1}); err != nil || !reached {
		t.Fatal("known zero not compared", err)
	}
	for _, invalid := range []string{"-1", "+1", "01", "1e9", "1.1234567890123456", ""} {
		if (EstimatedCostBudget{Currency: "USD", Threshold: invalid}).Validate() == nil {
			t.Fatal("invalid threshold", invalid)
		}
	}
}
