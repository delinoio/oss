package claude

import (
	"encoding/json"
	"testing"
)

func TestNativeUsagePreservesMissingZeroPrecisionAndDistinctScopes(t *testing.T) {
	for _, raw := range []string{`{}`, `{"input_tokens":null,"output_tokens":null,"cache_read_input_tokens":null}`} {
		var value ProviderUsage
		if json.Unmarshal([]byte(raw), &value) != nil || value.Input != nil || value.Output != nil || value.CacheRead != nil {
			t.Fatal("unavailable usage became zero")
		}
	}
	value, err := decodeResultUsage(json.RawMessage(`{"input_tokens":13,"output_tokens":0,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens_details":{"thinking_tokens":0}}`), json.RawMessage(`{"fixture-model":{"inputTokens":16,"outputTokens":22,"cacheReadInputTokens":14,"cacheCreationInputTokens":10,"costUSD":0.0006994999999999999,"contextWindow":200000,"maxOutputTokens":32000,"canonicalModel":"fixture-model","provider":"firstParty"}}`), json.RawMessage(`0.0006994999999999999`))
	if err != nil {
		t.Fatal(err)
	}
	model := value.Models["fixture-model"]
	if value.MainLoop == nil || *value.MainLoop.Input != 13 || *value.MainLoop.Output != 0 || value.MainLoop.OutputDetail == nil || *value.MainLoop.OutputDetail.Thinking != 0 || *model.Input != 16 || *model.Output != 22 || *value.NativeCostUSD != "0.0006994999999999999" || *model.CostUSD != *value.NativeCostUSD {
		t.Fatal("native usage scopes, measured zero or precise estimate changed")
	}
	raw, err := json.Marshal(value.NativeCostUSD)
	if err != nil || string(raw) != "0.0006994999999999999" {
		t.Fatal("native estimate did not round trip exactly")
	}
	absent, err := decodeResultUsage(nil, nil, nil)
	if err != nil || absent.MainLoop != nil || absent.Models != nil || absent.NativeCostUSD != nil {
		t.Fatal("missing result usage was fabricated")
	}
}

func TestNativeUsageRetainsDocumentedIterationAndCreditObservations(t *testing.T) {
	raw := []byte(`{"service_tier":"priority","speed":"fast","inference_geo":"us","iterations":[{"type":"message","model":null,"input_tokens":1},{"type":"compaction","output_tokens":2},{"type":"advisor_message","model":"advisor","output_tokens":3},{"type":"fallback_message","model":"fallback","output_tokens":4}],"fallback_credit":{"status":{"type":"not_applied","reason":"variant_fields_present","remove_to_redeem":["thinking"]}},"cache_creation":{"ephemeral_1h_input_tokens":5,"ephemeral_5m_input_tokens":6},"server_tool_use":{"web_search_requests":7,"web_fetch_requests":8}}`)
	var usage ProviderUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatal(err)
	}
	if len(usage.Iterations) != 4 || usage.Iterations[0].Model != nil || usage.Iterations[1].Kind != CompactionIteration || *usage.Iterations[2].Model != "advisor" || *usage.Iterations[3].Model != "fallback" || usage.Input != nil || usage.Output != nil || usage.FallbackCredit.Status.Kind != CreditNotApplied || usage.FallbackCredit.Status.RemoveToRedeem[0] != "thinking" || *usage.CacheDetail.OneHour != 5 || *usage.ServerTools.WebFetch != 8 {
		t.Fatal("iteration detail was aggregated or discarded")
	}
	// These metadata observations do not initiate any retry, routing change or
	// redemption. The immutable relay scope independently rejects fallbacks.
	for _, status := range []string{`{"type":"redeemed"}`, `{"type":"not_applied","reason":"expired"}`} {
		var credit FallbackCreditStatus
		if err := json.Unmarshal([]byte(status), &credit); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeUsageRejectsAmbiguousAndInvalidCounters(t *testing.T) {
	for _, raw := range []string{
		`{"input_tokens":-1}`, `{"output_tokens":9223372036854775808}`, `{"output_tokens":1.5}`, `{"output_tokens":"1"}`,
		`{"output_tokens":1,"output_tokens":2}`, `{"Output_tokens":1}`, `{"unknown":1}`, `{"output_tokens_details":{"Thinking_tokens":0}}`,
		`{"cache_creation":{"ephemeral_1h_input_tokens":-1}}`, `{"server_tool_use":{"web_fetch_requests":-1}}`,
		`{"service_tier":"other"}`, `{"speed":"other"}`, `{"iterations":[{"type":"other"}]}`,
		`{"iterations":[{"type":"compaction","model":"not-allowed"}]}`, `{"iterations":[{"type":"advisor_message"}]}`, `{"iterations":[{"type":"fallback_message","model":""}]}`,
		`{"fallback_credit":{}}`, `{"fallback_credit":{"status":{"type":"redeemed","reason":"expired"}}}`,
		`{"fallback_credit":{"status":{"type":"not_applied","reason":"future"}}}`, `{"fallback_credit":{"status":{"type":"not_applied","reason":"variant_fields_present","remove_to_redeem":[]}}}`,
		`{"fallback_credit":{"status":{"type":"not_applied","reason":"expired","remove_to_redeem":["thinking"]}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var usage ProviderUsage
			if json.Unmarshal([]byte(raw), &usage) == nil {
				t.Fatal("invalid native usage accepted")
			}
		})
	}
	for _, raw := range []string{`{"m":{"contextWindow":0}}`, `{"m":{"maxOutputTokens":-1}}`, `{"m":{"inputTokens":-1}}`, `{"m":{"InputTokens":1}}`, `{"m":{"provider":""}}`, `{"m":{},"m":{}}`} {
		if _, err := decodeResultUsage(nil, json.RawMessage(raw), nil); err == nil {
			t.Fatal("invalid cumulative model ledger accepted", raw)
		}
	}
	for _, raw := range []string{`-1`, `"1.2"`, `1e309`, `true`, `{}`, `null`} {
		var cost NativeUSD
		if json.Unmarshal([]byte(raw), &cost) == nil {
			t.Fatal("invalid native cost accepted", raw)
		}
	}
}
