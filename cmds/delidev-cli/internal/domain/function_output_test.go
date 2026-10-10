// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFunctionOutputArtifactSafeProjectionRoundTripAndClosedShape(t *testing.T) {
	text := "original inert text"
	for _, output := range []FunctionOutputObservation{{Name: "tool", Variant: FunctionOutputString, Text: &text}, {Name: "tool", Variant: FunctionOutputStructured, Parts: []FunctionOutputPart{}}, {Name: "tool", Variant: FunctionOutputStructured, Parts: []FunctionOutputPart{{Kind: FunctionOutputEncrypted}, {Kind: FunctionOutputAudio, Reference: FunctionOutputAudioURL}}}} {
		snapshot := ArtifactSnapshot{Kind: FunctionOutputArtifact, FunctionOutput: &output}
		raw, err := json.Marshal(snapshot)
		var restored ArtifactSnapshot
		if err != nil || Decode(raw, &restored) != nil || restored.Validate() != nil || !reflect.DeepEqual(snapshot, restored) {
			t.Fatal("string/empty/order/null projection lost in durable round trip", err)
		}
	}
	for _, part := range []FunctionOutputPart{{Kind: FunctionOutputEncrypted, Text: &text}, {Kind: FunctionOutputAudio, Text: &text, Reference: FunctionOutputAudioURL}, {Kind: FunctionOutputImage}, {Kind: FunctionOutputText}, {Kind: "unknown"}} {
		output := FunctionOutputObservation{Name: "tool", Variant: FunctionOutputStructured, Parts: []FunctionOutputPart{part}}
		if output.Validate() == nil {
			t.Fatal("invalid public projection granted content or authority")
		}
	}
	raw := []byte(`{"kind":"function-call-output","text":"","summary":null,"content":null,"function_output":{"name":"tool","namespace":null,"variant":"structured","parts":[{"kind":"encrypted_content","encrypted_content":"private"}]}}`)
	var snapshot ArtifactSnapshot
	if Decode(raw, &snapshot) == nil {
		t.Fatal("private ciphertext field accepted in public projection")
	}
}

func TestFunctionOutputAdditiveJSONKeepsLegacyArtifactAndUnknownPeerFences(t *testing.T) {
	legacy := ArtifactSnapshot{Kind: PlanArtifact, Text: "original plan"}
	raw, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(raw), "function_output") {
		t.Fatal("legacy artifact JSON changed")
	}
	text := "original result"
	current := ArtifactSnapshot{Kind: FunctionOutputArtifact, FunctionOutput: &FunctionOutputObservation{Name: "tool", Variant: FunctionOutputString, Text: &text}}
	raw, _ = json.Marshal(current)
	var old struct {
		Kind    ArtifactKind `json:"kind"`
		Text    string       `json:"text"`
		Summary []string     `json:"summary"`
		Content []string     `json:"content"`
	}
	if Decode(raw, &old) == nil {
		t.Fatal("old strict reader silently flattened new function output")
	}
}
