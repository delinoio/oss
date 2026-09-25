package domain

import (
	"math/big"
	"time"
)

const UsageWindowLimit = 366 * 24 * time.Hour

type UsageSelection struct {
	From        time.Time
	Until       time.Time
	SessionID   ID
	ProjectID   ID
	AccountID   ID
	ProviderID  ID
	ModelID     ID
	GeneralChat bool
}

func (f UsageSelection) Validate() error {
	if f.From.UnixMilli() <= 0 || f.Until.UnixMilli() > 253402300799999 || !f.Until.After(f.From) || f.Until.Sub(f.From) > UsageWindowLimit || (f.GeneralChat && f.ProjectID != "") {
		return Fail(InvalidArgument, "Invalid usage time range or project selection.", "Select a positive half-open time range of at most 366 days, and either a project or General Chat.")
	}
	for _, id := range []ID{f.SessionID, f.ProjectID, f.AccountID, f.ProviderID, f.ModelID} {
		if id != "" && id.Validate() != nil {
			return invalidObservation()
		}
	}
	return nil
}

// Empty KnownTotal means no measurement, while "0" is a measured zero. Decimal
// strings preserve exact sums beyond JavaScript and signed-64-bit integer limits.
type UsageMeasure struct {
	KnownTotal           string `json:"known_total"`
	MeasuredResponses    uint32 `json:"measured_responses"`
	UnavailableResponses uint32 `json:"unavailable_responses"`
}

func (m *UsageMeasure) add(value *int64) {
	if value == nil {
		m.UnavailableResponses++
		return
	}
	var sum big.Int
	if m.KnownTotal != "" {
		sum.SetString(m.KnownTotal, 10)
	}
	sum.Add(&sum, big.NewInt(*value))
	m.KnownTotal = sum.String()
	m.MeasuredResponses++
}

type UsageTotals struct {
	Responses       uint32       `json:"responses"`
	Input           UsageMeasure `json:"input"`
	CachedInput     UsageMeasure `json:"cached_input"`
	CacheWriteInput UsageMeasure `json:"cache_write_input"`
	Output          UsageMeasure `json:"output"`
	ReasoningOutput UsageMeasure `json:"reasoning_output"`
	Total           UsageMeasure `json:"total"`
}

func (t *UsageTotals) Add(counts *NativeTokenCounts) {
	t.Responses++
	if counts == nil {
		counts = &NativeTokenCounts{}
	}
	t.Input.add(counts.Input)
	t.CachedInput.add(counts.Cached)
	t.CacheWriteInput.add(counts.CacheWrite)
	t.Output.add(counts.Output)
	t.ReasoningOutput.add(counts.Reasoning)
	t.Total.add(counts.Total)
}

type UsageGroup struct {
	SessionID  ID          `json:"session_id"`
	ProjectID  ID          `json:"project_id,omitempty"`
	AccountID  ID          `json:"account_id"`
	ProviderID ID          `json:"provider_id"`
	ModelID    ID          `json:"model_id"`
	Totals     UsageTotals `json:"totals"`
}

type UsageSummary struct {
	Totals                            UsageTotals  `json:"totals"`
	Groups                            []UsageGroup `json:"groups"`
	AcceptedExecutionsWithoutResponse uint32       `json:"accepted_executions_without_response"`
}
