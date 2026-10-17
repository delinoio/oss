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

func TestExternalToolHistoryPreservesContextForkAndRevert(t *testing.T) {
	for _, item := range []string{
		mcpCompletedFixture,
		strings.Replace(strings.Replace(mcpStartedFixture, `"inProgress"`, `"failed"`, 1), `"error":null`, `"error":{"message":"original failure"}`, 1),
		`{"type":"webSearch","id":"web-call","query":"private-query","action":{"type":"search","query":"private-query"},"results":[{"url":"private-locator"}]}`,
	} {
		var identity struct{ Type, ID string }
		_ = json.Unmarshal([]byte(item), &identity)
		t.Run(identity.Type+identity.ID, func(t *testing.T) {
			c, capture, checkpoint, page := continuationFixture(t, "dynamic-history")
			turn := page["data"].([]any)[0].(map[string]any)
			turn["items"] = append(turn["items"].([]any), json.RawMessage(item))
			page["nextCursor"], page["backwardsCursor"] = nil, nil
			fixtureSignal(t, c, "history", map[string]any{"page": page})
			if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
				t.Fatal(err)
			}
			history, err := c.compactionTurnsLocked(context.Background())
			if err != nil || len(history) != 1 {
				t.Fatal("settled external tool cannot compact", err)
			}
			for _, managed := range []bool{false, true} {
				c.managedForkHistory = managed
				fork, err := c.forkTurnsLocked(context.Background(), c.thread)
				if err != nil || historyDigest(fork) != historyDigest(history) {
					t.Fatal("fork lost original external history", err)
				}
			}
			var wire turnWire
			if json.Unmarshal(history[0], &wire) != nil {
				t.Fatal("invalid fixture turn")
			}
			entries := []revertHistoryItem{}
			for _, raw := range wire.Items {
				entries = append(entries, revertHistoryItem{TurnID: wire.ID, Item: raw})
			}
			joined, err := joinRevertHistory(history, entries, c.historyAggregateLimit())
			if err != nil || historyDigest(joined) != historyDigest(history) {
				t.Fatal("revert lost original external history", err)
			}
			if len(c.dynamicItems) != 0 || len(c.dynamicArrivals) != 0 || !bytes.Contains(history[0], []byte(identity.ID)) {
				t.Fatal("history acquired live tool ownership or lost original identity")
			}
			c.sidechat = ReadOnlySidechatV1
			if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
				t.Fatal("restricted Sidechat adopted external history")
			}
			for _, method := range []string{"turn/start", "turn/steer", "thread/fork"} {
				if len(requestsOf(t, capture, method)) != 0 {
					t.Fatal("history validation dispatched work")
				}
			}
		})
	}
}

func TestExternalToolHistoryRejectsUnsettledMalformedAndOversizedItems(t *testing.T) {
	for _, raw := range []string{
		mcpStartedFixture,
		strings.Replace(mcpCompletedFixture, `"durationMs":9007199254740993`, `"durationMs":-1`, 1),
		strings.Replace(mcpCompletedFixture, `"Original result"`, `true`, 1),
		strings.Replace(mcpCompletedFixture, "Original result", strings.Repeat("x", domain.MaxMessageText+1), 1),
		`{"type":"webSearch","id":"web-call","query":"x","action":{"type":"unknown"}}`,
		`{"type":"webSearch","id":"web-call","query":"","action":null}`,
		`{"type":"webSearch","id":"web-call","query":""}`,
		`{"type":"webSearch","id":"web-call","query":"x","extra":true}`,
		`{"type":"webSearch","id":"web-call","query":"` + strings.Repeat("x", 512<<10) + `"}`,
	} {
		var identity struct{ Type string }
		_ = json.Unmarshal([]byte(raw), &identity)
		if settledExternalTool(json.RawMessage(raw), identity.Type) || managedForkItem(json.RawMessage(raw), identity.Type) {
			t.Fatal("invalid external history became fork eligible")
		}
		if _, err := verifyRevertHistoryItem(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid external history became revert eligible")
		}
		c, _, _, page := continuationFixture(t, "dynamic-history")
		turn := page["data"].([]any)[0].(map[string]any)
		turn["items"] = append(turn["items"].([]any), json.RawMessage(raw))
		page["nextCursor"], page["backwardsCursor"] = nil, nil
		fixtureSignal(t, c, "history", map[string]any{"page": page})
		if _, err := c.compactionTurnsLocked(context.Background()); err == nil {
			t.Fatal("invalid external history became compaction eligible")
		}
		for _, managed := range []bool{false, true} {
			c.managedForkHistory = managed
			if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
				t.Fatal("invalid external history became a fork")
			}
		}
	}
}
