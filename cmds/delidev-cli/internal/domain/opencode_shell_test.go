package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOpenCodeShellPreservesMissingAndNullExit(t *testing.T) {
	raw := `{"kind":"opencode-shell","status":"completed","changes":null,"shell":{"call_id":"original","input":{"command":"","timeout":1},"title":"","output":"","time":{"start":0,"end":0},"metadata":{"output":"","exit":null,"exit_observed":true,"truncated":false}}}`
	var value ToolSnapshot
	if Decode([]byte(raw), &value) != nil || value.Validate() != nil {
		t.Fatal("original nullable Shell exit was rejected")
	}
	encoded, err := json.Marshal(value)
	var retained ToolSnapshot
	if err != nil || Decode(encoded, &retained) != nil || !reflect.DeepEqual(retained, value) || retained.Shell.Metadata.Exit != nil || !retained.Shell.Metadata.ExitObserved {
		t.Fatal("original Shell roundtrip invented absent fields or zero exit")
	}
	for _, change := range [][2]string{
		{`"exit_observed":true`, `"exit_observed":false`},
		{`"exit":null`, `"exit":9007199254740992`},
		{`"timeout":1`, `"timeout":0`},
		{`"timeout":1`, `"timeout":0.5`},
		{`"timeout":1`, `"timeout":9007199254740992`},
		{`"end":0`, `"end":-1`},
		{`"kind":"opencode-shell"`, `"kind":"command"`},
	} {
		var bad ToolSnapshot
		if Decode([]byte(strings.Replace(raw, change[0], change[1], 1)), &bad) == nil && bad.Validate() == nil {
			t.Fatal("missing or inexact native Shell evidence was accepted", change[0])
		}
	}
}

func TestOpenCodeShellFailureDoesNotRequireInventedRunningInput(t *testing.T) {
	empty, failure := "", "original rejection"
	end := uint64(1)
	prior := ToolSnapshot{Kind: OpenCodeShellTool, Status: ToolPending, Shell: &OpenCodeShellObservation{CallID: "original", Raw: &empty}}
	next := ToolSnapshot{Kind: OpenCodeShellTool, Status: ToolFailed, Shell: &OpenCodeShellObservation{CallID: "original", Error: &failure, Timing: &OpenCodeToolTiming{Start: 1, End: &end}}}
	if ValidateOpenCodeToolTransition(prior, next) != nil {
		t.Fatal("pending native failure required an invented running observation")
	}
	next.Shell.CallID = "changed"
	if ValidateOpenCodeToolTransition(prior, next) == nil {
		t.Fatal("failed native call replaced the original owner")
	}
}
