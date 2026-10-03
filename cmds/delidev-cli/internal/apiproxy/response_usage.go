// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// The resumed Codex profile does not expose raw-response notifications. Only
// manual actions use this relay observation, so ordinary native accounting
// cannot charge the same original response a second time. Never retain bodies.
func observeResponseUsage(ctx context.Context, lease *Lease, request domain.ID, object map[string]json.RawMessage) error {
	if lease.ObserveResponseUsage == nil {
		return nil
	}
	var id, status string
	if json.Unmarshal(object["id"], &id) != nil || domain.Text(id, "original response identity", 1024, true) != nil {
		return errInvalidDocument
	}
	if raw, ok := object["status"]; ok && (json.Unmarshal(raw, &status) != nil || status != "completed") {
		return nil
	}
	digest := sha256.Sum256([]byte(id))
	observed := domain.NativeResponseUsage{Source: domain.CompactionHTTPResponse, ResponseDigest: hex.EncodeToString(digest[:]), CostEvidence: domain.UsageCostMissing}
	if raw := object["usage"]; nonNull(raw) {
		var usage struct {
			Input        *int64 `json:"input_tokens"`
			Output       *int64 `json:"output_tokens"`
			Total        *int64 `json:"total_tokens"`
			InputDetails *struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails *struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		}
		// Provider extensions may exist; consume only these original integer fields.
		// A fractional/overflowing value cannot silently become zero or a float.
		if json.Unmarshal(raw, &usage) != nil {
			return errInvalidDocument
		}
		counts := domain.NativeTokenCounts{Input: usage.Input, Output: usage.Output, Total: usage.Total}
		if usage.InputDetails != nil {
			counts.Cached = usage.InputDetails.Cached
		}
		if usage.OutputDetails != nil {
			counts.Reasoning = usage.OutputDetails.Reasoning
		}
		if counts.Input != nil || counts.Output != nil || counts.Total != nil || counts.Cached != nil || counts.Reasoning != nil {
			observed.Counts = &counts
		}
	}
	if observed.Validate() != nil {
		return errInvalidDocument
	}
	return lease.ObserveResponseUsage(ctx, request, observed)
}
