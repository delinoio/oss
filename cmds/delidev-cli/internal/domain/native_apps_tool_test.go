// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestSessionNativeAppsTypedResultsRemainOrderedInertAndBounded(t *testing.T) {
	structured := `{"native_result":true}`
	result := NativeAppToolObservation{AppID: "original", Name: "Original App", ToolName: "read", ArgumentsPresent: true, Result: &NativeAppResult{Content: []string{`{"type":"text","text":"first"}`, `{"type":"resource_link","uri":"https://private.example/result"}`}, StructuredContent: &structured}}
	if result.Validate(ToolCompleted) != nil {
		t.Fatal("original native JSON result surface rejected")
	}
	for _, scenario := range []string{"malformed", "missing-content", "result-before-completion", "foreign-command", "missing-result", "negative-duration"} {
		t.Run(scenario, func(t *testing.T) {
			value := result
			status := ToolCompleted
			snapshot := ToolSnapshot{Kind: NativeAppsTool, Status: status, Apps: &value}
			switch scenario {
			case "malformed":
				value.Result = &NativeAppResult{Content: []string{"incomplete-json"}}
			case "missing-content":
				value.Result = &NativeAppResult{}
			case "result-before-completion":
				snapshot.Status = ToolRunning
			case "foreign-command":
				snapshot.Command = &CommandObservation{Command: "effect"}
			case "missing-result":
				value.Result = nil
			case "negative-duration":
				negative := int64(-1)
				value.DurationMS = &negative
			}
			if snapshot.Validate() == nil {
				t.Fatal("partial or cross-tool result accepted")
			}
		})
	}
	if result.Result.Content[0] != `{"type":"text","text":"first"}` || result.Result.Content[1] != `{"type":"resource_link","uri":"https://private.example/result"}` {
		t.Fatal("original inert result order changed")
	}
}
