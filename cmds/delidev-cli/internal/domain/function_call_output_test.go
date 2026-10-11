// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestFunctionCallOutputClosedEvidenceStatus(t *testing.T) {
	text := "inert"
	snapshot := ToolSnapshot{Kind: FunctionCallOutputTool, Status: ToolResultObserved, FunctionCallOutput: &FunctionCallOutputObservation{Name: "tool", Text: &text}}
	update := ExecutionToolUpdate{ID: NewID(), NativeID: "result", Snapshot: &snapshot}
	if update.Validate(ExecutionToolCompleted) != nil {
		t.Fatal("valid completion-only evidence rejected")
	}
	for _, kind := range []ExecutionEventKind{ExecutionToolStarted, ExecutionToolUpdated, ExecutionToolOutput, ExecutionToolInput, ExecutionToolPatch} {
		if update.Validate(kind) == nil {
			t.Fatal("result acquired execution lifecycle")
		}
	}
	for _, status := range []ToolStatus{ToolRunning, ToolCompleted, ToolFailed, ToolDeclined} {
		snapshot.Status = status
		if snapshot.Validate() == nil {
			t.Fatal("result invented native outcome")
		}
	}
	snapshot.Status = ToolResultObserved
	snapshot.Sleep = &SleepObservation{}
	if snapshot.Validate() == nil {
		t.Fatal("result accepted executable payload")
	}
}
