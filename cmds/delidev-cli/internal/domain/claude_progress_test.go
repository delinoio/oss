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

func TestClaudeAPIRetryProgressPreservesExactOriginalObservations(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		for _, scenario := range []string{"valid", "foreign", "mixed", "missing", "overflow", "rounded", "status", "error"} {
			t.Run(scenario, func(t *testing.T) {
				id := string(NewID())
				v := ClaudeProgressObservation{NativeEventID: id, Kind: ClaudeAPIRetryProgress, InputAccepted: accepted, APIRetry: &ClaudeAPIRetryObservation{NativeEventID: id, Attempt: "9007199254740993", MaxRetries: "18446744073709551615", DelayMS: "0", Error: ClaudeAPIUnknown}}
				switch scenario {
				case "foreign":
					v.APIRetry.NativeEventID = string(NewID())
				case "mixed":
					v.Status = &ClaudeStatusObservation{}
				case "missing":
					v.APIRetry = nil
				case "overflow":
					v.APIRetry.DelayMS = "18446744073709551616"
				case "rounded":
					v.APIRetry.Attempt = "1.0"
				case "status":
					status := uint16(200)
					v.APIRetry.ErrorStatus = &status
				case "error":
					v.APIRetry.Error = "unclassified"
				}
				if (v.Validate() == nil) != (scenario == "valid") {
					t.Fatal("retry authority or exact values changed")
				}
			})
		}
	}
}
