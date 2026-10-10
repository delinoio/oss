// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func sleepFixture(c *Client, turn domain.ID, complete bool) map[string]any {
	p := map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "sleep", "id": "original-sleep", "durationMs": uint64(10)}}
	if complete {
		p["completedAtMs"] = 12
	} else {
		p["startedAtMs"] = 1
	}
	return p
}
func TestSleepLiveLifecycleRetainsDurationWithoutTurnAuthority(t *testing.T) {
	c, turn := observationClient()
	for _, complete := range []bool{false, true} {
		method, kind, status := "item/started", ToolStartedEvent, ToolRunning
		if complete {
			method, kind, status = "item/completed", ToolCompletedEvent, ToolCompleted
		}
		e, err := observeFixture(c, method, sleepFixture(c, turn, complete))
		if err != nil || e.Kind != kind || e.Tool == nil || e.Tool.Kind != SleepTool || e.Tool.Status != status || e.Tool.SleepDurationMS == nil || *e.Tool.SleepDurationMS != 10 || e.ItemID != "original-sleep" || !e.Correlated || e.InputID != "" || c.execution.turns[turn].Turn.Status != TurnRunning {
			t.Fatal("sleep altered original turn or duration", err)
		}
	}
}
func TestSleepRejectsMalformedAndForeignObservations(t *testing.T) {
	for _, raw := range []string{`{"type":"sleep","id":"s","durationMs":-1}`, `{"type":"sleep","id":"s","durationMs":1.5}`, `{"type":"sleep","id":"s","durationMs":null}`, `{"type":"sleep","id":"s"}`, `{"type":"sleep","id":"s","durationMs":1,"extra":true}`, `{"type":"sleep","id":"s","durationMs":1,"durationMs":2}`, `{"type":"sleep","id":"s","DurationMs":1}`, `{"type":"sleep","id":"s","durationMs":18446744073709551616}`} {
		if _, err := decodeSleep(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed sleep accepted", raw)
		}
		if managedForkItem(json.RawMessage(raw), "sleep") {
			t.Fatal("malformed sleep entered Fork")
		}
	}
	for _, foreign := range []string{"thread", "turn"} {
		c, turn := observationClient()
		p := sleepFixture(c, turn, false)
		if foreign == "thread" {
			p["threadId"] = domain.NewID()
		} else {
			p["turnId"] = domain.NewID()
		}
		e, err := observeFixture(c, "item/started", p)
		if foreign == "thread" && (err != nil || e.Kind != NativeExtensionEvent) || foreign == "turn" && err == nil {
			t.Fatal("foreign sleep gained ownership", err)
		}
	}
}
func TestSleepFullHistoryIsUnchangedAcrossContextContinuationAndFork(t *testing.T) {
	c, capture, checkpoint, page := continuationFixture(t, "sleep")
	turn := page["data"].([]any)[0].(map[string]any)
	item := map[string]any{"type": "sleep", "id": "original-sleep", "durationMs": uint64(18446744073709551615)}
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
	if !bytes.Contains(contextHistory[0], []byte(`"durationMs":18446744073709551615`)) {
		t.Fatal("native uint64 duration was rounded")
	}
	if len(requestsOf(t, capture, "turn/start")) != 0 || len(requestsOf(t, capture, "turn/steer")) != 0 || len(requestsOf(t, capture, "thread/fork")) != 0 {
		t.Fatal("passive history scheduled native work")
	}
}
