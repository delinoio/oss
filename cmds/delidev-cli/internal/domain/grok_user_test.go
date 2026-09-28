package domain

import "testing"

func TestGrokUserHistoryPreservesExactMetadata(t *testing.T) {
	thread := string(NewID())
	v := GrokUserHistory{Source: GrokClosedFirstText, NativeEventID: thread + "-2", TimestampMS: "253402300799999", PromptIndex: "0", Model: "Original model", InputDigest: GrokUserInputDigest("Original 한글\n")}
	if err := v.Validate(thread); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "event", "timestamp", "rounded", "index", "model", "digest"} {
		t.Run(field, func(t *testing.T) {
			x := v
			switch field {
			case "source":
				x.Source = "live"
			case "event":
				x.NativeEventID = thread + "-02"
			case "timestamp":
				x.TimestampMS = "253402300800000"
			case "rounded":
				x.TimestampMS = "1e3"
			case "index":
				x.PromptIndex = "1"
			case "model":
				x.Model = ""
			case "digest":
				x.InputDigest = "changed"
			}
			if x.Validate(thread) == nil {
				t.Fatal("invalid retained user metadata accepted")
			}
		})
	}
}
