// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"reflect"
	"strings"
	"testing"
)

func functionOutputFixture(output any) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "functionCallOutput", "id": "original-output", "name": "original_tool", "namespace": "original_namespace", "output": output})
	return raw
}
func functionOutputParts() []any {
	return []any{
		map[string]any{"type": "input_text", "text": "  original <script>inert()</script> 한글\n"},
		map[string]any{"type": "input_image", "image_url": "https://never-fetch.invalid/private-image-sentinel", "detail": "original"},
		map[string]any{"type": "input_audio", "audio_url": "file:///never-open/private-audio-sentinel"},
		map[string]any{"type": "encrypted_content", "encrypted_content": "private-encrypted-sentinel"},
		map[string]any{"type": "input_text", "text": "after protected part"},
	}
}
func TestFunctionOutputClosedUnionProjectsOrderWithoutRetrievalOrCiphertext(t *testing.T) {
	for _, value := range []any{"original string", []any{}, functionOutputParts(), []any{map[string]any{"type": "input_image", "file_id": "original-private-file-sentinel"}}} {
		raw := functionOutputFixture(value)
		before := append([]byte(nil), raw...)
		output, err := decodeFunctionOutput(raw)
		if err != nil || output.Observation.Validate() != nil || output.ID != "original-output" || output.Observation.Namespace == nil || *output.Observation.Namespace != "original_namespace" || !reflect.DeepEqual(raw, json.RawMessage(before)) {
			t.Fatal("valid original output lost identity or native bytes", err)
		}
		safe, _ := json.Marshal(output)
		for _, private := range []string{"private-image-sentinel", "private-audio-sentinel", "private-encrypted-sentinel", "original-private-file-sentinel"} {
			if strings.Contains(string(safe), private) {
				t.Fatal("private reference/content entered public observation")
			}
		}
		if len(output.Observation.Parts) == 5 {
			kinds := []domain.FunctionOutputPartKind{domain.FunctionOutputText, domain.FunctionOutputImage, domain.FunctionOutputAudio, domain.FunctionOutputEncrypted, domain.FunctionOutputText}
			for i, p := range output.Observation.Parts {
				if p.Kind != kinds[i] {
					t.Fatal("content order changed")
				}
			}
			if output.Observation.Parts[1].Reference != domain.FunctionOutputImageURL || *output.Observation.Parts[1].Detail != domain.FunctionOutputDetailOriginal {
				t.Fatal("image alternative/detail lost")
			}
		}
	}
	for _, detail := range []domain.FunctionOutputImageDetail{domain.FunctionOutputDetailAuto, domain.FunctionOutputDetailLow, domain.FunctionOutputDetailHigh, domain.FunctionOutputDetailOriginal} {
		if _, err := decodeFunctionOutput(functionOutputFixture([]any{map[string]any{"type": "input_image", "file_id": "inert", "detail": detail}})); err != nil {
			t.Fatal("legal image detail rejected", err)
		}
	}
	raw := json.RawMessage(`{"type":"functionCallOutput","id":"original","name":"tool","namespace":null,"output":""}`)
	if output, err := decodeFunctionOutput(raw); err != nil || output.Observation.Namespace != nil || output.Observation.Text == nil || *output.Observation.Text != "" {
		t.Fatal("nullable namespace/empty string changed", err)
	}
}
func TestFunctionOutputRejectsMalformedDuplicateUnknownAndExcessiveUnion(t *testing.T) {
	for _, output := range []any{nil, 12, map[string]any{}, []any{nil}, []any{map[string]any{"type": "input_image", "image_url": "x", "file_id": "y"}}, []any{map[string]any{"type": "input_image", "image_url": "x", "file_id": nil}}, []any{map[string]any{"type": "input_image", "image_url": "x", "detail": nil}}, []any{map[string]any{"type": "input_image", "image_url": "x", "detail": "future"}}, []any{map[string]any{"type": "input_audio", "audio_url": nil}}, []any{map[string]any{"type": "encrypted_content", "encrypted_content": nil}}, []any{map[string]any{"type": "input_text", "text": nil}}, []any{map[string]any{"type": "inputText", "text": "dynamic is distinct"}}, []any{map[string]any{"type": "input_text", "text": "x", "extra": true}}, strings.Repeat("x", domain.MaxMessageText+1)} {
		if _, err := decodeFunctionOutput(functionOutputFixture(output)); err == nil {
			t.Fatal("invalid union accepted", output)
		}
	}
	for _, raw := range []string{
		`{"type":"functionCallOutput","id":"x","name":"t","output":"x"}`,
		`{"type":"functionCallOutput","id":"x","name":"t","namespace":null,"output":"x","output":"y"}`,
		`{"type":"functionCallOutput","id":"x","name":"t","namespace":null,"output":[{"type":"input_text","text":"x","text":"y"}]}`,
		`{"type":"functionCallOutput","id":"x","name":"t","namespace":null,"output":"x","arguments":{}}`,
	} {
		if _, err := decodeFunctionOutput(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed/duplicate output accepted")
		}
	}
	parts := make([]any, domain.MaxArtifactParts+1)
	for i := range parts {
		parts[i] = map[string]any{"type": "input_text", "text": ""}
	}
	if _, err := decodeFunctionOutput(functionOutputFixture(parts)); err == nil {
		t.Fatal("excessive content accepted")
	}
}
func TestFunctionOutputLiveOwnershipAndIndependentTerminalAuthority(t *testing.T) {
	for _, method := range []string{"item/started", "item/completed"} {
		c, turn := observationClient()
		paused := c.execution.paused
		params := map[string]any{"threadId": c.thread, "turnId": turn, "item": functionOutputFixture(functionOutputParts())}
		if method == "item/started" {
			params["startedAtMs"] = int64(1)
		} else {
			params["completedAtMs"] = int64(2)
		}
		event, err := observeFixture(c, method, params)
		if err != nil || event.FunctionOutput == nil || !event.Correlated || event.Late || event.ItemID != "original-output" || c.execution.active != turn || c.execution.turns[turn].Turn.Status != TurnRunning || c.execution.paused != paused {
			t.Fatal("output gained root completion or execution authority", err)
		}
		params["turnId"] = domain.NewID()
		if _, err := observeFixture(c, method, params); err == nil {
			t.Fatal("foreign turn accepted")
		}
		params["turnId"], params["threadId"] = turn, domain.NewID()
		event, err = observeFixture(c, method, params)
		if err != nil || event.Kind != NativeExtensionEvent || event.FunctionOutput != nil {
			t.Fatal("foreign root published", err)
		}
	}
	c, turn := observationClient()
	c.execution.active = ""
	if _, err := c.observeFunctionOutput(nativewire.Event{Method: "item/completed"}, turn, functionOutputFixture("inert")); err == nil {
		t.Fatal("unowned active turn adopted")
	}
}

func TestFunctionOutputFullHistoryRetainsNativeBytesAndRestrictedForkProfiles(t *testing.T) {
	c, capture := openThreadFixture(t, "thread-function-output-history")
	bound, err := c.ResumeThread(context.Background(), domain.NewID(), domain.NewID(), threadSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	user := map[string]any{"type": "userMessage", "id": "original-user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "original prompt", "text_elements": []any{}}}}
	rawOutput := functionOutputFixture(functionOutputParts())
	turn["itemsView"], turn["items"] = "full", []any{user, rawOutput}
	page := map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	rawPage, _ := json.Marshal(page)
	if _, inputs, err := decodeLatestTurnInputs(rawPage, c.nativeImageInput); err != nil || len(inputs) != 1 {
		t.Fatal("latest continuation history rejected typed inert output", err)
	}
	var baseline []json.RawMessage
	for _, direction := range []string{"asc", "desc"} {
		turns, err := c.contextTurnsLocked(context.Background(), direction, nil, false)
		if err != nil || len(turns) != 1 || !strings.Contains(string(turns[0]), "private-encrypted-sentinel") || !strings.Contains(string(turns[0]), "private-image-sentinel") {
			t.Fatal("full context projection discarded original private history", err)
		}
		if baseline != nil && historyDigest(baseline) != historyDigest(turns) {
			t.Fatal("ascending/descending history changed original digest")
		}
		baseline = turns
	}
	if _, err := c.forkTurnsLocked(context.Background(), bound.Thread.ID); err == nil {
		t.Fatal("restricted API Fork adopted rich output")
	}
	c.managedForkHistory = true
	forked, err := c.forkTurnsLocked(context.Background(), bound.Thread.ID)
	if err != nil || historyDigest(forked) != historyDigest(baseline) {
		t.Fatal("managed whitelist or secondary Fork gate rejected original typed output", err)
	}
	checkpoint := (&ForkSource{managedToolHistory: true, turns: forked}).Checkpoint()
	if checkpoint.ForkHistory == nil || !checkpoint.ForkHistory.matches(baseline) {
		t.Fatal("managed Fork lost complete original history")
	}
	changed := []json.RawMessage{json.RawMessage(strings.ReplaceAll(string(baseline[0]), "private-encrypted-sentinel", "changed-encrypted-sentinel"))}
	if historyDigest(changed) == historyDigest(baseline) || checkpoint.ForkHistory.matches(changed) || equivalentForkJSON(baseline[0], changed[0]) {
		t.Fatal("opaque private bytes were replaced by the safe projection")
	}
	badOutput := json.RawMessage(strings.ReplaceAll(string(rawOutput), `"input_audio"`, `"unknown_audio"`))
	turn["items"] = []any{user, badOutput}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.contextTurnsLocked(context.Background(), "asc", nil, false); err == nil {
		t.Fatal("context adopted unknown output")
	}
	if _, err := c.forkTurnsLocked(context.Background(), bound.Thread.ID); err == nil || managedForkItem(badOutput, "functionCallOutput") || functionOutputForkItem(badOutput, "functionCallOutput") {
		t.Fatal("managed output whitelist accepted unknown output")
	}
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "thread/fork")) != 0 {
		t.Fatal("history observation sent execution or Fork")
	}
}
