// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestOriginalAsyncAssistantMessagesAndQuestions(t *testing.T) {
	for _, fields := range []string{``, `,"delivery":"async"`, `,"questions":[{"title":"q","options":null},{"title":"q2","options":[]},{"title":"q3","options":["B","A"]}]`, `,"delivery":"async","questions":[{"title":"q","options":["B","A"]}]`, `,"delivery":null,"questions":null`, `,"questions":[]`} {
		raw := json.RawMessage(`{"type":"agentMessage","id":"original","text":"Exact","phase":null,"memoryCitation":null` + fields + `}`)
		expected, citation, err := decodeAssistantMessage(raw)
		if err != nil || citation {
			t.Fatal(err)
		}
		if !managedForkItem(raw, "agentMessage") {
			t.Fatal("original history was rejected")
		}
		c, turn := observationClient()
		c.asyncMessages = true
		for _, method := range []string{"item/started", "item/completed"} {
			params := map[string]any{"threadId": c.thread, "turnId": turn, "item": raw}
			if method == "item/started" {
				params["startedAtMs"] = 0
			} else {
				params["completedAtMs"] = 1
			}
			event, err := observeFixture(c, method, params)
			if err != nil || event.Message == nil || !event.Correlated || !domain.SameCodexAssistantMetadata(event.Message.CodexAssistant, expected.CodexAssistant) || c.execution.turns[turn].Turn.Status != TurnRunning {
				t.Fatal("original metadata lost or granted turn completion", err)
			}
		}
	}
}
func TestAssistantMetadataRejectsMalformedAndKeepsLegacyChildBoundary(t *testing.T) {
	for _, fields := range []string{`,"delivery":"sync"`, `,"questions":[{"title":"q"}]`, `,"questions":[{"title":"q","options":null,"extra":1}]`, `,"delivery":"async","delivery":null`} {
		if _, _, err := decodeAssistantMessage([]byte(`{"type":"agentMessage","id":"x","text":"q"` + fields + `}`)); err == nil {
			t.Fatal("malformed item accepted")
		}
	}
	c, turn := observationClient()
	item := map[string]any{"type": "agentMessage", "id": "original", "text": "Original", "delivery": "async", "questions": nil}
	params := map[string]any{"threadId": c.thread, "turnId": turn, "item": item, "completedAtMs": 1}
	event, err := observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("legacy peer gained metadata authority", err)
	}
	c.asyncMessages = true
	params["threadId"] = domain.NewID()
	event, err = observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != NativeExtensionEvent || event.Message != nil {
		t.Fatal("foreign source gained message authority", err)
	}
	params["threadId"] = c.thread
	params["turnId"] = domain.NewID()
	if _, err = observeFixture(c, "item/completed", params); err == nil {
		t.Fatal("foreign turn accepted")
	}
	item["memoryCitation"] = map[string]any{"unimplemented": "original"}
	params["turnId"] = turn
	event, err = observeFixture(c, "item/completed", params)
	if err != nil || event.Kind != NativeExtensionEvent {
		t.Fatal("citation adapter boundary changed", err)
	}
}

func TestAssistantContinuationKeepsRawHistoryWithoutNewAuthority(t *testing.T) {
	raw := json.RawMessage(`{"type":"agentMessage","id":"original-assistant","text":"Original","delivery":"async","questions":[{"title":"q","options":["B","A"]}]}`)
	user := map[string]any{"type": "userMessage", "id": "original-user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "Original input", "text_elements": []any{}}}}
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	turn["itemsView"] = "full"
	turn["items"] = []any{user, raw}
	page, _ := json.Marshal(map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
	original := sha256.Sum256(page)
	if _, inputs, err := decodeLatestTurnInputs(page); err != nil || len(inputs) != 1 || inputs[0].PromptDigest != (domain.SessionInput{Prompt: "Original input"}).InputDigest() || sha256.Sum256(page) != original {
		t.Fatal("history changed or gained message/input authority", err)
	}
	turn["items"] = []any{user, json.RawMessage(`{"type":"agentMessage","id":"original-assistant","text":"Original","delivery":"sync"}`)}
	page, _ = json.Marshal(map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
	if _, _, err := decodeLatestTurnInputs(page); err == nil {
		t.Fatal("malformed retained metadata authorized continuation")
	}
}
