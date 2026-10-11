// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const richFunctionOutput = `{"type":"functionCallOutput","id":"original-output","name":"fixture_tool","namespace":"original_namespace","output":[{"type":"input_text","text":"<script>inert()</script>"},{"type":"input_image","image_url":"private-image-sentinel","detail":"original"},{"type":"input_image","file_id":"private-file-sentinel"},{"type":"input_audio","audio_url":"private-audio-sentinel"},{"type":"encrypted_content","encrypted_content":"private-encrypted-sentinel"},{"type":"input_text","text":"last"}]}`

func TestFunctionOutputClosedUnionAndPrivateProjection(t *testing.T) {
	id, output, err := decodeFunctionOutput(json.RawMessage(richFunctionOutput))
	if err != nil || id != "original-output" || output.Namespace == nil || *output.Namespace != "original_namespace" || !output.Structured || len(output.Content) != 6 {
		t.Fatal("original identity/order lost", err)
	}
	if output.Content[0].Text == nil || *output.Content[0].Text != "<script>inert()</script>" || output.Content[1].ReferenceKind != domain.FunctionImageURL || output.Content[1].Detail == nil || *output.Content[1].Detail != "original" || output.Content[2].ReferenceKind != domain.FunctionFileID || output.Content[2].Detail != nil || output.Content[3].ReferenceKind != domain.FunctionAudioURL || output.Content[4].Kind != domain.FunctionEncrypted || *output.Content[5].Text != "last" {
		t.Fatal("native content alternatives changed")
	}
	public, _ := json.Marshal(output)
	for _, sentinel := range []string{"private-image-sentinel", "private-file-sentinel", "private-audio-sentinel", "private-encrypted-sentinel"} {
		if bytes.Contains(public, []byte(sentinel)) {
			t.Fatal("private content escaped projection")
		}
	}
	for _, raw := range []string{`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":""}`, `{"type":"functionCallOutput","id":"s","name":"tool","namespace":"","output":[]}`} {
		if _, _, err := decodeFunctionOutput(json.RawMessage(raw)); err != nil {
			t.Fatal("empty legal output lost", err)
		}
	}
}
func TestFunctionOutputRejectsAmbiguousAndUnboundedNativeContent(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(richFunctionOutput, `"namespace":"original_namespace",`, "", 1),
		strings.Replace(richFunctionOutput, `"namespace"`, `"Namespace"`, 1),
		strings.Replace(richFunctionOutput, `"id":"original-output"`, `"id":"original-output","id":"duplicate"`, 1),
		strings.Replace(richFunctionOutput, `"image_url":"private-image-sentinel"`, `"image_url":"private-image-sentinel","file_id":"conflict"`, 1),
		strings.Replace(richFunctionOutput, `"detail":"original"`, `"detail":null`, 1),
		strings.Replace(richFunctionOutput, `"detail":"original"`, `"detail":"unknown"`, 1),
		strings.Replace(richFunctionOutput, `"input_audio"`, `"unknown"`, 1),
		strings.Replace(richFunctionOutput, `"audio_url":"private-audio-sentinel"`, `"audio_url":null`, 1),
		strings.Replace(richFunctionOutput, `"encrypted_content":"private-encrypted-sentinel"`, `"encrypted_content":"private-encrypted-sentinel","permission":"allow"`, 1),
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":null}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":"\ud800"}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":{}}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":"` + strings.Repeat("x", domain.MaxMessageText+1) + `"}`,
	} {
		if _, _, err := decodeFunctionOutput(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed output accepted")
		}
		if managedForkItem(json.RawMessage(raw), "functionCallOutput") {
			t.Fatal("malformed output entered Fork")
		}
	}
}
func functionOutputFixture(c *Client, turn domain.ID, complete bool, raw string) map[string]any {
	p := map[string]any{"threadId": c.thread, "turnId": turn, "item": json.RawMessage(raw)}
	if complete {
		p["completedAtMs"] = 12
	} else {
		p["startedAtMs"] = 1
	}
	return p
}
func TestFunctionOutputKeepsOriginalLifecycleWithoutTurnAuthority(t *testing.T) {
	c, turn := observationClient()
	for _, complete := range []bool{false, true} {
		method := "item/started"
		stage := domain.FunctionOutputStarted
		if complete {
			method = "item/completed"
			stage = domain.FunctionOutputCompleted
		}
		e, err := observeFixture(c, method, functionOutputFixture(c, turn, complete, richFunctionOutput))
		if err != nil || e.Kind != FunctionOutputEvent || e.FunctionOutput == nil || e.FunctionOutputStage != stage || e.Tool != nil || e.InputID != "" || !e.Correlated || c.execution.turns[turn].Turn.Status != TurnRunning {
			t.Fatal("result granted execution authority", err)
		}
	}
	if _, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, richFunctionOutput)); err == nil {
		t.Fatal("duplicate completion accepted")
	}
	c, turn = observationClient()
	if _, err := observeFixture(c, "item/started", functionOutputFixture(c, turn, false, richFunctionOutput)); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(richFunctionOutput, "private-encrypted-sentinel", "changed-encrypted-sentinel", 1)
	if _, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, changed)); err == nil {
		t.Fatal("private ciphertext substitution accepted")
	}
	for _, foreign := range []string{"thread", "turn"} {
		c, turn := observationClient()
		p := functionOutputFixture(c, turn, true, richFunctionOutput)
		if foreign == "thread" {
			p["threadId"] = domain.NewID()
		} else {
			p["turnId"] = domain.NewID()
		}
		e, err := observeFixture(c, "item/completed", p)
		if foreign == "thread" && (err != nil || e.Kind != NativeExtensionEvent) || foreign == "turn" && err == nil {
			t.Fatal("foreign output gained ownership", err)
		}
	}
}
func TestFunctionOutputPreservesProtectedHistoryAndDigests(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "function-output")
	turn := page["data"].([]any)[0].(map[string]any)
	turn["items"] = append(turn["items"].([]any), json.RawMessage(richFunctionOutput))
	page["nextCursor"], page["backwardsCursor"] = nil, nil
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("output blocked continuation", err)
	}
	history, err := c.compactionTurnsLocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		c.managedForkHistory = managed
		fork, err := c.forkTurnsLocked(context.Background(), c.thread)
		if err != nil || len(fork) != 1 || len(history) != 1 || !bytes.Equal(fork[0], history[0]) || historyDigest(fork) != historyDigest(history) {
			t.Fatal("original history changed", managed, err)
		}
	}
	for _, sentinel := range []string{"private-image-sentinel", "private-file-sentinel", "private-audio-sentinel", "private-encrypted-sentinel"} {
		if !bytes.Contains(history[0], []byte(sentinel)) {
			t.Fatal("protected native evidence lost")
		}
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/fork"} {
		if len(requestsOf(t, capture, method)) != 0 {
			t.Fatal("history retrieval scheduled work")
		}
	}
}
