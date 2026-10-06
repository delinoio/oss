package domain

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

type OpenCodeUsageSource string

const (
	OpenCodeStepUsage    OpenCodeUsageSource = "step-finish"
	OpenCodeMessageUsage OpenCodeUsageSource = "assistant-finalized"
)

// Native OpenCode counters are disjoint categories after native normalization,
// unlike the inclusive Codex input/output counters. Native missing values can
// already have become zero; these observations are never exact billing proof.
type OpenCodeTokenCounts struct {
	Input      string  `json:"uncached_input"`
	Output     string  `json:"nonreasoning_output"`
	Reasoning  string  `json:"reasoning_output"`
	CacheRead  string  `json:"cache_read_input"`
	CacheWrite string  `json:"cache_write_input"`
	Total      *string `json:"total"`
}

type OpenCodeUsageObservation struct {
	Source         OpenCodeUsageSource `json:"source"`
	NativeID       string              `json:"native_id"`
	NativeParentID string              `json:"native_parent_id"`
	Counts         OpenCodeTokenCounts `json:"counts"`
	NativeEstimate string              `json:"native_estimate"`
}

func (u OpenCodeUsageObservation) Validate() error {
	if NativeIdentity(u.NativeParentID).Validate(OpenCode, NativeMessageIdentity) != nil {
		return invalidObservation()
	}
	switch u.Source {
	case OpenCodeStepUsage:
		if NativeIdentity(u.NativeID).Validate(OpenCode, NativePartIdentity) != nil {
			return invalidObservation()
		}
	case OpenCodeMessageUsage:
		if u.NativeID != u.NativeParentID {
			return invalidObservation()
		}
	default:
		return invalidObservation()
	}
	values := []string{u.Counts.Input, u.Counts.Output, u.Counts.Reasoning, u.Counts.CacheRead, u.Counts.CacheWrite}
	if u.Counts.Total != nil {
		values = append(values, *u.Counts.Total)
	}
	for _, value := range values {
		count, err := strconv.ParseUint(value, 10, 64)
		if err != nil || count > maxNativeExactInteger || strconv.FormatUint(count, 10) != value {
			return invalidObservation()
		}
	}
	if !NativeNonnegativeDecimal(u.NativeEstimate) {
		return invalidObservation()
	}
	return nil
}

// The original observation stays outside the response ledger. Only step-finish
// parts enter the separate accounting ledger; finalized assistants overlap.
type OpenCodeUsageRecord struct {
	ExecutionID         ID                       `json:"execution_id"`
	AccountID           ID                       `json:"account_id"`
	ConnectionID        ID                       `json:"connection_id"`
	ProviderID          ID                       `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService      `json:"subscription_service,omitempty"`
	ModelID             ID                       `json:"model_id"`
	Harness             Harness                  `json:"harness"`
	Version             string                   `json:"native_version"`
	ThreadID            string                   `json:"native_thread_id"`
	TurnID              string                   `json:"native_turn_id"`
	Sequence            uint64                   `json:"sequence"`
	Usage               OpenCodeUsageObservation `json:"opencode_observation"`
}

var nativeDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Preserve the original number spelling without expanding an attacker-selected
// exponent into a big integer. Float decoding only checks the pinned native
// finite-number range; the original mantissa determines negativity, including
// tiny negative values that would otherwise underflow to floating-point zero.
func NativeNonnegativeDecimal(value string) bool {
	if len(value) == 0 || len(value) > 128 || !nativeDecimalPattern.MatchString(value) {
		return false
	}
	var number float64
	if json.Unmarshal([]byte(value), &number) != nil {
		return false
	}
	if strings.HasPrefix(value, "-") {
		mantissa := strings.FieldsFunc(value[1:], func(r rune) bool { return r == 'e' || r == 'E' })[0]
		for _, char := range mantissa {
			if char != '0' && char != '.' {
				return false
			}
		}
	}
	return true
}
