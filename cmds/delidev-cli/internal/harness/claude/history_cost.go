// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"encoding/json"
	"math"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Native shutdown persists cumulative cost metadata outside the message graph.
// Validate its bounded schema and retain its original bytes in the file digest;
// it cannot supply conversation, input completion, accounting or price authority.
func validHistoryCostState(fields map[string]json.RawMessage) bool {
	required := map[string]float64{"totalCostUSD": 1e9, "totalAPIDuration": 1e15, "totalAPIDurationWithoutRetries": 1e15, "totalToolDuration": 1e15, "totalLinesAdded": 1e15, "totalLinesRemoved": 1e15, "totalDuration": 1e15, "startTime": 1e15}
	if len(fields) < len(required)+3 || len(fields) > len(required)+4 {
		return false
	}
	for key, raw := range fields {
		if limit, ok := required[key]; ok {
			if !validHistoryCostNumber(raw, limit) {
				return false
			}
			continue
		}
		switch key {
		case "type", "sessionId", "modelUsage":
		case "hasUnknownModelCost":
			var flag *bool
			if json.Unmarshal(raw, &flag) != nil || flag == nil {
				return false
			}
		default:
			return false
		}
	}
	for key, limit := range required {
		if !validHistoryCostNumber(fields[key], limit) {
			return false
		}
	}
	var models map[string]map[string]json.RawMessage
	if domain.Decode(fields["modelUsage"], &models) != nil || models == nil || len(models) > 128 {
		return false
	}
	totals := map[string]float64{}
	for name, model := range models {
		if domain.Text(name, "native cost model", 1024, true) != nil || len(model) < 6 || len(model) > 7 {
			return false
		}
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
			if !validHistoryCostNumber(model[key], 1e15) {
				return false
			}
		}
		// Six counters are required; thinking is optional. Keep unknown fields closed.
		for key, raw := range model {
			switch key {
			case "inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD", "thinkingTokens":
				if !validHistoryCostNumber(raw, 1e15) {
					return false
				}
			default:
				return false
			}
		}
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens"} {
			var n float64
			_ = json.Unmarshal(model[key], &n)
			totals[key] += n
			if totals[key] > 1e15 {
				return false
			}
		}
	}
	return true
}

func validHistoryCostNumber(raw json.RawMessage, limit float64) bool {
	var n *float64
	return len(raw) != 0 && json.Unmarshal(raw, &n) == nil && n != nil && !math.IsNaN(*n) && !math.IsInf(*n, 0) && *n >= 0 && *n <= limit
}
