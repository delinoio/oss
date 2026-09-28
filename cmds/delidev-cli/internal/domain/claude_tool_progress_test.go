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

func TestClaudeToolHeartbeatPreservesSeparateIdentity(t *testing.T) {
	ref := ClaudeToolReference{ID: NewID(), NativeID: "original-tool", Name: "Bash"}
	yes := true
	base := ClaudeToolProgressObservation{Tool: ref, ParentToolID: &ref.NativeID, NativeToolID: ref.NativeID + "-heartbeat-0", ElapsedSeconds: "30", Heartbeat: &yes}
	for _, change := range []string{"valid", "next", "parent", "identity", "leading-zero", "overflow", "missing-identity", "missing-parent", "missing-heartbeat", "false", "task"} {
		t.Run(change, func(t *testing.T) {
			v := base
			other, no := "foreign", false
			switch change {
			case "next":
				v.NativeToolID = ref.NativeID + "-heartbeat-12"
			case "parent":
				v.ParentToolID = &other
			case "identity":
				v.NativeToolID = other + "-heartbeat-0"
			case "leading-zero":
				v.NativeToolID = ref.NativeID + "-heartbeat-00"
			case "overflow":
				v.NativeToolID = ref.NativeID + "-heartbeat-4294967296"
			case "missing-identity":
				v.NativeToolID = ""
			case "missing-parent":
				v.ParentToolID = nil
			case "missing-heartbeat":
				v.Heartbeat = nil
			case "false":
				v.Heartbeat = &no
			case "task":
				v.TaskID = &other
			}
			if (v.Validate() == nil) != (change == "valid" || change == "next") {
				t.Fatal("incorrect heartbeat ownership classification")
			}
		})
	}
}
