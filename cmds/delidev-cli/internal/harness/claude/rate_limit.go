// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
)

type nativeRateLimitStatus string

const (
	nativeRateAllowed  nativeRateLimitStatus = "allowed"
	nativeRateWarning  nativeRateLimitStatus = "allowed_warning"
	nativeRateRejected nativeRateLimitStatus = "rejected"
)

// Native quota metadata is deliberately not converted into account observations.
// Its transport identity is checked by the lifecycle; contents grant no action.
func validNativeRateLimitEvent(raw json.RawMessage) bool {
	var event struct {
		Type    string          `json:"type"`
		Info    json.RawMessage `json:"rate_limit_info"`
		UUID    string          `json:"uuid"`
		Session domain.ID       `json:"session_id"`
	}
	if decodeNativeObject(raw, &event) != nil || event.Type != "rate_limit_event" || len(event.Info) > 64<<10 {
		return false
	}
	var info map[string]json.RawMessage
	if domain.Decode(event.Info, &info) != nil || info == nil || len(info) > 64 {
		return false
	}
	var status nativeRateLimitStatus
	if json.Unmarshal(info["status"], &status) != nil || !slices.Contains([]nativeRateLimitStatus{nativeRateAllowed, nativeRateWarning, nativeRateRejected}, status) {
		return false
	}
	return true
}
