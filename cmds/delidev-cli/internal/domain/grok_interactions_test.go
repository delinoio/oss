package domain

import (
	"encoding/json"
	"testing"
)

func TestGrokResponsesRejectForeignAndNullFamilies(t *testing.T) {
	for _, raw := range []string{`{"grok":null}`, `{"grok":{"outcome":null}}`, `{"grok":{"decision":"allow-once","outcome":null}}`, `{"grok":{"decision":"allow-once"},"claude":null}`} {
		var v ApprovalResponseInput
		if Decode([]byte(raw), &v) == nil {
			t.Fatal("ambiguous approval decoded", raw)
		}
	}
	for _, raw := range []string{`{"grok":null}`, `{"grok":{"outcome":"cancelled","answers":null}}`, `{"grok":{"outcome":"accepted","answers":{"q":null}}}`, `{"grok":{"outcome":"accepted","annotations":{"q":{"notes":null}}}}`, `{"grok":{"outcome":"cancelled"},"answers":{}}`} {
		var v QuestionResponseInput
		if Decode([]byte(raw), &v) == nil {
			t.Fatal("ambiguous question decoded", raw)
		}
	}
	for _, raw := range []string{`{"grok":{"decision":"allow-edits-session"}}`, `{"grok":{"outcome":"cancelled"}}`} {
		var v ApprovalResponseInput
		if Decode([]byte(raw), &v) != nil {
			t.Fatal("valid native choice rejected", raw)
		}
	}
}
func TestGrokExactNativeCountersAndNullableQuestionShape(t *testing.T) {
	for _, raw := range []string{`18446744073709551615`, `"18446744073709551615"`} {
		var n GrokCount
		if json.Unmarshal([]byte(raw), &n) != nil || string(n) != "18446744073709551615" {
			t.Fatal("counter rounded")
		}
	}
	for _, raw := range []string{`18446744073709551616`, `1.5`, `null`, `"01"`} {
		var n GrokCount
		if json.Unmarshal([]byte(raw), &n) == nil {
			t.Fatal("invalid counter accepted")
		}
	}
	raw := []byte(`{"question":"Original?","options":[],"multiSelect":null}`)
	var q GrokQuestion
	if Decode(raw, &q) != nil || !q.MultiplePresent || q.Multiple != nil {
		t.Fatal("native null lost")
	}
	encoded, _ := json.Marshal(q)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	if string(fields["multiSelect"]) != "null" {
		t.Fatal("native null became false or omitted")
	}
}
