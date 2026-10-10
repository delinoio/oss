// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

const functionOutputRich = `{"type":"functionCallOutput","id":"original-result","name":"exact_tool","namespace":null,"output":[{"type":"input_text","text":"<script>inert()</script> 한글"},{"type":"input_image","image_url":"https://private.example/image?credential=private-reference-sentinel","detail":"original"},{"type":"input_image","file_id":"private-file-sentinel"},{"type":"input_audio","audio_url":"data:audio/wav;base64,private-audio-sentinel"},{"type":"encrypted_content","encrypted_content":"private-encrypted-sentinel"},{"type":"input_text","text":"last"}]}`

func TestFunctionCallOutputClosedProjectionAndOriginalHistory(t *testing.T) {
	for _, raw := range []string{functionOutputRich, `{"type":"functionCallOutput","id":"result","name":"tool","namespace":"","output":""}`, `{"type":"functionCallOutput","id":"result","name":"tool","namespace":"exact namespace","output":"inert result"}`, `{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":[{"type":"input_audio","audio_url":""},{"type":"encrypted_content","encrypted_content":""}]}`, `{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":[]}`} {
		original := []byte(raw)
		before := bytes.Clone(original)
		v, err := decodeFunctionOutput(original)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, original) || !managedForkItem(original, "functionCallOutput") {
			t.Fatal("original protected history modified or refused")
		}
		for _, secret := range []string{"private-reference-sentinel", "private-file-sentinel", "private-audio-sentinel", "private-encrypted-sentinel", "https://private.example", "data:audio"} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatal("private bytes escaped projection")
			}
		}
		var projected domain.CodexFunctionOutput
		if domain.Decode(encoded, &projected) != nil || projected.Validate() != nil {
			t.Fatal("public closed projection cannot round trip")
		}
		if raw == functionOutputRich && (len(v.Output.Contents) != 6 || v.Output.Contents[1].Type != domain.CodexFunctionImage || v.Output.Contents[2].ReferenceKind != domain.CodexFunctionFileID || v.Output.Contents[3].Type != domain.CodexFunctionAudio || v.Output.Contents[4].Type != domain.CodexFunctionEncrypted || v.Output.InertText() != "<script>inert()</script> 한글last" || v.Namespace != nil) {
			t.Fatal("variant/order/null namespace lost")
		}
		turn, input := domain.NewID(), domain.NewID()
		history := json.RawMessage(`{"id":"` + string(turn) + `","status":"completed","error":null,"itemsView":"full","items":[{"type":"userMessage","id":"original-input","clientId":"` + string(input) + `","content":[{"type":"text","text":"original prompt","text_elements":[]} ]},` + raw + `]}`)
		if _, _, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{history})); err != nil {
			t.Fatal("complete continuation history refused", err)
		}
		proof := (&ForkSource{managedToolHistory: true, turns: []json.RawMessage{history}}).Checkpoint().ForkHistory
		if proof == nil || !proof.matches([]json.RawMessage{history}) || proof.matches([]json.RawMessage{bytes.Replace(history, []byte(`"name":"`), []byte(`"name":"changed-`), 1)}) {
			t.Fatal("original full history digest lost")
		}
	}
}
func TestFunctionCallOutputRejectsClosedShapeAndBounds(t *testing.T) {
	valid := `{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":%s}`
	for _, output := range []string{`null`, `{}`, `42`, `[{"type":"unknown"}]`, `[{"type":"input_text","text":null}]`, `[{"type":"input_text","text":"a","extra":true}]`, `[{"type":"input_text","text":"a","text":"b"}]`, `[{"type":"input_image","image_url":"a","file_id":"b"}]`, `[{"type":"input_image","image_url":null,"file_id":"b"}]`, `[{"type":"input_image","image_url":"a","detail":null}]`, `[{"type":"input_image","image_url":"a","detail":"future"}]`, `[{"type":"input_audio","audio_url":null}]`, `[{"type":"encrypted_content","encrypted_content":null}]`, `[{"type":"input_text","text":"` + strings.Repeat("x", domain.MaxMessageText+1) + `"}]`, `"` + strings.Repeat("x", domain.MaxMessageText+1) + `"`} {
		raw := []byte(strings.Replace(valid, "%s", output, 1))
		if _, err := decodeFunctionOutput(raw); err == nil || managedForkItem(raw, "functionCallOutput") {
			t.Fatal("unsupported output admitted", len(raw))
		}
	}

	for _, output := range []string{`[` + strings.TrimSuffix(strings.Repeat(`{"type":"encrypted_content","encrypted_content":"x"},`, domain.MaxCodexFunctionContents+1), ",") + `]`, `[{"type":"input_text","text":"` + strings.Repeat("x", domain.MaxMessageText/2) + `"},{"type":"input_text","text":"` + strings.Repeat("x", domain.MaxMessageText/2+1) + `"}]`} {
		raw := []byte(strings.Replace(valid, "%s", output, 1))
		if _, err := decodeFunctionOutput(raw); err == nil {
			t.Fatal("aggregate or count bound ignored")
		}
	}
	for _, raw := range []string{strings.Replace(functionOutputRich, `"namespace":null,`, "", 1), strings.Replace(functionOutputRich, `"namespace":null`, `"namespace":null,"namespace":"other"`, 1), functionOutputRich + ` {}`, strings.Replace(functionOutputRich, `"type":"functionCallOutput"`, `"type":"functionCallOutput","extra":true`, 1)} {
		if _, err := decodeFunctionOutput([]byte(raw)); err == nil {
			t.Fatal("invalid envelope admitted")
		}
	}
}
func TestFunctionCallOutputLiveOriginalOwnershipAndNoAuthority(t *testing.T) {
	for _, method := range []string{"item/started", "item/completed"} {
		c, turn := observationClient()
		before := c.execution.active
		params := map[string]any{"threadId": c.thread, "turnId": turn, "item": json.RawMessage(functionOutputRich)}
		if method == "item/started" {
			params["startedAtMs"] = int64(1)
		} else {
			params["completedAtMs"] = int64(2)
		}
		event, err := observeFixture(c, method, params)
		if err != nil || event.Kind != FunctionOutputEvent || !event.Correlated || event.FunctionOutput == nil || event.ItemID != "original-result" || c.execution.active != before || c.execution.turns[turn].Turn.Status != TurnRunning {
			t.Fatal("result gained root completion or lost original ownership", err)
		}
		params["threadId"] = domain.NewID()
		event, err = observeFixture(c, method, params)
		if err != nil || event.Kind != NativeExtensionEvent || event.FunctionOutput != nil {
			t.Fatal("foreign thread gained output ownership", err)
		}
		params["threadId"], params["turnId"] = c.thread, domain.NewID()
		if _, err = observeFixture(c, method, params); err == nil {
			t.Fatal("unknown turn adopted")
		}
		params["turnId"] = turn
		c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnCompleted}}
		event, err = observeFixture(c, method, params)
		if err != nil || !event.Late {
			t.Fatal("late history gained live authority", err)
		}
	}
}
