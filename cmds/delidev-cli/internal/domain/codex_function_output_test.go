// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestPublicFunctionOutputRejectsOpaqueBytesAndMixedProgress(t *testing.T) {
	raw := []byte(`{"native_item_id":"original","name":"tool","namespace":null,"variant":"structured","content":[{"type":"encrypted_content","encrypted_content":"private"}]}`)
	var v CodexFunctionOutput
	if Decode(raw, &v) == nil {
		t.Fatal("public decoder accepted an encrypted bytes field")
	}
	v = CodexFunctionOutput{NativeItemID: "original", Name: "tool", Variant: FunctionOutputStructured, Content: []FunctionOutputContent{{Kind: FunctionOutputEncrypted}}}
	p := ExecutionProgressUpdate{ID: NewID(), Progress: NativeProgress{Kind: CodexFunctionOutputProgress, FunctionOutput: &v}}
	if p.Validate() != nil {
		t.Fatal("safe encrypted presence rejected")
	}
	diff := "not result"
	p.Progress.Diff = &diff
	if p.Validate() == nil {
		t.Fatal("result became mixed progress authority")
	}
	p.Progress.Diff = nil
	p.Progress.Kind = PlanProgress
	if p.Validate() == nil {
		t.Fatal("function result accepted outside its closed kind")
	}
}
