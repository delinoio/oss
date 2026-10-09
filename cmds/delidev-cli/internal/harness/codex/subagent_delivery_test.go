// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func ownedDeliveryChild(t *testing.T) (*Client, domain.ID, domain.ID) {
	t.Helper()
	c, turn := observationClient()
	c.execution.paused = false
	child := domain.NewID()
	spawn := map[string]any{"type": "collabAgentToolCall", "id": "original-spawn", "tool": "spawnAgent", "status": "completed", "senderThreadId": c.thread, "receiverThreadIds": []domain.ID{child}, "agentsStates": map[string]any{string(child): map[string]any{"status": "running", "message": nil}}}
	event, err := observeFixture(c, "item/completed", map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 1, "item": spawn})
	if err != nil || len(event.Subagents) != 1 {
		t.Fatal("original spawn not proved", err)
	}
	return c, turn, child
}

func childDeliveryItem() map[string]any {
	return map[string]any{"type": "agentMessage", "id": "child-message", "text": "Child fixture finished.", "phase": "final_answer", "memoryCitation": nil}
}

func TestOwnedChildAgentMessageNullableDelivery(t *testing.T) {
	for _, delivery := range []string{"omitted", "null", "populated", "unknown-field"} {
		t.Run(delivery, func(t *testing.T) {
			c, turn, child := ownedDeliveryChild(t)
			item := childDeliveryItem()
			switch delivery {
			case "null":
				item["delivery"] = nil
			case "populated":
				item["delivery"] = map[string]any{"private_delivery": "synthetic"}
			case "unknown-field":
				item["unexpected"] = nil
			}
			event, err := observeFixture(c, "item/completed", map[string]any{"threadId": child, "turnId": domain.NewID(), "completedAtMs": 1, "item": item})
			if delivery == "unknown-field" {
				if err == nil {
					t.Fatal("unknown field accepted")
				}
				return
			}
			if err != nil {
				t.Fatal("supported nullable delivery rejected", err)
			}
			if delivery == "populated" {
				if event.Kind != NativeExtensionEvent || len(event.Subagents) != 0 {
					t.Fatal("opaque delivery became child output")
				}
				return
			}
			if event.Kind != SubagentEvent || len(event.Subagents) != 1 {
				t.Fatal("original child observation lost")
			}
			o := event.Subagents[0]
			if o.NativeID != string(child) || o.ParentID != string(c.thread) || o.SourceID != "child-message" || o.Output == nil || !o.Output.Partial || o.Output.NativeMessageID != "child-message" || o.Output.Text != "Child fixture finished." || o.ObservedModel != nil || o.Usage != nil {
				t.Fatal("child output identity or unavailable telemetry changed")
			}
			if c.problem != nil || c.execution.paused || c.execution.active != turn {
				t.Fatal("nullable delivery changed root execution")
			}
		})
	}
}

func TestChildNullableDeliveryThroughNextEventPreservesRootAndOwnership(t *testing.T) {
	c, _ := openSubagentFixture(t, "thread-subagent-live")
	if _, err := c.StartThread(context.Background(), domain.NewID(), threadSettings(t)); err != nil {
		t.Fatal(err)
	}
	turn := domain.NewID()
	c.execution.active = turn
	c.execution.turns[turn] = trackedTurn{Turn: Turn{ID: turn, Status: TurnRunning}}
	inspected, err := c.InspectDescendants(context.Background())
	if err != nil || len(inspected.Subagents) != 2 {
		t.Fatal("original native child inventory unavailable", err)
	}
	original := inspected.Subagents[0]
	item := childDeliveryItem()
	item["delivery"] = nil
	raw, _ := json.Marshal(map[string]any{"threadId": original.NativeID, "turnId": domain.NewID(), "completedAtMs": 1, "item": item})
	c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "item/completed", Params: raw}
	event, err := c.NextEvent(context.Background())
	if err != nil || event.Kind != SubagentEvent || len(event.Subagents) != 1 {
		t.Fatal("canonical child delivery latched recovery", err)
	}
	o := event.Subagents[0]
	if o.ID != original.ID || o.NativeID != original.NativeID || o.ParentID != original.ParentID || o.Status != original.Status || o.Output == nil || o.Output.Text != "Child fixture finished." || o.Usage != nil || o.ObservedModel != nil {
		t.Fatal("current-source child output changed original ownership or invented telemetry")
	}
	if c.problem != nil || c.execution.paused || c.execution.active != turn {
		t.Fatal("healthy root paused on explicit no-delivery")
	}
	// Unknown fields still pass through the original strict failure/recovery path.
	item["unexpected"] = true
	raw, _ = json.Marshal(map[string]any{"threadId": original.NativeID, "turnId": domain.NewID(), "completedAtMs": 1, "item": item})
	c.pendingEvent = &nativewire.Event{Kind: nativewire.Notification, Method: "item/completed", Params: raw}
	if _, err = c.NextEvent(context.Background()); err == nil || c.problem == nil || !c.execution.paused {
		t.Fatal("unknown child fields bypassed original recovery")
	}
}
