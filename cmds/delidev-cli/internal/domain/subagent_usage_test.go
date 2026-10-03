// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
)

const codexChildUsageReport = `{"total":{"inputTokens":2,"cachedInputTokens":0,"cacheWriteInputTokens":null,"outputTokens":3,"reasoningOutputTokens":1,"totalTokens":5},"last":{"inputTokens":2,"cachedInputTokens":0,"cacheWriteInputTokens":null,"outputTokens":3,"reasoningOutputTokens":1,"totalTokens":5},"modelContextWindow":null}`

func usageCount(value string) *string { return &value }

func TestSubagentUsageValidatesNativeFamilyAndExactProjection(t *testing.T) {
	provider := ` {"input_tokens":9223372036854775807,"output_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":2,"ephemeral_5m_input_tokens":null},"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":1,"web_fetch_requests":null},"fallback_credit":{"status":{"type":"not_applied","reason":"variant_fields_present","remove_to_redeem":["temperature"]}},"iterations":[{"type":"advisor_message","input_tokens":1,"output_tokens":null,"cache_creation":{"ephemeral_1h_input_tokens":0},"model":"native-advisor"}],"service_tier":"priority","speed":"fast","inference_geo":"us"} `
	tests := []struct {
		name   string
		source SubagentSource
		usage  SubagentUsage
	}{
		{"codex", CodexHistorySource, SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("5"), Input: usageCount("2"), Output: usageCount("3"), NativeReport: codexChildUsageReport}},
		{"task uint64", ClaudeTaskSource, SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("18446744073709551615"), NativeReport: `{"total_tokens":18446744073709551615,"tool_uses":0,"duration_ms":1}`}},
		{"content provider", ClaudeContentSource, SubagentUsage{Scope: SubagentResponseUsage, Input: usageCount("9223372036854775807"), Output: usageCount("0"), NativeReport: provider}},
		{"history provider", ClaudeHistorySource, SubagentUsage{Scope: SubagentResponseUsage, Input: usageCount("9223372036854775807"), Output: usageCount("0"), NativeReport: provider}},
		{"provider unavailable", ClaudeContentSource, SubagentUsage{Scope: SubagentResponseUsage, NativeReport: `{"input_tokens":null,"output_tokens":null}`}},
		{"task zero", ClaudeTaskSource, SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("0"), NativeReport: `{"total_tokens":0,"tool_uses":0,"duration_ms":0}`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: test.source, SourceID: "original-usage", Usage: &test.usage}
			original := observation.Usage.NativeReport
			if observation.Validate() != nil || observation.Usage.NativeReport != original {
				t.Fatal("valid native report or exact original bytes were lost")
			}
		})
	}
}

func TestSubagentUsageRejectsClaudeProjectionAndTaskSchemaMismatch(t *testing.T) {
	for _, source := range []SubagentSource{ClaudeTaskSource, ClaudeContentSource, ClaudeHistorySource} {
		for _, scenario := range []string{"missing report", "wrong family", "wrong scope", "missing count", "changed count", "invented zero", "invented total", "noncanonical count"} {
			t.Run(string(source)+"/"+scenario, func(t *testing.T) {
				u := SubagentUsage{Scope: SubagentResponseUsage, Input: usageCount("2"), Output: usageCount("0"), NativeReport: `{"input_tokens":2,"output_tokens":0}`}
				if source == ClaudeTaskSource {
					u = SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("5"), NativeReport: `{"total_tokens":5,"tool_uses":0,"duration_ms":1}`}
				}
				switch scenario {
				case "missing report":
					u.NativeReport = ""
				case "wrong family":
					u.NativeReport = codexChildUsageReport
				case "wrong scope":
					if source == ClaudeTaskSource {
						u.Scope = SubagentResponseUsage
					} else {
						u.Scope = SubagentCumulativeUsage
					}
				case "missing count":
					u.Total, u.Input, u.Output = nil, nil, nil
				case "changed count":
					if source == ClaudeTaskSource {
						u.Total = usageCount("6")
					} else {
						u.Output = usageCount("1")
					}
				case "invented zero":
					if source == ClaudeTaskSource {
						u.Input = usageCount("0")
					} else {
						u.NativeReport = `{"input_tokens":2,"output_tokens":null}`
					}
				case "invented total":
					if source == ClaudeTaskSource {
						u.Output = usageCount("0")
					} else {
						u.Total = usageCount("2")
					}
				case "noncanonical count":
					if source == ClaudeTaskSource {
						u.Total = usageCount("05")
					} else {
						u.Input = usageCount("02")
					}
				}
				o := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: source, SourceID: "original-usage", Usage: &u}
				if o.Validate() == nil {
					t.Fatal("unproved Claude usage projection accepted")
				}
			})
		}
	}
	for _, report := range []string{`{"total_tokens":5,"tool_uses":0}`, `{"total_tokens":5,"tool_uses":null,"duration_ms":1}`, `{"total_tokens":5,"tool_uses":-1,"duration_ms":1}`, `{"total_tokens":5,"tool_uses":0,"duration_ms":18446744073709551616}`, `{"total_tokens":5,"tool_uses":0,"duration_ms":1,"secret":"private"}`, `{"total_tokens":5,"tool_uses":0,"Duration_ms":1}`, `{"total_tokens":5,"tool_uses":0,"duration_ms":1,"duration_ms":1}`} {
		o := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: ClaudeTaskSource, SourceID: "original-usage", Usage: &SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("5"), NativeReport: report}}
		if o.Validate() == nil {
			t.Fatal("malformed native task report accepted")
		}
	}
}

func TestSubagentUsageRejectsUnvalidatedReportsAndMismatchedCounters(t *testing.T) {
	for _, scenario := range []string{"no report", "arbitrary object", "secret extension", "nested extension", "wrong casing", "duplicate", "null", "array", "wrong family", "wrong scope", "total mismatch", "input mismatch", "missing projection", "string counter", "negative", "float", "exponent", "overflow", "missing codex total", "null codex last", "invalid window", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			u := SubagentUsage{Scope: SubagentCumulativeUsage, Total: usageCount("5"), Input: usageCount("2"), Output: usageCount("3"), NativeReport: codexChildUsageReport}
			o := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: CodexHistorySource, SourceID: "original-usage", Usage: &u}
			switch scenario {
			case "no report":
				u.NativeReport = ""
			case "arbitrary object":
				u.NativeReport = `{"diagnostic":"private diagnostic"}`
			case "secret extension":
				u.NativeReport = strings.Replace(u.NativeReport, `"modelContextWindow":null`, `"modelContextWindow":null,"secret":"private credential"`, 1)
			case "nested extension":
				u.NativeReport = strings.Replace(u.NativeReport, `"inputTokens":2`, `"inputTokens":2,"private":"diagnostic"`, 1)
			case "wrong casing":
				u.NativeReport = strings.Replace(u.NativeReport, `"inputTokens"`, `"InputTokens"`, 1)
			case "duplicate":
				u.NativeReport = strings.Replace(u.NativeReport, `"inputTokens":2`, `"inputTokens":2,"inputTokens":2`, 1)
			case "null":
				u.NativeReport = "null"
			case "array":
				u.NativeReport = "[]"
			case "wrong family":
				o.Source = CodexCollaborationSource
			case "wrong scope":
				u.Scope = SubagentResponseUsage
			case "total mismatch":
				u.Total = usageCount("6")
			case "input mismatch":
				u.Input = usageCount("0")
			case "missing projection":
				u.Output = nil
			case "string counter":
				u.NativeReport = strings.Replace(u.NativeReport, `"inputTokens":2`, `"inputTokens":"2"`, 1)
			case "negative":
				u.NativeReport = strings.Replace(u.NativeReport, `"cachedInputTokens":0`, `"cachedInputTokens":-1`, 1)
			case "float":
				u.NativeReport = strings.Replace(u.NativeReport, `"cachedInputTokens":0`, `"cachedInputTokens":0.0`, 1)
			case "exponent":
				u.NativeReport = strings.Replace(u.NativeReport, `"cachedInputTokens":0`, `"cachedInputTokens":0e0`, 1)
			case "overflow":
				u.NativeReport = strings.Replace(u.NativeReport, `"cachedInputTokens":0`, `"cachedInputTokens":9223372036854775808`, 1)
			case "missing codex total":
				u.NativeReport = strings.Replace(u.NativeReport, `"totalTokens":5`, `"totalTokens":null`, 1)
			case "null codex last":
				u.NativeReport = `{"total":{"inputTokens":2,"cachedInputTokens":0,"outputTokens":3,"reasoningOutputTokens":1,"totalTokens":5},"last":null}`
			case "invalid window":
				u.NativeReport = strings.Replace(u.NativeReport, `"modelContextWindow":null`, `"modelContextWindow":0`, 1)
			case "oversize":
				u.NativeReport = strings.Repeat(" ", 64<<10) + u.NativeReport
			}
			if o.Validate() == nil {
				t.Fatal("unproved native usage accepted")
			}
		})
	}
	for _, source := range []SubagentSource{ClaudeTaskSource, ClaudeContentSource, ClaudeHistorySource} {
		for _, report := range []string{`{}`, `{"input_tokens":"0"}`, `{"input_tokens":-1}`, `{"input_tokens":0e0}`, `{"input_tokens":9223372036854775808}`, `{"input_tokens":null,"diagnostic":"private"}`, `{"input_tokens":null,"cache_creation":{"private":"diagnostic"}}`, `{"input_tokens":null,"Input_tokens":0}`, `{"input_tokens":null,"input_tokens":null}`, `{"iterations":[{"type":"advisor_message"}]}`, `{"service_tier":"unknown"}`} {
			t.Run(string(source)+report, func(t *testing.T) {
				u := SubagentUsage{Scope: SubagentResponseUsage, NativeReport: report}
				if source == ClaudeTaskSource {
					u.Scope = SubagentCumulativeUsage
				}
				o := SubagentObservation{ID: NewID(), NativeID: "child", ParentID: "root", Status: SubagentRunning, Source: source, SourceID: "original-usage", Usage: &u}
				// An empty provider object has only unavailable counters, as in
				// the pinned native schema; task counters must all be present.
				if report == `{}` && source != ClaudeTaskSource {
					return
				}
				if o.Validate() == nil {
					t.Fatal("malformed Claude native usage accepted")
				}
			})
		}
	}
}
