package domain

import "testing"

func TestClaudeDenialCompletionPreservesAbsentNativeInputAndIndependentCleanup(t *testing.T) {
	for _, scenario := range []string{"valid", "input", "reused-reference", "reused-envelope", "invalid-envelope", "native-input", "cleanup"} {
		v := ClaudeDenialCompletion{InputID: NewID(), InteractionID: NewID(), ArrivalID: NewID(), ContextID: NewID(), ResultID: NewID(), CommandNativeID: string(NewID()), IdleNativeID: string(NewID()), CleanupVerified: true}
		switch scenario {
		case "input":
			v.InputID = ""
		case "reused-reference":
			v.ContextID = v.ResultID
		case "reused-envelope":
			v.IdleNativeID = v.CommandNativeID
		case "invalid-envelope":
			v.CommandNativeID = "foreign"
		case "native-input":
			v.NativeInputID = &v.InputID
		case "cleanup":
			v.CleanupVerified = false
		}
		if (v.Validate() == nil) != (scenario == "valid") {
			t.Fatal("unproved denial completion accepted", scenario)
		}
	}
}
