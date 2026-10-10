// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestFunctionOutputPreservesPrivateSourceAndSafeOrder(t *testing.T) {
	raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"fixture_tool","namespace":"original","output":[{"type":"input_text","text":"inert output"},{"type":"input_image","image_url":"https://invalid.example/image","detail":"original"},{"type":"input_audio","audio_url":"file:///private/fixture.wav"},{"type":"encrypted_content","encrypted_content":"private-encrypted-sentinel"},{"type":"input_image","file_id":"native-file"}]}`)
	before := sha256.Sum256(raw)
	v, err := decodeFunctionOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(raw) != before || v.Namespace == nil || *v.Namespace != "original" || v.Variant != domain.FunctionOutputStructured || len(v.Content) != 5 {
		t.Fatal("source/order/namespace changed")
	}
	safe, err := json.Marshal(v)
	if err != nil || strings.Contains(string(safe), "private-encrypted-sentinel") || !strings.Contains(string(safe), `"type":"encrypted_content"`) {
		t.Fatal("opaque bytes reached public projection")
	}
	if !managedForkItem(raw, "functionCallOutput") {
		t.Fatal("original function result excluded from settled managed history")
	}
	c, turn := observationClient()
	e, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 10, "item": raw})
	public, marshalErr := json.Marshal(e)
	if err != nil || marshalErr != nil || e.Kind != FunctionOutputCompletedEvent || e.FunctionOutput == nil || !e.Correlated || e.Native != nil || e.Tool != nil || e.Turn != nil || c.execution.active != turn || strings.Contains(string(public), "private-encrypted-sentinel") {
		t.Fatal("result became execution authority or leaked", err)
	}
}
func TestFunctionOutputStringAndEmptyStructuredVariants(t *testing.T) {
	for _, output := range []string{`"exact output"`, `""`, `[]`} {
		raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":` + output + `}`)
		v, err := decodeFunctionOutput(raw)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := json.Marshal(v)
		var retained domain.CodexFunctionOutput
		if err != nil || domain.Decode(saved, &retained) != nil || retained.Validate() != nil {
			t.Fatal("variant lost in durable round-trip")
		}
	}
}
func TestFunctionOutputRejectsMalformedAndBoundedSource(t *testing.T) {
	for _, output := range []string{`null`, `{}`, `[{"type":"unknown"}]`, `[{"type":"input_text","text":null}]`, `[{"type":"input_text","text":"a","text":"b"}]`, `[{"type":"input_audio","audioUrl":"x"}]`, `[{"type":"input_audio","audio_url":null}]`, `[{"type":"encrypted_content","encrypted_content":null}]`, `[{"type":"input_image","image_url":"a","file_id":"b"}]`, `[{"type":"input_image","image_url":"a","detail":null}]`, `[{"type":"input_image","image_url":"a","detail":"unsupported"}]`, `[{"type":"encrypted_content","encrypted_content":"private","extra":true}]`, `"` + strings.Repeat("x", domain.MaxMessageText+1) + `"`} {
		raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":` + output + `}`)
		if _, err := decodeFunctionOutput(raw); err == nil {
			t.Fatal("malformed output accepted")
		}
	}
	for _, extra := range []string{`,"id":"duplicate"`, `,"unexpected":true`} {
		if _, err := decodeFunctionOutput(json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":"ok"` + extra + `}`)); err == nil {
			t.Fatal("ambiguous item accepted")
		}
	}
}
func TestFunctionOutputForeignAndLateOwner(t *testing.T) {
	c, turn := observationClient()
	raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":"ok"}`)
	params := map[string]any{"threadId": domain.NewID(), "turnId": turn, "completedAtMs": 10, "item": raw}
	e, err := observeFixture(c, "item/completed", params)
	if err != nil || e.Kind != NativeExtensionEvent || e.FunctionOutput != nil {
		t.Fatal("foreign result became root evidence", err)
	}
	params["threadId"] = c.thread
	state := c.execution.turns[turn]
	state.Turn.Status = TurnCompleted
	c.execution.turns[turn] = state
	e, err = observeFixture(c, "item/completed", params)
	if err != nil || !e.Late {
		t.Fatal("late result lost original terminal fence", err)
	}
}

func TestFunctionOutputImageAlternativesAndBounds(t *testing.T) {
	for _, detail := range []string{"auto", "low", "high", "original"} {
		for _, reference := range []string{"image_url", "file_id"} {
			raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":[{"type":"input_image","` + reference + `":"inert","detail":"` + detail + `"}]}`)
			if _, err := decodeFunctionOutput(raw); err != nil {
				t.Fatal("legal native image reference rejected", err)
			}
		}
	}
	parts := strings.TrimSuffix(strings.Repeat(`{"type":"input_text","text":""},`, domain.MaxArtifactParts+1), ",")
	if _, err := decodeFunctionOutput(json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":[` + parts + `]}`)); err == nil {
		t.Fatal("excessive ordered parts accepted")
	}
}
