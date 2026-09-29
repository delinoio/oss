package domain

import "testing"

func TestAccountingUnsignedSumsAndKindsStayDistinct(t *testing.T) {
	var totals UsageTotals
	maximum := "18446744073709551615"
	zero := "0"
	totals.AddAccounting(GrokClosedInput, &maximum)
	totals.AddAccounting(GrokClosedInput, &maximum)
	totals.AddAccounting(CodexResponse, nil)
	totals.AddAccounting(CodexResponse, &zero)
	if totals.Responses != 0 || totals.Total.KnownTotal != "" || len(totals.Accounting) != 2 || totals.Accounting[0].KnownTotal != "36893488147419103230" || totals.Accounting[0].MeasuredUnits != 2 || totals.Accounting[1].KnownTotal != "0" || totals.Accounting[1].UnavailableUnits != 1 {
		t.Fatalf("lost exact or distinct native accounting: %+v", totals)
	}
	var other UsageTotals
	other.Merge(totals)
	other.Merge(totals)
	if other.Accounting[0].KnownTotal != "73786976294838206460" || other.Accounting[0].Units != 4 || other.Accounting[1].UnavailableUnits != 2 {
		t.Fatalf("merge changed unit semantics: %+v", other)
	}
}
