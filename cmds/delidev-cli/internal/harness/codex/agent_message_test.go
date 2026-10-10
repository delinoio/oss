// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAsyncAgentMessageShapes(t *testing.T) {
	for _, extra := range []string{``, `,"delivery":null,"questions":null`, `,"delivery":"async"`, `,"questions":[]`, `,"questions":[{"title":"","options":[""]}]`, `,"questions":[{"title":"First","options":null},{"title":"Second","options":[]},{"title":"Third","options":["one","two"]}]`, `,"delivery":"async","questions":[{"title":"Question","options":["answer"]}]`} {
		raw := json.RawMessage(`{"type":"agentMessage","id":"original","text":"synthetic","phase":null,"memoryCitation":null` + extra + `}`)
		message, private, err := decodeAgentMessage(raw)
		if err != nil || private || message == nil || message.ID != "original" || message.Text != "synthetic" {
			t.Fatalf("shape %s: %v", extra, err)
		}
		if !managedForkItem(raw, "agentMessage") {
			t.Fatal("owned settled message denied original Fork validation")
		}
		if strings.Contains(extra, `"title":"First"`) {
			q := message.Codex.Questions
			if q[0].Options != nil || q[1].Options == nil || len(q[1].Options) != 0 || !reflect.DeepEqual(q[2].Options, []string{"one", "two"}) {
				t.Fatal("nullable/empty/order changed")
			}
		}
	}
}
func TestAsyncAgentMessageRejectsMalformed(t *testing.T) {
	for _, extra := range []string{`,"delivery":"sync"`, `,"delivery":7`, `,"questions":{}`, `,"questions":[null]`, `,"questions":[{"title":"x"}]`, `,"questions":[{"title":"x","options":{},"unknown":true}]`, `,"questions":[{"title":"x","title":"y","options":null}]`, `,"delivery":"async","delivery":null`, `,"unknown":true`, `,"questions":[{"title":"x","options":[null]}]`, `,"questions":[{"title":"` + strings.Repeat("x", (64<<10)+1) + `","options":null}]`} {
		raw := json.RawMessage(`{"type":"agentMessage","id":"original","text":"synthetic","phase":null` + extra + `}`)
		if _, private, err := decodeAgentMessage(raw); err == nil || private {
			t.Fatalf("malformed shape was admitted: %s", extra[:min(len(extra), 100)])
		}
		if managedForkItem(raw, "agentMessage") {
			t.Fatal("malformed native original history admitted")
		}
	}
	if _, private, err := decodeAgentMessage(json.RawMessage(`{"type":"agentMessage","id":"original","text":"synthetic","phase":null,"memoryCitation":{"entries":["private"]}}`)); err != nil || !private {
		t.Fatal("populated citation ownership changed")
	}
}
func TestAsyncRootObservationKeepsOriginalOwnership(t *testing.T) {
	for _, foreign := range []string{"", "thread", "turn"} {
		c, turn := observationClient()
		params := map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": map[string]any{"type": "agentMessage", "id": "original", "text": "synthetic", "delivery": "async", "questions": []any{map[string]any{"title": "question", "options": nil}}}}
		if foreign == "thread" {
			params["threadId"] = domain.NewID()
		}
		if foreign == "turn" {
			params["turnId"] = domain.NewID()
		}
		event, err := observeFixture(c, "item/completed", params)
		if foreign != "" {
			if err == nil && event.Message != nil {
				t.Fatal("foreign observation acquired message authority")
			}
			continue
		}
		if err != nil || event.Kind != MessageCompletedEvent || !event.Correlated || event.ThreadID != c.thread || event.TurnID != turn || event.Message.Codex == nil || event.Interaction != nil || event.RequestID != "" {
			t.Fatal("async observation borrowed question/input authority", err)
		}
	}
}

func TestAsyncAgentMessageCompleteHistoryRetainsNativeBytes(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		native := json.RawMessage(`{"type":"agentMessage","id":"original-agent","text":"synthetic","phase":"final_answer","memoryCitation":null,"delivery":"async","questions":[{"title":"First","options":null},{"title":"Second","options":[]},{"title":"Third","options":["one","two"]}]}`)
		if malformed {
			native = json.RawMessage(`{"type":"agentMessage","id":"original-agent","text":"synthetic","delivery":"other"}`)
		}
		turn := fixtureTurn(domain.NewID(), TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{map[string]any{"type": "userMessage", "id": "original-user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "original prompt", "text_elements": []any{}}}}, native}
		raw, _ := json.Marshal(turn)
		page := marshalForkPage([]json.RawMessage{raw})
		before := bytes.Clone(page)
		digest := sha256.Sum256(page)
		_, inputs, err := decodeLatestTurnInputs(page)
		if (err != nil) != malformed || !malformed && len(inputs) != 1 {
			t.Fatal("original complete history family validation changed", err)
		}
		if !bytes.Equal(before, page) || sha256.Sum256(page) != digest {
			t.Fatal("history validation rebuilt native bytes/digest")
		}
	}
}
