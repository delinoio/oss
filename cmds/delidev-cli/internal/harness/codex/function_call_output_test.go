// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestFunctionCallOutputClosedProjection(t *testing.T) {
	for _, body := range []string{`"inert"`, `[]`, `[{"type":"input_text","text":"first"},{"type":"input_image","image_url":"https://invalid.example/unretrieved","detail":"original"},{"type":"input_audio","audio_url":"file:///unopened"},{"type":"encrypted_content","encrypted_content":"PRIVATE_SENTINEL"},{"type":"input_image","file_id":"untouched"}]`} {
		raw := json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":` + body + `}`)
		id, o, err := decodeFunctionCallOutput(raw)
		if err != nil || id != "result" || o.Name != "tool" || o.Namespace != nil {
			t.Fatal(err)
		}
		public, _ := json.Marshal(o)
		if strings.Contains(string(public), "PRIVATE_SENTINEL") || !strings.Contains(string(raw), body) {
			t.Fatal("private evidence altered or published")
		}
		var round domain.FunctionCallOutputObservation
		if domain.Decode(public, &round) != nil || round.Validate() != nil {
			t.Fatal("variant lost in durable round trip")
		}
		if len(o.Content) == 5 && (o.Content[3].Type != domain.FunctionOutputEncrypted || o.InertText() != "first") {
			t.Fatal("ordered safe evidence lost")
		}
	}
}
func TestFunctionCallOutputRejectsMalformed(t *testing.T) {
	for _, body := range []string{`null`, `[{"type":"unknown"}]`, `[{"type":"input_image","image_url":"x","file_id":"y"}]`, `[{"type":"input_image","image_url":"x","detail":null}]`, `[{"type":"input_image","image_url":"x","detail":"bad"}]`, `[{"type":"input_text","text":"a","text":"b"}]`, `[{"type":"encrypted_content","encrypted_content":null}]`, `[{"type":"input_text","text":null}]`, `[{"type":"input_audio","audio_url":"x","extra":1}]`, `"` + strings.Repeat("a", domain.MaxMessageText+1) + `"`} {
		if _, _, err := decodeFunctionCallOutput(json.RawMessage(`{"type":"functionCallOutput","id":"result","name":"tool","namespace":null,"output":` + body + `}`)); err == nil {
			t.Fatal("accepted malformed output")
		}
	}
}
func TestFunctionCallOutputLiveOwnership(t *testing.T) {
	c, turn := observationClient()
	item := map[string]any{"type": "functionCallOutput", "id": "result", "name": "tool", "namespace": "exact namespace", "output": "inert"}
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "completedAtMs": 1}
	event, err := observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != FunctionCallOutputEvent || !event.Correlated || *event.FunctionCallOutput.Namespace != "exact namespace" || c.execution.turns[turn].Turn.Status != TurnRunning {
		t.Fatal("result changed ownership/outcome", err)
	}
	params["threadId"] = domain.NewID()
	event, err = observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != NativeExtensionEvent || event.FunctionCallOutput != nil {
		t.Fatal("foreign output gained authority")
	}
}

func TestFunctionCallOutputHistoryUnchangedAcrossContinuationContextAndFork(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "sleep")
	turn := page["data"].([]any)[0].(map[string]any)
	item := map[string]any{"type": "functionCallOutput", "id": "original-result", "name": "tool", "namespace": nil, "output": []any{map[string]any{"type": "input_text", "text": "first"}, map[string]any{"type": "encrypted_content", "encrypted_content": "PRIVATE_SENTINEL"}}}
	turn["items"] = append(turn["items"].([]any), item)
	page["nextCursor"], page["backwardsCursor"] = nil, nil
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("sleep blocked continuation", err)
	}
	contextHistory, err := c.compactionTurnsLocked(context.Background())
	if err != nil {
		t.Fatal("sleep blocked complete context", err)
	}
	for _, managed := range []bool{false, true} {
		c.managedForkHistory = managed
		forkHistory, err := c.forkTurnsLocked(context.Background(), c.thread)
		if err != nil || len(forkHistory) != 1 || len(contextHistory) != 1 || !bytes.Equal(forkHistory[0], contextHistory[0]) || historyDigest(forkHistory) != historyDigest(contextHistory) {
			t.Fatal("sleep changed Fork/history digest", managed, err)
		}
	}
	if !bytes.Contains(contextHistory[0], []byte(`"encrypted_content":"PRIVATE_SENTINEL"`)) {
		t.Fatal("original encrypted history was altered")
	}
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "thread/fork")) != 0 {
		t.Fatal("passive history scheduled native work")
	}
}
