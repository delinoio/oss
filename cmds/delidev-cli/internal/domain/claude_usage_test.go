package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeUsagePreservesExactNullableScopes(t *testing.T) {
	raw := []byte(`{"main_loop_turn":{"input_tokens":"9007199254740993","output_tokens":"0","cache_read_input_tokens":null,"output_tokens_details":{"thinking_tokens":"0"},"cache_creation":{"ephemeral_1h_input_tokens":"5"},"server_tool_use":{"web_fetch_requests":"2"},"service_tier":"priority","speed":"fast","inference_geo":"us","iterations":[{"type":"message","model":null,"input_tokens":"1"},{"type":"compaction","output_tokens":"2"},{"type":"advisor_message","model":"advisor"},{"type":"fallback_message","model":"fallback"}],"fallback_credit":{"status":{"type":"not_applied","reason":"variant_fields_present","remove_to_redeem":["thinking"]}}},"native_cumulative_models":{"model":{"inputTokens":"9223372036854775807","costUSD":"0.0006994999999999999","contextWindow":"200000","maxOutputTokens":"32000"}},"native_cumulative_cost_usd":"0.0006994999999999999"}`)
	var result ClaudeResultUsage
	if err := Decode(raw, &result); err != nil {
		t.Fatal(err)
	}
	model := result.Models["model"]
	if *result.MainLoop.Input != "9007199254740993" || *result.MainLoop.Output != "0" || result.MainLoop.CacheRead != nil || *model.Input != "9223372036854775807" || *model.CostUSD != *result.NativeCostUSD || len(result.MainLoop.Iterations) != 4 {
		t.Fatal("usage precision, availability or scope changed")
	}
	u := ClaudeUsageObservation{Source: ClaudeInputResultUsage, NativeEventID: string(NewID()), Result: &result}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(u)
	if !strings.Contains(string(encoded), `"9007199254740993"`) || !strings.Contains(string(encoded), `"0.0006994999999999999"`) {
		t.Fatal("public usage did not preserve exact strings")
	}
	for _, raw := range []string{`{}`, `{"input_tokens":null,"output_tokens":null}`} {
		var value ClaudeProviderUsage
		if Decode([]byte(raw), &value) != nil || value.Input != nil || value.Output != nil {
			t.Fatal("unavailable counters became measured zero")
		}
	}
}

func TestClaudeUsageRejectsMalformedAndMixedEvidence(t *testing.T) {
	for _, raw := range []string{
		`{"input_tokens":1}`, `{"input_tokens":"01"}`, `{"input_tokens":"-1"}`, `{"output_tokens":"9223372036854775808"}`, `{"output_tokens":"1.5"}`, `{"output_tokens":"1e2"}`,
		`{"input_tokens":"1","input_tokens":"2"}`, `{"Input_tokens":"1"}`, `{"unknown":"1"}`, `{"output_tokens_details":{"Thinking_tokens":"0"}}`,
		`{"cache_creation":{"ephemeral_1h_input_tokens":"-1"}}`, `{"server_tool_use":{"web_fetch_requests":"-1"}}`,
		`{"service_tier":"other"}`, `{"speed":"other"}`, `{"iterations":[{"type":"other"}]}`, `{"iterations":[{"type":"compaction","model":"not-allowed"}]}`, `{"iterations":[{"type":"advisor_message"}]}`,
		`{"fallback_credit":{}}`, `{"fallback_credit":{"status":{"type":"redeemed","reason":"expired"}}}`, `{"fallback_credit":{"status":{"type":"not_applied","reason":"variant_fields_present","remove_to_redeem":[]}}}`,
	} {
		var value ClaudeProviderUsage
		if Decode([]byte(raw), &value) == nil {
			t.Fatal("invalid provider usage accepted", raw)
		}
	}
	for _, raw := range []string{`{"native_cumulative_models":{"m":{"contextWindow":"0"}}}`, `{"native_cumulative_models":{"m":{"maxOutputTokens":"-1"}}}`, `{"native_cumulative_models":{"m":{"InputTokens":"1"}}}`, `{"native_cumulative_cost_usd":1.2}`, `{"native_cumulative_cost_usd":"-0"}`, `{"native_cumulative_cost_usd":"1e309"}`} {
		var value ClaudeResultUsage
		if Decode([]byte(raw), &value) == nil {
			t.Fatal("invalid result usage accepted", raw)
		}
	}
	base := ClaudeUsageObservation{Source: ClaudeMessageStartUsage, NativeEventID: string(NewID()), MessageID: NewID(), NativeMessageID: "msg_original", Model: "model", Provider: &ClaudeProviderUsage{}}
	for _, name := range []string{"native-id", "message-id", "mixed", "index", "source", "counter", "cost", "extra-observation"} {
		u := base
		switch name {
		case "native-id":
			u.NativeEventID = "msg_fabricated"
		case "message-id":
			u.MessageID = ""
		case "mixed":
			u.Result = &ClaudeResultUsage{}
		case "index":
			i := uint32(0)
			u.Index = &i
		case "source":
			u.Source = "unknown"
		case "counter":
			n := ClaudeUsageCount("-1")
			u.Provider = &ClaudeProviderUsage{Input: &n}
		case "cost":
			cost := ClaudeNativeUSD("NaN")
			u.Source, u.MessageID, u.NativeMessageID, u.Model, u.Provider, u.Result = ClaudeInputResultUsage, "", "", "", nil, &ClaudeResultUsage{NativeCostUSD: &cost}
		case "extra-observation":
			e := ExecutionEvent{Kind: ExecutionNoticeObserved, ClaudeUsage: &u}
			if e.Validate() == nil {
				t.Fatal("mixed event accepted")
			}
			continue
		}
		if u.Validate() == nil {
			t.Fatal("invalid usage accepted", name)
		}
	}
}
