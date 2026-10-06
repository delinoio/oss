// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func contextItem(c *Client, turn domain.ID, item string, started bool, stamp int64) map[string]any {
	v := map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "contextCompaction", "id": item}}
	if started {
		v["startedAtMs"] = stamp
	} else {
		v["completedAtMs"] = stamp
	}
	return v
}

func TestCompactionContextItemsRetainOriginalAutomaticTurn(t *testing.T) {
	c, turn := observationClient()
	for _, step := range []struct {
		method string
		stage  CompactionStage
		start  bool
		stamp  int64
	}{{"item/started", CompactionStarted, true, 10}, {"item/completed", CompactionCompleted, false, 12}} {
		event, err := observeFixture(c, step.method, contextItem(c, turn, "original-item", step.start, step.stamp))
		if err != nil || event.Kind != CompactionEvent || !event.Correlated || event.Compaction == nil || event.Compaction.Stage != step.stage || event.Compaction.Trigger != AutomaticCompaction || event.Compaction.ActionID != "" || event.Message != nil || event.Usage != nil || event.ResponseUsage != nil || event.InputID != "" {
			t.Fatal("native context became input/content/usage authority", err)
		}
	}
}

func TestCompactionRejectsUnownedOrPrematureContextCompletion(t *testing.T) {
	for _, mode := range []string{"missing-start", "foreign-turn", "changed-item", "earlier-time", "changed-repeat", "open-terminal", "new-turn-without-claim"} {
		t.Run(mode, func(t *testing.T) {
			c, turn := observationClient()
			if mode != "missing-start" && mode != "new-turn-without-claim" {
				if _, err := observeFixture(c, "item/started", contextItem(c, turn, "original-item", true, 10)); err != nil {
					t.Fatal(err)
				}
			}
			params := contextItem(c, turn, "original-item", false, 12)
			method := "item/completed"
			switch mode {
			case "foreign-turn":
				params["turnId"] = domain.NewID()
			case "changed-item":
				params["item"] = map[string]any{"type": "contextCompaction", "id": "changed-item"}
			case "earlier-time":
				params["completedAtMs"] = 9
			case "changed-repeat":
				method = "item/started"
				params = contextItem(c, turn, "original-item", true, 11)
			case "open-terminal":
				method = "turn/completed"
				params = map[string]any{"threadId": c.thread, "turn": fixtureTurn(turn, TurnCompleted)}
			case "new-turn-without-claim":
				method = "turn/started"
				params = map[string]any{"threadId": c.thread, "turn": fixtureTurn(domain.NewID(), TurnRunning)}
			}
			if _, err := observeFixture(c, method, params); err == nil {
				t.Fatal("unowned native context accepted")
			}
		})
	}
}

func TestCompactionForeignThreadRetainsPrivateExtension(t *testing.T) {
	c, turn := observationClient()
	v := contextItem(c, turn, "private-foreign-item", true, 10)
	v["threadId"] = domain.NewID()
	event, err := observeFixture(c, "item/started", v)
	if err != nil || event.Kind != NativeExtensionEvent || event.Compaction != nil || len(c.execution.compactionItems) != 0 {
		t.Fatal("foreign context entered original root", err)
	}
}
