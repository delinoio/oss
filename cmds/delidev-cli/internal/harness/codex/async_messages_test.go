// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestAsyncMessageRootLifecycleAndExactHistory(t *testing.T) {
	c, turn := observationClient()
	paused := c.execution.paused
	raw := json.RawMessage(`{"type":"agentMessage","id":"async-item","text":"original","phase":"commentary","delivery":"async","questions":[{"title":"First","options":null},{"title":"Second","options":[]},{"title":"Third","options":["b","a"]}]}`)
	for _, method := range []string{"item/started", "item/completed"} {
		params := map[string]any{"threadId": c.thread, "turnId": turn, "item": raw}
		if method == "item/started" {
			params["startedAtMs"] = 1
		} else {
			params["completedAtMs"] = 2
		}
		e, err := observeFixture(c, method, params)
		if err != nil || e.Message == nil || e.Interaction != nil || !e.Correlated || e.TurnID != turn || e.Message.ID != "async-item" {
			t.Fatalf("owned async message lost lifecycle: %v", err)
		}
		a := e.Message.CodexAsyncMessage
		if a == nil || *a.Delivery != "async" || len(a.Questions) != 3 || a.Questions[0].Options != nil || a.Questions[1].Options == nil || strings.Join(a.Questions[2].Options, ",") != "b,a" {
			t.Fatal("ordered nullable provenance changed")
		}
	}
	if !managedForkItem(raw, "agentMessage") {
		t.Fatal("exact history rejected async content")
	}
	turns := []json.RawMessage{json.RawMessage(`{"id":"original-turn","items":[` + string(raw) + `]}`)}
	proof := (&ForkSource{managedToolHistory: true, turns: turns}).Checkpoint().ForkHistory
	if proof == nil || !proof.matches(turns) {
		t.Fatal("original history digest lost")
	}
	changed := []json.RawMessage{json.RawMessage(strings.Replace(string(turns[0]), `["b","a"]`, `["a","b"]`, 1))}
	if proof.matches(changed) {
		t.Fatal("question order did not affect original native digest")
	}
	if c.execution.paused != paused {
		t.Fatal("inert question created pause/reply authority")
	}
}
func TestAsyncMessagePresenceAndStrictBoundaries(t *testing.T) {
	for _, fields := range []string{"", `,"delivery":null,"questions":null`, `,"questions":[]`, `,"delivery":"async"`, `,"questions":[{"title":"q","options":null}]`} {
		raw := json.RawMessage(`{"type":"agentMessage","id":"m","text":"t"` + fields + `}`)
		m, err := decodeAgentMessage(raw)
		if err != nil {
			t.Fatal(err)
		}
		if fields == "" && m.CodexAsyncMessage != nil {
			t.Fatal("invented metadata")
		}
		if fields != "" && m.CodexAsyncMessage == nil {
			t.Fatal("lost native field presence")
		}
	}
	for _, fields := range []string{`,"delivery":"sync"`, `,"delivery":{}`, `,"questions":true`, `,"questions":[{"title":"q"}]`, `,"questions":[{"title":null,"options":null}]`, `,"questions":[{"title":"q","options":[null]}]`, `,"questions":[{"title":"q","options":null,"reply":true}]`, `,"questions":null,"questions":[]`, `,"memoryCitation":{}`, `,"unknown":true`, `,"questions":[{"title":"` + strings.Repeat("x", 4097) + `","options":null}]`} {
		raw := json.RawMessage(`{"type":"agentMessage","id":"m","text":"t"` + fields + `}`)
		if _, err := decodeAgentMessage(raw); err == nil || managedForkItem(raw, "agentMessage") {
			t.Fatal("malformed or unsupported native metadata admitted", fields)
		}
	}
	c, turn := observationClient()
	raw := json.RawMessage(`{"type":"agentMessage","id":"m","text":"t","delivery":"async"}`)
	for _, owner := range []map[string]any{{"threadId": domain.NewID(), "turnId": turn, "item": raw}, {"threadId": c.thread, "turnId": domain.NewID(), "item": raw}} {
		e, err := observeFixture(c, "item/started", owner)
		if err == nil && e.Message != nil {
			t.Fatal("foreign metadata gained message authority")
		}
	}
}

func TestAsyncMessageContinuationValidatesExactOriginal(t *testing.T) {
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	user := map[string]any{"type": "userMessage", "id": "user", "clientId": domain.NewID(), "content": []any{map[string]any{"type": "text", "text": "original input", "text_elements": []any{}}}}
	async := map[string]any{"type": "agentMessage", "id": "async", "text": "original assistant", "delivery": "async", "questions": []any{map[string]any{"title": "q", "options": nil}}}
	turn["items"], turn["itemsView"] = []any{user, async}, "full"
	page := map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}
	raw, _ := json.Marshal(page)
	before := string(raw)
	if _, inputs, err := decodeLatestTurnInputs(raw); err != nil || len(inputs) != 1 || string(raw) != before {
		t.Fatal("complete original async continuation rejected or rewritten", err)
	}
	async["delivery"] = "sync"
	raw, _ = json.Marshal(page)
	if _, _, err := decodeLatestTurnInputs(raw); err == nil {
		t.Fatal("malformed complete-history metadata admitted")
	}
}
