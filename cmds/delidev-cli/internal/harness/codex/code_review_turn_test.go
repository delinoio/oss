// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func TestNativeReviewCorrelatesAlternateChildTurnWithoutReplacingAcknowledgment(t *testing.T) {
	c := codeReviewClientFixture(t)
	a := c.codeReview.turn
	b := domain.NewID()
	c.codeReview.terminal = false
	c.execution.turns = map[domain.ID]trackedTurn{a: {Turn: Turn{ID: a, Status: TurnRunning}, Mode: domain.PlanMode}}
	c.execution.active = a

	notify := func(method string, params any) Event {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw})
		if err != nil {
			t.Fatalf("observe %s: %v", method, err)
		}
		return observed
	}

	started := notify("turn/started", map[string]any{"threadId": c.thread, "turn": fixtureTurn(b, TurnRunning)})
	if started.TurnID != a || started.Turn == nil || started.Turn.ID != a || !started.Correlated || c.execution.active != a || c.codeReview.childTurn != b {
		t.Fatal("alternate child turn replaced the acknowledged review turn")
	}

	tool := notify("item/commandExecution/outputDelta", map[string]any{"threadId": c.thread, "turnId": b, "itemId": "tool-1", "delta": "checked"})
	if tool.TurnID != a || !tool.Correlated || tool.ToolOutputKind != CommandTool {
		t.Fatal("alternate child tool event was not correlated to the review assignment")
	}

	usage := notify("rawResponse/completed", map[string]any{"threadId": c.thread, "turnId": b, "responseId": "child-response"})
	if usage.TurnID != a || !usage.Correlated || usage.ResponseUsage == nil || c.codeReview.turn != a || c.execution.active != a {
		t.Fatal("alternate child response usage was not normalized to the acknowledged review turn")
	}

	rawItem := notify("rawResponseItem/completed", map[string]any{"threadId": c.thread, "turnId": b, "item": map[string]any{"type": "message"}})
	if rawItem.TurnID != a || !rawItem.Correlated || rawItem.Metadata != RawSupplementDiscarded || c.codeReview.turn != a || c.execution.active != a {
		t.Fatal("alternate child raw item was not normalized to the acknowledged review turn")
	}

	item := map[string]any{"type": "enteredReviewMode", "id": "entry", "review": "current changes"}
	entered := notify("item/started", map[string]any{"threadId": c.thread, "turnId": a, "item": item, "startedAtMs": int64(1)})
	if entered.TurnID != a || entered.Metadata != NativeReviewItemPending {
		t.Fatal("review lifecycle item lost its acknowledged turn identity")
	}
	if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "item/started", Params: mustJSON(t, map[string]any{"threadId": c.thread, "turnId": b, "item": item, "startedAtMs": int64(1)})}); err == nil {
		t.Fatal("review lifecycle item was accepted on the alternate tool turn")
	}
	if _, err := c.observeEventLocked(nativewire.Event{Kind: nativewire.Notification, Method: "turn/completed", Params: mustJSON(t, map[string]any{"threadId": c.thread, "turn": fixtureTurn(b, TurnCompleted)})}); err == nil {
		t.Fatal("alternate child turn was accepted as the review completion")
	}
}
