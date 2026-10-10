package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexDynamicPublicShapeRejectsMixedFieldsAndChangedOriginalIdentity(t *testing.T) {
	started := ToolSnapshot{Kind: CodexDynamicTool, Status: ToolRunning, Dynamic: &CodexDynamicObservation{Tool: "fixture_tool", ArgumentsJSON: `{}`}}
	negative, duration := false, int64(17)
	text := "native negative"
	completed := ToolSnapshot{Kind: CodexDynamicTool, Status: ToolCompleted, Dynamic: &CodexDynamicObservation{Tool: "fixture_tool", ArgumentsJSON: `{}`, Success: &negative, DurationMS: &duration, ContentItems: []DynamicContent{{Kind: DynamicText, Text: &text}, {Kind: DynamicImage, Digest: strings.Repeat("a", 64)}, {Kind: DynamicAudio, Digest: strings.Repeat("b", 64)}}}}
	if started.Validate() != nil || completed.Validate() != nil || ValidateCodexDynamicTransition(started, completed) != nil {
		t.Fatal("valid independent negative outcome rejected")
	}
	raw, _ := json.Marshal(completed)
	var retained ToolSnapshot
	if Decode(raw, &retained) != nil || retained.Validate() != nil || retained.Dynamic.Success == nil || *retained.Dynamic.Success || len(retained.Dynamic.ContentItems) != 3 {
		t.Fatal("negative/ordered content did not round trip")
	}
	for _, change := range []func(*ToolSnapshot){func(s *ToolSnapshot) { s.Dynamic.Tool = "another" }, func(s *ToolSnapshot) { s.Dynamic.ArgumentsJSON = `{"other":true}` }, func(s *ToolSnapshot) { namespace := "other"; s.Dynamic.Namespace = &namespace }, func(s *ToolSnapshot) { s.Kind = CommandTool }, func(s *ToolSnapshot) { s.Command = &CommandObservation{} }, func(s *ToolSnapshot) { s.Status = ToolRunning }} {
		next := completed
		observed := *completed.Dynamic
		next.Dynamic = &observed
		change(&next)
		if ValidateCodexDynamicTransition(started, next) == nil {
			t.Fatal("substituted original dynamic operation")
		}
	}
	for _, arguments := range []string{`{"duplicate":1,"duplicate":2}`, `{`, strings.Repeat("x", MaxMessageText+1)} {
		next := started
		v := *started.Dynamic
		next.Dynamic = &v
		v.ArgumentsJSON = arguments
		if next.Validate() == nil {
			t.Fatal("bad arguments accepted")
		}
	}
}
