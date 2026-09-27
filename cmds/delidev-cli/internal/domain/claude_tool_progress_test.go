package domain

import "testing"

func TestClaudeToolProgressKeepsExactNativeElapsedAndClosedFamilies(t *testing.T) {
	ref := ClaudeToolReference{ID: NewID(), NativeID: "original-tool", Name: "Bash"}
	for _, value := range []string{"0", "0.0010", "9007199254740993", "9.007199254740993e+15", "-1", "-0", "01", "1e999", " 1", "1 ", "null", "true", "0xff", ""} {
		o := ClaudeProgressObservation{NativeEventID: string(NewID()), Kind: ClaudeToolProgress, InputAccepted: true, Tool: &ClaudeToolProgressObservation{Tool: ref, ElapsedSeconds: value}}
		valid := value == "0" || value == "0.0010" || value == "9007199254740993" || value == "9.007199254740993e+15"
		if (o.Validate() == nil) != valid {
			t.Fatal("invalid elapsed classification", value)
		}
		if valid {
			o.Thinking = &ClaudeThinkingObservation{Tokens: "0", Delta: "0"}
			if o.Validate() == nil {
				t.Fatal("mixed family accepted")
			}
		}
	}
	for _, duplicateNative := range []bool{false, true} {
		other := ref
		if duplicateNative {
			other.ID = NewID()
		}
		if (ClaudeToolSummaryObservation{Summary: "Original advisory text", Tools: []ClaudeToolReference{ref, other}}).Validate() == nil {
			t.Fatal("duplicate summary owner accepted")
		}
	}
}
