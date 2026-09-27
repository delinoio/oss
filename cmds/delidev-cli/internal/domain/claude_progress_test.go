package domain

import "testing"

func TestClaudeThinkingProgressPreservesUnsignedExactEstimates(t *testing.T) {
	for _, count := range []ClaudeProgressCount{"0", "9007199254740993", "18446744073709551615", "18446744073709551616", "-1", "01", "1.0", "1e2", ""} {
		v := ClaudeProgressObservation{NativeEventID: string(NewID()), Kind: ClaudeThinkingProgress, InputAccepted: true, Thinking: &ClaudeThinkingObservation{Tokens: count, Delta: "0"}}
		valid := count == "0" || count == "9007199254740993" || count == "18446744073709551615"
		if (v.Validate() == nil) != valid {
			t.Fatal("native estimate was rounded or changed", count)
		}
		v.InputAccepted = false
		if v.Validate() == nil {
			t.Fatal("thinking estimate accepted an input")
		}
	}
}

func TestClaudeStatusPreservesNullAndIndependentPermissionAndCompaction(t *testing.T) {
	v := ClaudeProgressObservation{NativeEventID: string(NewID()), Kind: ClaudeStatusProgress, Status: &ClaudeStatusObservation{}}
	if err := v.Validate(); err != nil {
		t.Fatal("explicit null status lost", err)
	}
	status, permission, result, diagnostic := ClaudeCompacting, ClaudePermissionDefault, ClaudeCompactFailed, "Original compact failure"
	v.Status = &ClaudeStatusObservation{Status: &status, Permission: &permission, CompactResult: &result, CompactError: &diagnostic}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	v.Thinking = &ClaudeThinkingObservation{Tokens: "0", Delta: "0"}
	if v.Validate() == nil {
		t.Fatal("mixed native progress accepted")
	}
	v.Thinking = nil
	permission = "foreign"
	if v.Validate() == nil {
		t.Fatal("unrecognized native permission accepted")
	}
}
