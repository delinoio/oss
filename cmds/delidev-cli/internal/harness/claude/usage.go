package claude

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// NativeUSD preserves the native estimate's decimal spelling. It is neither a
// price lookup nor billable cost, and must not be reconstructed from tokens.
type NativeUSD string

func (n *NativeUSD) UnmarshalJSON(raw []byte) error {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] < '0' || value[0] > '9' || !json.Valid(value) {
		return lifecycleUncertain()
	}
	amount, err := strconv.ParseFloat(string(value), 64)
	if err != nil || amount < 0 || math.IsInf(amount, 0) || math.IsNaN(amount) {
		return lifecycleUncertain()
	}
	*n = NativeUSD(value)
	return nil
}

func (n NativeUSD) MarshalJSON() ([]byte, error) {
	var checked NativeUSD
	if err := checked.UnmarshalJSON([]byte(n)); err != nil {
		return nil, err
	}
	return []byte(checked), nil
}

type UsageIterationKind string
type NativeServiceTier string
type NativeSpeed string
type CreditStatusKind string
type CreditFailureReason string

const (
	MessageIteration    UsageIterationKind = "message"
	CompactionIteration UsageIterationKind = "compaction"
	AdvisorIteration    UsageIterationKind = "advisor_message"
	FallbackIteration   UsageIterationKind = "fallback_message"
	StandardTier        NativeServiceTier  = "standard"
	PriorityTier        NativeServiceTier  = "priority"
	BatchTier           NativeServiceTier  = "batch"
	StandardSpeed       NativeSpeed        = "standard"
	FastSpeed           NativeSpeed        = "fast"
	CreditRedeemed      CreditStatusKind   = "redeemed"
	CreditNotApplied    CreditStatusKind   = "not_applied"
)

type CacheCreationUsage struct {
	OneHour     *int64 `json:"ephemeral_1h_input_tokens"`
	FiveMinutes *int64 `json:"ephemeral_5m_input_tokens"`
}

type OutputTokenDetails struct {
	Thinking *int64 `json:"thinking_tokens"`
}

type ServerToolUsage struct {
	WebSearch *int64 `json:"web_search_requests"`
	WebFetch  *int64 `json:"web_fetch_requests"`
}

// Credit metadata is observation only. It cannot select a fallback model,
// authorize another request or cause automatic token redemption/retry.
type FallbackCreditUsage struct {
	Status FallbackCreditStatus `json:"status"`
}

type FallbackCreditStatus struct {
	Kind           CreditStatusKind    `json:"type"`
	Reason         CreditFailureReason `json:"reason,omitempty"`
	RemoveToRedeem []string            `json:"remove_to_redeem,omitempty"`
}

// ProviderUsage keeps the native Messages counter meanings: input excludes
// cache read/creation, whereas output includes thinking. Nil is unavailable,
// not measured zero. Never substitute transcript size or a synthetic total.
type ProviderUsage struct {
	Input          *int64               `json:"input_tokens"`
	CacheWrite     *int64               `json:"cache_creation_input_tokens"`
	CacheRead      *int64               `json:"cache_read_input_tokens"`
	Output         *int64               `json:"output_tokens"`
	OutputDetail   *OutputTokenDetails  `json:"output_tokens_details"`
	CacheDetail    *CacheCreationUsage  `json:"cache_creation"`
	ServerTools    *ServerToolUsage     `json:"server_tool_use"`
	FallbackCredit *FallbackCreditUsage `json:"fallback_credit"`
	ServiceTier    *NativeServiceTier   `json:"service_tier"`
	InferenceGeo   *string              `json:"inference_geo"`
	Speed          *NativeSpeed         `json:"speed"`
	Iterations     []UsageIteration     `json:"iterations"`
}

type UsageIteration struct {
	Kind        UsageIterationKind  `json:"type"`
	Input       *int64              `json:"input_tokens"`
	CacheWrite  *int64              `json:"cache_creation_input_tokens"`
	CacheRead   *int64              `json:"cache_read_input_tokens"`
	Output      *int64              `json:"output_tokens"`
	CacheDetail *CacheCreationUsage `json:"cache_creation"`
	Model       *string             `json:"model,omitempty"`
}

// NativeModelUsage is the harness's cumulative model ledger, which includes
// calls outside the main loop. Its scope/reset boundary belongs to the owned
// native runtime; repeated snapshots are not independent billable deltas.
type NativeModelUsage struct {
	Input          *int64     `json:"inputTokens"`
	Output         *int64     `json:"outputTokens"`
	CacheRead      *int64     `json:"cacheReadInputTokens"`
	CacheWrite     *int64     `json:"cacheCreationInputTokens"`
	WebSearch      *int64     `json:"webSearchRequests"`
	CostUSD        *NativeUSD `json:"costUSD"`
	ContextWindow  *int64     `json:"contextWindow"`
	MaxOutput      *int64     `json:"maxOutputTokens"`
	CanonicalModel *string    `json:"canonicalModel"`
	Provider       *string    `json:"provider"`
}

type ResultUsage struct {
	// MainLoop is per input turn and excludes subagent/auxiliary model calls.
	MainLoop      *ProviderUsage              `json:"main_loop_turn"`
	Models        map[string]NativeModelUsage `json:"native_cumulative_models"`
	NativeCostUSD *NativeUSD                  `json:"native_cumulative_cost_usd"`
}

func nonnegativeCounts(values ...*int64) bool {
	for _, value := range values {
		if value != nil && *value < 0 {
			return false
		}
	}
	return true
}

func (u *CacheCreationUsage) UnmarshalJSON(raw []byte) error {
	type wire CacheCreationUsage
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.OneHour, value.FiveMinutes) {
		return lifecycleUncertain()
	}
	*u = CacheCreationUsage(value)
	return nil
}

func (u *OutputTokenDetails) UnmarshalJSON(raw []byte) error {
	type wire OutputTokenDetails
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.Thinking) {
		return lifecycleUncertain()
	}
	*u = OutputTokenDetails(value)
	return nil
}

func (u *ServerToolUsage) UnmarshalJSON(raw []byte) error {
	type wire ServerToolUsage
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.WebSearch, value.WebFetch) {
		return lifecycleUncertain()
	}
	*u = ServerToolUsage(value)
	return nil
}

func (u *UsageIteration) UnmarshalJSON(raw []byte) error {
	type wire UsageIteration
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.Input, value.Output, value.CacheWrite, value.CacheRead) {
		return lifecycleUncertain()
	}
	switch value.Kind {
	case CompactionIteration:
		if value.Model != nil {
			return lifecycleUncertain()
		}
	case MessageIteration:
		if value.Model != nil && domain.Text(*value.Model, "native iteration model", 256, true) != nil {
			return lifecycleUncertain()
		}
	case AdvisorIteration, FallbackIteration:
		if value.Model == nil || domain.Text(*value.Model, "native iteration model", 256, true) != nil {
			return lifecycleUncertain()
		}
	default:
		return lifecycleUncertain()
	}
	*u = UsageIteration(value)
	return nil
}

func (u *ProviderUsage) UnmarshalJSON(raw []byte) error {
	type wire ProviderUsage
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.Input, value.Output, value.CacheRead, value.CacheWrite) || len(value.Iterations) > 1024 {
		return lifecycleUncertain()
	}
	if (value.ServiceTier != nil && !slices.Contains([]NativeServiceTier{StandardTier, PriorityTier, BatchTier}, *value.ServiceTier)) || (value.Speed != nil && !slices.Contains([]NativeSpeed{StandardSpeed, FastSpeed}, *value.Speed)) || (value.InferenceGeo != nil && domain.Text(*value.InferenceGeo, "native inference geography", 128, false) != nil) {
		return lifecycleUncertain()
	}
	*u = ProviderUsage(value)
	return nil
}

func (u *FallbackCreditUsage) UnmarshalJSON(raw []byte) error {
	type wire FallbackCreditUsage
	var value wire
	if decodeNativeObject(raw, &value) != nil || (value.Status.Kind != CreditRedeemed && value.Status.Kind != CreditNotApplied) {
		return lifecycleUncertain()
	}
	*u = FallbackCreditUsage(value)
	return nil
}

func (s *FallbackCreditStatus) UnmarshalJSON(raw []byte) error {
	type wire FallbackCreditStatus
	var value wire
	if decodeNativeObject(raw, &value) != nil {
		return lifecycleUncertain()
	}
	switch value.Kind {
	case CreditRedeemed:
		if value.Reason != "" || value.RemoveToRedeem != nil {
			return lifecycleUncertain()
		}
	case CreditNotApplied:
		if !slices.Contains([]CreditFailureReason{"body_mismatch", "continuation_excluded", "continuation_only", "expired", "invalid_target_model", "not_enabled", "reprice_unavailable", "temporarily_unavailable", "variant_fields_present", "wrong_organization", "wrong_platform", "wrong_workspace"}, value.Reason) {
			return lifecycleUncertain()
		}
		if value.Reason == "variant_fields_present" {
			if !uniqueText(value.RemoveToRedeem, 128, 256) {
				return lifecycleUncertain()
			}
		} else if value.RemoveToRedeem != nil {
			return lifecycleUncertain()
		}
	default:
		return lifecycleUncertain()
	}
	*s = FallbackCreditStatus(value)
	return nil
}

func (u *NativeModelUsage) UnmarshalJSON(raw []byte) error {
	type wire NativeModelUsage
	var value wire
	if decodeNativeObject(raw, &value) != nil || !nonnegativeCounts(value.Input, value.Output, value.CacheRead, value.CacheWrite, value.WebSearch) || (value.ContextWindow != nil && *value.ContextWindow <= 0) || (value.MaxOutput != nil && *value.MaxOutput <= 0) {
		return lifecycleUncertain()
	}
	for _, label := range []*string{value.CanonicalModel, value.Provider} {
		if label != nil && domain.Text(*label, "native model usage metadata", 256, true) != nil {
			return lifecycleUncertain()
		}
	}
	*u = NativeModelUsage(value)
	return nil
}

func decodeResultUsage(main, models, cost json.RawMessage) (*ResultUsage, error) {
	var value ResultUsage
	for _, field := range []struct {
		raw    json.RawMessage
		target any
	}{{main, &value.MainLoop}, {models, &value.Models}, {cost, &value.NativeCostUSD}} {
		if len(field.raw) != 0 && domain.Decode(field.raw, field.target) != nil {
			return nil, lifecycleUncertain()
		}
	}
	if len(value.Models) > 256 {
		return nil, lifecycleUncertain()
	}
	for model := range value.Models {
		if domain.Text(model, "native model usage identity", 256, true) != nil {
			return nil, lifecycleUncertain()
		}
	}
	return &value, nil
}
