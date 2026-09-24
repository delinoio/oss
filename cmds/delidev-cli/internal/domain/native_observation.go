package domain

// NativeTokenCounts preserves the harness's own counter meanings. Cached input
// and reasoning output are breakdowns, not additional tokens to add to Total.
// Missing counters remain unavailable; they must never silently become zero.
type NativeTokenCounts struct {
	Input      *int64 `json:"input"`
	Cached     *int64 `json:"cached_input"`
	CacheWrite *int64 `json:"cache_write_input"`
	Output     *int64 `json:"output"`
	Reasoning  *int64 `json:"reasoning_output"`
	Total      *int64 `json:"total"`
}

type NativeTokenUsage struct {
	Total         NativeTokenCounts `json:"cumulative"`
	Last          NativeTokenCounts `json:"last_request"`
	ContextWindow *int64            `json:"context_window"`
}

func (u NativeTokenUsage) Validate() error {
	for _, counts := range []NativeTokenCounts{u.Total, u.Last} {
		for _, count := range []*int64{counts.Input, counts.Cached, counts.Output, counts.Reasoning, counts.Total} {
			if count == nil || *count < 0 {
				return invalidObservation()
			}
		}
		if counts.CacheWrite != nil && *counts.CacheWrite < 0 {
			return invalidObservation()
		}
	}
	if u.ContextWindow != nil && *u.ContextWindow <= 0 {
		return invalidObservation()
	}
	return nil
}

type NativeNotice string

const (
	NativeWarning       NativeNotice = "native-warning"
	NativeConfigWarning NativeNotice = "native-config-warning"
)

// ExecutionUsageObservation is an immutable observation, not billable usage.
// Codex may reset counters or fill context totals after a limit failure; later
// aggregation must reconcile native history instead of summing observations.
type ExecutionUsageObservation struct {
	ExecutionID  ID               `json:"execution_id"`
	AccountID    ID               `json:"account_id"`
	ConnectionID ID               `json:"connection_id"`
	ProviderID   ID               `json:"provider_id"`
	ModelID      ID               `json:"model_id"`
	Harness      Harness          `json:"harness"`
	Version      string           `json:"native_version"`
	ThreadID     string           `json:"native_thread_id"`
	TurnID       string           `json:"native_turn_id"`
	Sequence     uint64           `json:"sequence"`
	Usage        NativeTokenUsage `json:"observation"`
}

func invalidObservation() *Error {
	return Fail(InvalidArgument, "Invalid native observation.", "Retain only bounded typed harness observations; unavailable values are not zero.")
}
