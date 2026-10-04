package domain

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ClaudeNativeUSD preserves the native estimate's decimal spelling. It is neither a
// price lookup nor billable cost, and must not be reconstructed from tokens.
type ClaudeNativeUSD string

func (n *ClaudeNativeUSD) UnmarshalJSON(raw []byte) error {
	var value string
	if json.Unmarshal(raw, &value) != nil || !NativeNonnegativeDecimal(value) || strings.HasPrefix(value, "-") {
		return invalidClaudeUsage()
	}
	*n = ClaudeNativeUSD(value)
	return nil
}

// Counts cross the generic Resource JSON boundary as exact decimal strings.
// Missing/null remains unavailable; measured zero remains the string "0".
type ClaudeUsageCount string

func (n *ClaudeUsageCount) UnmarshalJSON(raw []byte) error {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return invalidClaudeUsage()
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != value {
		return invalidClaudeUsage()
	}
	*n = ClaudeUsageCount(value)
	return nil
}

type ClaudeUsageIterationKind string
type ClaudeNativeServiceTier string
type ClaudeNativeSpeed string
type ClaudeCreditStatusKind string
type ClaudeCreditFailureReason string

const (
	ClaudeMessageIteration    ClaudeUsageIterationKind = "message"
	ClaudeCompactionIteration ClaudeUsageIterationKind = "compaction"
	ClaudeAdvisorIteration    ClaudeUsageIterationKind = "advisor_message"
	ClaudeFallbackIteration   ClaudeUsageIterationKind = "fallback_message"
	ClaudeStandardTier        ClaudeNativeServiceTier  = "standard"
	ClaudePriorityTier        ClaudeNativeServiceTier  = "priority"
	ClaudeBatchTier           ClaudeNativeServiceTier  = "batch"
	ClaudeStandardSpeed       ClaudeNativeSpeed        = "standard"
	ClaudeFastSpeed           ClaudeNativeSpeed        = "fast"
	ClaudeCreditRedeemed      ClaudeCreditStatusKind   = "redeemed"
	ClaudeCreditNotApplied    ClaudeCreditStatusKind   = "not_applied"
)

type ClaudeCacheCreationUsage struct {
	OneHour     *ClaudeUsageCount `json:"ephemeral_1h_input_tokens"`
	FiveMinutes *ClaudeUsageCount `json:"ephemeral_5m_input_tokens"`
}

type ClaudeOutputTokenDetails struct {
	Thinking *ClaudeUsageCount `json:"thinking_tokens"`
}

type ClaudeServerToolUsage struct {
	WebSearch *ClaudeUsageCount `json:"web_search_requests"`
	WebFetch  *ClaudeUsageCount `json:"web_fetch_requests"`
}

// Credit metadata is observation only. It cannot select a fallback model,
// authorize another request or cause automatic token redemption/retry.
type ClaudeFallbackCreditUsage struct {
	Status ClaudeFallbackCreditStatus `json:"status"`
}

type ClaudeFallbackCreditStatus struct {
	Kind           ClaudeCreditStatusKind    `json:"type"`
	Reason         ClaudeCreditFailureReason `json:"reason,omitempty"`
	RemoveToRedeem []string                  `json:"remove_to_redeem,omitempty"`
}

// ClaudeProviderUsage keeps the native Messages counter meanings: input excludes
// cache read/creation, whereas output includes thinking. Nil is unavailable,
// not measured zero. Never substitute transcript size or a synthetic total.
type ClaudeProviderUsage struct {
	Input          *ClaudeUsageCount          `json:"input_tokens"`
	CacheWrite     *ClaudeUsageCount          `json:"cache_creation_input_tokens"`
	CacheRead      *ClaudeUsageCount          `json:"cache_read_input_tokens"`
	Output         *ClaudeUsageCount          `json:"output_tokens"`
	OutputDetail   *ClaudeOutputTokenDetails  `json:"output_tokens_details"`
	CacheDetail    *ClaudeCacheCreationUsage  `json:"cache_creation"`
	ServerTools    *ClaudeServerToolUsage     `json:"server_tool_use"`
	FallbackCredit *ClaudeFallbackCreditUsage `json:"fallback_credit"`
	ServiceTier    *ClaudeNativeServiceTier   `json:"service_tier"`
	InferenceGeo   *string                    `json:"inference_geo"`
	Speed          *ClaudeNativeSpeed         `json:"speed"`
	Iterations     []ClaudeUsageIteration     `json:"iterations"`
}

type ClaudeUsageIteration struct {
	Kind        ClaudeUsageIterationKind  `json:"type"`
	Input       *ClaudeUsageCount         `json:"input_tokens"`
	CacheWrite  *ClaudeUsageCount         `json:"cache_creation_input_tokens"`
	CacheRead   *ClaudeUsageCount         `json:"cache_read_input_tokens"`
	Output      *ClaudeUsageCount         `json:"output_tokens"`
	CacheDetail *ClaudeCacheCreationUsage `json:"cache_creation"`
	Model       *string                   `json:"model,omitempty"`
}

// ClaudeNativeModelUsage is the harness's cumulative model ledger, which includes
// calls outside the main loop. Its scope/reset boundary belongs to the owned
// native runtime; repeated snapshots are not independent billable deltas.
type ClaudeNativeModelUsage struct {
	Input          *ClaudeUsageCount `json:"inputTokens"`
	Output         *ClaudeUsageCount `json:"outputTokens"`
	CacheRead      *ClaudeUsageCount `json:"cacheReadInputTokens"`
	CacheWrite     *ClaudeUsageCount `json:"cacheCreationInputTokens"`
	WebSearch      *ClaudeUsageCount `json:"webSearchRequests"`
	CostUSD        *ClaudeNativeUSD  `json:"costUSD"`
	ContextWindow  *ClaudeUsageCount `json:"contextWindow"`
	MaxOutput      *ClaudeUsageCount `json:"maxOutputTokens"`
	CanonicalModel *string           `json:"canonicalModel"`
	Provider       *string           `json:"provider"`
}

type ClaudeResultUsage struct {
	// MainLoop is per input turn and excludes subagent/auxiliary model calls.
	MainLoop      *ClaudeProviderUsage              `json:"main_loop_turn"`
	Models        map[string]ClaudeNativeModelUsage `json:"native_cumulative_models"`
	NativeCostUSD *ClaudeNativeUSD                  `json:"native_cumulative_cost_usd"`
}

func claudeUsageCounts(values ...*ClaudeUsageCount) bool {
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, err := strconv.ParseInt(string(*value), 10, 64)
		if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != string(*value) {
			return false
		}
	}
	return true
}

func (u *ClaudeCacheCreationUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeCacheCreationUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.OneHour, value.FiveMinutes) {
		return invalidClaudeUsage()
	}
	*u = ClaudeCacheCreationUsage(value)
	return nil
}

func (u *ClaudeOutputTokenDetails) UnmarshalJSON(raw []byte) error {
	type wire ClaudeOutputTokenDetails
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.Thinking) {
		return invalidClaudeUsage()
	}
	*u = ClaudeOutputTokenDetails(value)
	return nil
}

func (u *ClaudeServerToolUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeServerToolUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.WebSearch, value.WebFetch) {
		return invalidClaudeUsage()
	}
	*u = ClaudeServerToolUsage(value)
	return nil
}

func (u *ClaudeUsageIteration) UnmarshalJSON(raw []byte) error {
	type wire ClaudeUsageIteration
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.Input, value.Output, value.CacheWrite, value.CacheRead) {
		return invalidClaudeUsage()
	}
	switch value.Kind {
	case ClaudeCompactionIteration:
		if value.Model != nil {
			return invalidClaudeUsage()
		}
	case ClaudeMessageIteration:
		if value.Model != nil && Text(*value.Model, "native iteration model", 256, true) != nil {
			return invalidClaudeUsage()
		}
	case ClaudeAdvisorIteration, ClaudeFallbackIteration:
		if value.Model == nil || Text(*value.Model, "native iteration model", 256, true) != nil {
			return invalidClaudeUsage()
		}
	default:
		return invalidClaudeUsage()
	}
	*u = ClaudeUsageIteration(value)
	return nil
}

func (u *ClaudeProviderUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeProviderUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.Input, value.Output, value.CacheRead, value.CacheWrite) || len(value.Iterations) > 1024 {
		return invalidClaudeUsage()
	}
	if (value.ServiceTier != nil && !slices.Contains([]ClaudeNativeServiceTier{ClaudeStandardTier, ClaudePriorityTier, ClaudeBatchTier}, *value.ServiceTier)) || (value.Speed != nil && !slices.Contains([]ClaudeNativeSpeed{ClaudeStandardSpeed, ClaudeFastSpeed}, *value.Speed)) || (value.InferenceGeo != nil && Text(*value.InferenceGeo, "native inference geography", 128, false) != nil) {
		return invalidClaudeUsage()
	}
	*u = ClaudeProviderUsage(value)
	return nil
}

func (u *ClaudeFallbackCreditUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeFallbackCreditUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || (value.Status.Kind != ClaudeCreditRedeemed && value.Status.Kind != ClaudeCreditNotApplied) {
		return invalidClaudeUsage()
	}
	*u = ClaudeFallbackCreditUsage(value)
	return nil
}

func (s *ClaudeFallbackCreditStatus) UnmarshalJSON(raw []byte) error {
	type wire ClaudeFallbackCreditStatus
	var value wire
	if decodeUsageObject(raw, &value) != nil {
		return invalidClaudeUsage()
	}
	switch value.Kind {
	case ClaudeCreditRedeemed:
		if value.Reason != "" || value.RemoveToRedeem != nil {
			return invalidClaudeUsage()
		}
	case ClaudeCreditNotApplied:
		if !slices.Contains([]ClaudeCreditFailureReason{"body_mismatch", "continuation_excluded", "continuation_only", "expired", "invalid_target_model", "not_enabled", "reprice_unavailable", "temporarily_unavailable", "variant_fields_present", "wrong_organization", "wrong_platform", "wrong_workspace"}, value.Reason) {
			return invalidClaudeUsage()
		}
		if value.Reason == "variant_fields_present" {
			if !claudeUsageUniqueText(value.RemoveToRedeem, 128, 256) {
				return invalidClaudeUsage()
			}
		} else if value.RemoveToRedeem != nil {
			return invalidClaudeUsage()
		}
	default:
		return invalidClaudeUsage()
	}
	*s = ClaudeFallbackCreditStatus(value)
	return nil
}

func (u *ClaudeNativeModelUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeNativeModelUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || !claudeUsageCounts(value.Input, value.Output, value.CacheRead, value.CacheWrite, value.WebSearch) || (value.ContextWindow != nil && *value.ContextWindow == "0") || (value.MaxOutput != nil && *value.MaxOutput == "0") {
		return invalidClaudeUsage()
	}
	for _, label := range []*string{value.CanonicalModel, value.Provider} {
		if label != nil && Text(*label, "native model usage metadata", 256, true) != nil {
			return invalidClaudeUsage()
		}
	}
	*u = ClaudeNativeModelUsage(value)
	return nil
}

func (u *ClaudeResultUsage) UnmarshalJSON(raw []byte) error {
	type wire ClaudeResultUsage
	var value wire
	if decodeUsageObject(raw, &value) != nil || len(value.Models) > 256 {
		return invalidClaudeUsage()
	}
	for model := range value.Models {
		if Text(model, "native model usage identity", 256, true) != nil {
			return invalidClaudeUsage()
		}
	}
	*u = ClaudeResultUsage(value)
	return nil
}

func invalidClaudeUsage() *Error {
	return Fail(InvalidArgument, "Invalid native Claude usage observation.", "Preserve exact nullable counters and independent original usage scopes.")
}

// Native and retained usage schemas share exact case-sensitive field names.
// Decode also rejects duplicate keys at every depth before typed projection.
func decodeUsageObject(raw []byte, target any) error {
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil || fields == nil {
		return invalidClaudeUsage()
	}
	shape := reflect.TypeOf(target).Elem()
	allowed := make(map[string]bool, shape.NumField())
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return invalidClaudeUsage()
		}
	}
	return Decode(raw, target)
}
func claudeUsageUniqueText(values []string, count, length int) bool {
	if len(values) == 0 || len(values) > count {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if Text(value, "native usage metadata", length, true) != nil || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

type ClaudeUsageSource string

const (
	ClaudeMessageStartUsage    ClaudeUsageSource = "provider-message-start"
	ClaudeBlockCompleteUsage   ClaudeUsageSource = "block-complete"
	ClaudeMessageMetadataUsage ClaudeUsageSource = "provider-message-metadata"
	ClaudeInputResultUsage     ClaudeUsageSource = "input-result"
)

// Sources overlap. These records never enter the normalized response ledger,
// price estimates or budgets, and cumulative model snapshots are not deltas.
type ClaudeUsageObservation struct {
	Source          ClaudeUsageSource    `json:"source"`
	NativeEventID   string               `json:"native_event_id"`
	MessageID       ID                   `json:"message_id,omitempty"`
	NativeMessageID string               `json:"native_message_id,omitempty"`
	Model           string               `json:"model,omitempty"`
	Index           *uint32              `json:"index,omitempty"`
	Provider        *ClaudeProviderUsage `json:"provider,omitempty"`
	Result          *ClaudeResultUsage   `json:"result,omitempty"`
}

func (u ClaudeUsageObservation) Validate() error {
	if NativeIdentity(u.NativeEventID).Validate(ClaudeCode, NativeTurnIdentity) != nil {
		return invalidClaudeUsage()
	}
	switch u.Source {
	case ClaudeMessageStartUsage, ClaudeBlockCompleteUsage, ClaudeMessageMetadataUsage:
		if u.MessageID.Validate() != nil || Text(u.NativeMessageID, "native usage message", 1024, true) != nil || Text(u.Model, "native usage model", 256, true) != nil || u.Provider == nil || u.Result != nil || (u.Source == ClaudeBlockCompleteUsage) != (u.Index != nil) || u.Index != nil && *u.Index >= 1024 {
			return invalidClaudeUsage()
		}
		raw, err := json.Marshal(u.Provider)
		var copy ClaudeProviderUsage
		if err != nil || Decode(raw, &copy) != nil {
			return invalidClaudeUsage()
		}
	case ClaudeInputResultUsage:
		if u.Provider != nil || u.Result == nil || u.MessageID != "" || u.NativeMessageID != "" || u.Model != "" || u.Index != nil {
			return invalidClaudeUsage()
		}
		raw, err := json.Marshal(u.Result)
		var copy ClaudeResultUsage
		if err != nil || Decode(raw, &copy) != nil {
			return invalidClaudeUsage()
		}
	default:
		return invalidClaudeUsage()
	}
	return nil
}

type ClaudeUsageRecord struct {
	ExecutionID         ID                     `json:"execution_id"`
	AccountID           ID                     `json:"account_id"`
	ConnectionID        ID                     `json:"connection_id"`
	ProviderID          ID                     `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService    `json:"subscription_service,omitempty"`
	ModelID             ID                     `json:"model_id"`
	Harness             Harness                `json:"harness"`
	Version             string                 `json:"native_version"`
	ThreadID            string                 `json:"native_thread_id"`
	TurnID              string                 `json:"native_turn_id"`
	Sequence            uint64                 `json:"sequence"`
	Usage               ClaudeUsageObservation `json:"claude_observation"`
}
