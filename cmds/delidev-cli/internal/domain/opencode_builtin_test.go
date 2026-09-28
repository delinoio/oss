package domain

import (
	"strings"
	"testing"
)

func TestOpenCodeBuiltinRetainsExactJSONAndRejectsInvalidObjects(t *testing.T) {
	for _, raw := range []string{`{}`, `{"n":9007199254740993,"scale":1.000,"nested":{"retained":true}}`} {
		if ValidateOpenCodeObjectJSON(raw) != nil {
			t.Fatal("original object representation rejected")
		}
	}
	for _, raw := range []string{`null`, `[]`, `1`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{"n":NaN}`, "{\"x\":\"\x00\"}", strings.Repeat(" ", MaxMessageText+1) + `{}`} {
		if ValidateOpenCodeObjectJSON(raw) == nil {
			t.Fatalf("invalid original object accepted: %.80q", raw)
		}
	}
}

func TestOpenCodeBuiltinTransitionsOwnAppliedInputAndToolIdentity(t *testing.T) {
	empty, output, title, failure := "", "original result", "original title", "original failure"
	metadata := `{"diagnostics":{},"exact":9007199254740993}`
	end := uint64(200)
	for _, name := range []OpenCodeBuiltinName{OpenCodeWrite, OpenCodeEdit, OpenCodeApplyPatch, OpenCodeGlob, OpenCodeGrep} {
		pending := ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolPending, Builtin: &OpenCodeBuiltinObservation{Name: name, CallID: "call_original", InputJSON: `{}`, Raw: &empty}}
		running := ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolRunning, Builtin: &OpenCodeBuiltinObservation{Name: name, CallID: "call_original", InputJSON: `{"original":"input"}`, Timing: &OpenCodeToolTiming{Start: 100}}}
		completed := ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolCompleted, Builtin: &OpenCodeBuiltinObservation{Name: name, CallID: "call_original", InputJSON: running.Builtin.InputJSON, Timing: &OpenCodeToolTiming{Start: 100, End: &end}, Title: &title, Output: &output, MetadataJSON: &metadata}}
		if ValidateOpenCodeBuiltinTransition(pending, running) != nil || ValidateOpenCodeBuiltinTransition(running, completed) != nil || completed.Builtin.InputJSON != running.Builtin.InputJSON {
			t.Fatal("original builtin lifecycle rejected or reinterpreted")
		}
		if ValidateOpenCodeBuiltinTransition(pending, completed) == nil || ValidateOpenCodeBuiltinTransition(completed, running) == nil {
			t.Fatal("missing applied input or reopened completion accepted")
		}
		for _, change := range []func(*OpenCodeBuiltinObservation){
			func(v *OpenCodeBuiltinObservation) { v.Name = "external" },
			func(v *OpenCodeBuiltinObservation) { v.CallID = "changed" },
			func(v *OpenCodeBuiltinObservation) { v.InputJSON = `{"original":"changed"}` },
			func(v *OpenCodeBuiltinObservation) { v.MetadataJSON = nil },
			func(v *OpenCodeBuiltinObservation) { v.Timing = &OpenCodeToolTiming{Start: 101, End: &end} },
		} {
			changed := *completed.Builtin
			change(&changed)
			if ValidateOpenCodeBuiltinTransition(running, ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolCompleted, Builtin: &changed}) == nil {
				t.Fatal("changed original builtin evidence accepted")
			}
		}
		failed := *running.Builtin
		failed.Error, failed.Timing = &failure, completed.Builtin.Timing
		if ValidateOpenCodeBuiltinTransition(running, ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolFailed, Builtin: &failed}) != nil {
			t.Fatal("original failure required invented output or metadata")
		}
	}
}
