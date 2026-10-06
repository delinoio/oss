// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"strconv"
)

type codexChildUsageCounts struct {
	Input      *int64 `json:"inputTokens"`
	Cached     *int64 `json:"cachedInputTokens"`
	CacheWrite *int64 `json:"cacheWriteInputTokens"`
	Output     *int64 `json:"outputTokens"`
	Reasoning  *int64 `json:"reasoningOutputTokens"`
	Total      *int64 `json:"totalTokens"`
}

func (c *codexChildUsageCounts) UnmarshalJSON(raw []byte) error {
	type wire codexChildUsageCounts
	var value wire
	if decodeUsageObject(raw, &value) != nil {
		return invalidSubagent()
	}
	*c = codexChildUsageCounts(value)
	return nil
}

func (c codexChildUsageCounts) normalized() NativeTokenCounts {
	return NativeTokenCounts{Input: c.Input, Cached: c.Cached, CacheWrite: c.CacheWrite, Output: c.Output, Reasoning: c.Reasoning, Total: c.Total}
}

func sameSubagentCount(observed *string, native *int64) bool {
	if observed == nil || native == nil {
		return observed == nil && native == nil
	}
	return *observed == strconv.FormatInt(*native, 10)
}

func sameClaudeSubagentCount(observed *string, native *ClaudeUsageCount) bool {
	if observed == nil || native == nil {
		return observed == nil && native == nil
	}
	return *observed == string(*native)
}

func (u SubagentUsage) validateNative(source SubagentSource) error {
	if u.NativeReport == "" || len(u.NativeReport) > 64<<10 {
		return invalidSubagent()
	}
	raw := []byte(u.NativeReport)
	switch source {
	case CodexHistorySource:
		var report struct {
			Total  codexChildUsageCounts `json:"total"`
			Last   codexChildUsageCounts `json:"last"`
			Window *int64                `json:"modelContextWindow"`
		}
		if u.Scope != SubagentCumulativeUsage || decodeUsageObject(raw, &report) != nil || (NativeTokenUsage{Total: report.Total.normalized(), Last: report.Last.normalized(), ContextWindow: report.Window}).Validate() != nil || !sameSubagentCount(u.Total, report.Total.Total) || !sameSubagentCount(u.Input, report.Total.Input) || !sameSubagentCount(u.Output, report.Total.Output) {
			return invalidSubagent()
		}
	case OpenCodeChildContentSource, OpenCodeChildHistorySource, OpenCodeChildCleanupSource:
		return validateOpenCodeChildUsage(u)
	case ClaudeTaskSource:
		var report struct {
			Tokens   *uint64 `json:"total_tokens"`
			Tools    *uint64 `json:"tool_uses"`
			Duration *uint64 `json:"duration_ms"`
		}
		if u.Scope != SubagentCumulativeUsage || decodeUsageObject(raw, &report) != nil || report.Tokens == nil || report.Tools == nil || report.Duration == nil || u.Total == nil || *u.Total != strconv.FormatUint(*report.Tokens, 10) || u.Input != nil || u.Output != nil {
			return invalidSubagent()
		}
	case ClaudeContentSource, ClaudeHistorySource:
		report, err := nativeClaudeChildUsage(raw)
		if u.Scope != SubagentResponseUsage || err != nil || u.Total != nil || !sameClaudeSubagentCount(u.Input, report.Input) || !sameClaudeSubagentCount(u.Output, report.Output) {
			return invalidSubagent()
		}
	default:
		// Collaboration and activity sources do not contain native usage.
		return invalidSubagent()
	}
	return nil
}

// Native provider counters are JSON integers; the shared Claude schema uses
// exact decimal strings. Convert only the pinned counter paths in a temporary
// validation copy, then apply the existing complete, closed schema. Never
// rewrite the retained native report or add overlapping counters.
func nativeClaudeChildUsage(raw []byte) (ClaudeProviderUsage, error) {
	var fields map[string]json.RawMessage
	var report ClaudeProviderUsage
	if Decode(raw, &fields) != nil || fields == nil || normalizeClaudeChildUsage(fields, true) != nil {
		return report, invalidSubagent()
	}
	checked, err := json.Marshal(fields)
	if err != nil || Decode(checked, &report) != nil {
		return report, invalidSubagent()
	}
	return report, nil
}

func normalizeClaudeChildCounters(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		raw, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		value, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != string(bytes.TrimSpace(raw)) {
			return invalidSubagent()
		}
		fields[name], _ = json.Marshal(strconv.FormatInt(value, 10))
	}
	return nil
}

func normalizeClaudeChildUsage(fields map[string]json.RawMessage, provider bool) error {
	if normalizeClaudeChildCounters(fields, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens") != nil {
		return invalidSubagent()
	}
	nested := map[string][]string{"cache_creation": {"ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens"}}
	if provider {
		nested["output_tokens_details"] = []string{"thinking_tokens"}
		nested["server_tool_use"] = []string{"web_search_requests", "web_fetch_requests"}
	}
	for name, counters := range nested {
		raw, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var object map[string]json.RawMessage
		if Decode(raw, &object) != nil || object == nil || normalizeClaudeChildCounters(object, counters...) != nil {
			return invalidSubagent()
		}
		fields[name], _ = json.Marshal(object)
	}
	if provider {
		if raw, present := fields["iterations"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var iterations []map[string]json.RawMessage
			if Decode(raw, &iterations) != nil || len(iterations) > 1024 {
				return invalidSubagent()
			}
			for _, iteration := range iterations {
				if iteration == nil || normalizeClaudeChildUsage(iteration, false) != nil {
					return invalidSubagent()
				}
			}
			fields["iterations"], _ = json.Marshal(iterations)
		}
	}
	return nil
}
