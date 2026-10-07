// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"math/big"
	"strconv"
)

type AccountingProfile int32

const (
	ResponseOnlyAccounting  AccountingProfile = 0
	NativeUnitsV1Accounting AccountingProfile = 1
)

type AccountingUnitKind int32

const (
	CodexResponse   AccountingUnitKind = 1
	GrokClosedInput AccountingUnitKind = 2
)

// AccountingTotals preserves the source unit instead of calling every native
// input a response. KnownTotal contains only independently supplied totals.
type AccountingTotals struct {
	Kind             AccountingUnitKind `json:"kind"`
	Units            uint32             `json:"units"`
	KnownTotal       string             `json:"known_total"`
	MeasuredUnits    uint32             `json:"measured_units"`
	UnavailableUnits uint32             `json:"unavailable_units"`
}

func (t *UsageTotals) AddAccounting(kind AccountingUnitKind, total *string) {
	for i := range t.Accounting {
		if t.Accounting[i].Kind == kind {
			t.Accounting[i].add(total)
			return
		}
	}
	t.Accounting = append(t.Accounting, AccountingTotals{Kind: kind})
	t.Accounting[len(t.Accounting)-1].add(total)
}

func (t *AccountingTotals) add(total *string) {
	t.Units++
	if total == nil {
		t.UnavailableUnits++
		return
	}
	var sum, value big.Int
	sum.SetString(t.KnownTotal, 10)
	value.SetString(*total, 10)
	sum.Add(&sum, &value)
	t.KnownTotal = sum.String()
	t.MeasuredUnits++
}

func CodexAccountingTotal(counts *NativeTokenCounts) *string {
	if counts == nil || counts.Total == nil {
		return nil
	}
	value := strconv.FormatInt(*counts.Total, 10)
	return &value
}

// GrokAccountingRecord is retained only with the accepted original completion
// receipt. Response dimensions remain in SourceUsageID and never price this unit.
type GrokAccountingRecord struct {
	Kind           AccountingUnitKind  `json:"kind"`
	SourceReceipt  ID                  `json:"source_receipt"`
	SourceUsageID  ID                  `json:"source_usage_id"`
	JobID          ID                  `json:"job_id"`
	SessionID      ID                  `json:"session_id"`
	ProjectID      ID                  `json:"project_id,omitempty"`
	ExecutionID    ID                  `json:"execution_id"`
	InputID        ID                  `json:"input_id"`
	InputRequestID ID                  `json:"input_request_id"`
	AccountID      ID                  `json:"account_id"`
	ConnectionID   ID                  `json:"connection_id"`
	ProviderID     ID                  `json:"provider_id"`
	ModelID        ID                  `json:"model_id"`
	Version        string              `json:"native_version"`
	Terminal       GrokTextTerminal    `json:"terminal"`
	Completion     ExecutionCompletion `json:"completion"`
}

func (r GrokAccountingRecord) Validate() error {
	for _, id := range []ID{r.SourceReceipt, r.SourceUsageID, r.JobID, r.SessionID, r.ExecutionID, r.InputID, r.InputRequestID, r.AccountID, r.ConnectionID, r.ProviderID, r.ModelID} {
		if id.Validate() != nil {
			return invalidGrokContent()
		}
	}
	if r.Kind != GrokClosedInput || (r.ProjectID != "" && r.ProjectID.Validate() != nil) || !GrokRetainedVersion(r.Version) || r.Terminal.User == nil || r.Terminal.Validate(string(r.Completion.NativeThreadID)) != nil || r.Completion.ValidateForHarness(GrokBuild) != nil || r.Completion.Version != 1 || r.Completion.ExecutionID != r.ExecutionID || r.Completion.InputID != r.InputID || r.Completion.Outcome != ExecutionSucceeded {
		return invalidGrokContent()
	}
	return nil
}
