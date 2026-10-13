// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestFunctionOutputPublicUnionCannotCarryMixedAuthority(t *testing.T) {
	text := "inert"
	valid := FunctionOutput{Name: "tool", Text: &text}
	if valid.Validate() != nil {
		t.Fatal("valid string output rejected")
	}
	for _, output := range []FunctionOutput{
		{Name: "tool", Text: &text, Structured: true},
		{Name: "tool", Text: &text, Content: []FunctionOutputContent{}},
		{Name: "tool", Structured: true, Content: []FunctionOutputContent{{Kind: FunctionEncrypted, Text: &text}}},
		{Name: "tool", Structured: true, Content: []FunctionOutputContent{{Kind: FunctionImage, ReferenceKind: FunctionAudioURL}}},
		{Name: "tool", Structured: true, Content: []FunctionOutputContent{{Kind: FunctionOutputKind("unknown")}}},
	} {
		if output.Validate() == nil {
			t.Fatal("mixed or unknown content accepted")
		}
	}
	event := ExecutionEvent{Version: 1, ExecutionID: NewID(), Sequence: 3, Kind: ExecutionFunctionOutputObserved, NativeThreadID: string(NewID()), NativeTurnID: string(NewID()), FunctionOutput: &FunctionOutputUpdate{ID: NewID(), NativeID: "output", Stage: FunctionOutputCompleted, Output: valid}}
	if event.Validate() != nil {
		t.Fatal("valid observation rejected")
	}
	event.Outcome = ExecutionSucceeded
	if event.Validate() == nil {
		t.Fatal("result granted terminal authority")
	}
	event.Outcome = ""
	event.Kind = ExecutionTurnFinished
	event.Outcome = ExecutionSucceeded
	if event.Validate() == nil {
		t.Fatal("turn terminal accepted function content")
	}
}
