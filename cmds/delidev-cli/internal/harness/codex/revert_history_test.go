// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRevertHydratesCombinedTurnAndItemPagesIncludingEmptyPrefix(t *testing.T) {
	for _, index := range []int{0, 1, 2} {
		t.Run(string(rune('0'+index)), func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, "paginated-items")
			claims := 0
			var intent RevertIntent
			proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[index], ids[index], func(v RevertIntent) error { claims++; intent = v; return nil })
			if err != nil || proof.Revert == nil || proof.TurnsCount != uint32(index) || claims != 1 || len(intent.ExpectedHistory) != index {
				t.Fatal("complete original prefix was not proved", err)
			}
			if proof.HistoryDigest != historyDigest(intent.ExpectedHistory) || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("checkpoint changed original history or repeated mutation")
			}
			itemCalls := requestsOf(t, capture, "thread/items/list")
			descending, ascending := 0, 0
			anchored := false
			for _, call := range itemCalls {
				if call["threadId"] != string(source.ThreadID) || call["turnId"] != nil || call["limit"] != float64(50) {
					t.Fatal("item hydration changed native thread scope or filter")
				}
				if call["sortDirection"] == "desc" {
					descending++
					if call["cursor"] == "revert-item-anchor" {
						anchored = true
					}
				} else {
					ascending++
				}
			}
			wantRetainedItems := index
			if index > 0 {
				wantRetainedItems += 2
			}
			wantDescending := wantRetainedItems
			if wantDescending == 0 {
				wantDescending = 1
			}
			if descending != wantDescending || ascending != 5+wantDescending || anchored != (index > 0) {
				t.Fatalf("incomplete item lane: desc=%d asc=%d anchor=%v", descending, ascending, anchored)
			}
			if _, err := c.RevertThread(context.Background(), intent.ActionID, source, inputs[index], ids[index], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("proved replacement admitted another native Revert")
			}
		})
	}
}

func TestRevertItemEvidenceFailureQuarantinesWithoutCheckpointOrResend(t *testing.T) {
	for _, mode := range []string{"changed", "current-changed", "duplicate", "foreign", "missing", "missing-page", "missing-cursor", "cycle", "running", "reordered"} {
		t.Run(mode, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, "paginated-items-"+mode)
			claims := 0
			proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
			if err == nil || proof.Revert != nil || proof.HistoryDigest != "" || c.problem == nil || !c.execution.paused || claims != 1 {
				t.Fatal("partial item proof released original action quarantine")
			}
			if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("item proof failure admitted another mutation")
			}
		})
	}
}

func revertJoinFixtureTurn(t *testing.T, id domain.ID, item json.RawMessage) json.RawMessage {
	t.Helper()
	turn := fixtureTurn(id, TurnCompleted)
	turn["itemsView"], turn["items"] = "full", []json.RawMessage{item}
	raw, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRevertItemJoinRejectsForeignDuplicateMissingAndReorderedEvidence(t *testing.T) {
	ids := []domain.ID{domain.NewID(), domain.NewID()}
	items := []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"first","text":"original"}`), json.RawMessage(`{"type":"agentMessage","id":"second","text":"original"}`)}
	turns := []json.RawMessage{revertJoinFixtureTurn(t, ids[0], items[0]), revertJoinFixtureTurn(t, ids[1], items[1])}
	valid := []revertHistoryItem{{Turn: ids[0], Item: items[0]}, {Turn: ids[1], Item: items[1]}}
	if _, err := joinRevertItems(turns, valid); err != nil {
		t.Fatal("ordinary full item join rejected", err)
	}
	cases := map[string][]revertHistoryItem{
		"missing":     valid[:1],
		"foreign":     {{Turn: domain.NewID(), Item: items[0]}, valid[1]},
		"duplicate":   {valid[0], valid[0], valid[1]},
		"reordered":   {valid[1], valid[0]},
		"changed":     {{Turn: ids[0], Item: json.RawMessage(`{"type":"agentMessage","id":"first","text":"changed"}`)}, valid[1]},
		"unsupported": {{Turn: ids[0], Item: json.RawMessage(`{"type":"futureItem","id":"first"}`)}, valid[1]},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := joinRevertItems(turns, entries); err == nil {
				t.Fatal("incomplete or changed item history joined")
			}
		})
	}
	if _, err := joinRevertItems([]json.RawMessage{turns[0], revertJoinFixtureTurn(t, ids[1], items[0])}, []revertHistoryItem{valid[0], {Turn: ids[1], Item: items[0]}}); err == nil {
		t.Fatal("item identity reused across original turns")
	}
	if _, err := joinRevertItems([]json.RawMessage{}, []revertHistoryItem{}); err != nil {
		t.Fatal("explicit empty prefix rejected", err)
	}
}

func TestRevertJoinedHistoryRetainsTurnAndByteBounds(t *testing.T) {
	turns, entries := []json.RawMessage{}, []revertHistoryItem{}
	for index := 0; index < maxForkTurns; index++ {
		id := domain.NewID()
		raw, _ := json.Marshal(map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": "bounded"})
		turns = append(turns, revertJoinFixtureTurn(t, id, raw))
		entries = append(entries, revertHistoryItem{Turn: id, Item: raw})
	}
	if _, err := joinRevertItems(turns, entries); err != nil {
		t.Fatal("128 original turns rejected", err)
	}
	if _, err := joinRevertItems(append(turns, turns[0]), entries); err == nil {
		t.Fatal("over-bound history accepted")
	}
	turns, entries = nil, nil
	for index := 0; index < 5; index++ {
		id := domain.NewID()
		raw, _ := json.Marshal(map[string]any{"type": "agentMessage", "id": string(domain.NewID()), "text": strings.Repeat("x", 1<<20)})
		turns = append(turns, revertJoinFixtureTurn(t, id, raw))
		entries = append(entries, revertHistoryItem{Turn: id, Item: raw})
	}
	if _, err := joinRevertItems(turns, entries); err == nil {
		t.Fatal("history over 4 MiB accepted")
	}
}

func TestRevertItemPaginationRejectsMissingMalformedAndUnboundedMetadata(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`123`), json.RawMessage(`""`), json.RawMessage(`"` + strings.Repeat("x", 4097) + `"`)} {
		if _, err := revertItemCursor(raw); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`-1`), json.RawMessage(`1.5`), json.RawMessage(`9007199254740992`), json.RawMessage(`"timestamp"`)} {
		if _, err := revertItemTimestamp(raw); err == nil {
			t.Fatal("invalid nullable timestamp accepted")
		}
	}
	if cursor, err := revertItemCursor(json.RawMessage(`null`)); err != nil || cursor != nil {
		t.Fatal("closed null cursor rejected")
	}
	if value, err := revertItemTimestamp(json.RawMessage(`null`)); err != nil || value != nil {
		t.Fatal("closed null timestamp rejected")
	}
}

func TestRevertConsumesReportedItemAnchorEvenWithLegacyMetadata(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "legacy-items-anchor")
	proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[1], ids[1], func(RevertIntent) error { return nil })
	if err != nil || proof.Revert == nil {
		t.Fatal("controlled item anchor proof rejected", err)
	}
	calls := requestsOf(t, capture, "thread/items/list")
	if len(calls) != 3 || calls[0]["cursor"] != "revert-item-anchor" || calls[0]["sortDirection"] != "desc" {
		t.Fatal("reported item cursor was validated but not consumed")
	}
}
