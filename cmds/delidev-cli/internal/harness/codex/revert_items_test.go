// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (f *threadFixture) handleRevertItems(id json.RawMessage, raw json.RawMessage, write func(json.RawMessage, any), capture func()) bool {
	var params struct {
		Thread    domain.ID `json:"threadId"`
		Direction string    `json:"sortDirection"`
		Limit     int       `json:"limit"`
		Cursor    *string   `json:"cursor"`
	}
	if domain.Decode(raw, &params) != nil || params.Thread != f.thread["id"] || params.Limit != 50 || (params.Direction != "asc" && params.Direction != "desc") {
		os.Exit(76)
	}
	capture()
	var history struct {
		Data []json.RawMessage `json:"data"`
	}
	json.Unmarshal(f.history, &history)
	entries := []revertHistoryItem{}
	for _, raw := range history.Data {
		var turn turnWire
		json.Unmarshal(raw, &turn)
		for _, item := range turn.Items {
			entries = append(entries, revertHistoryItem{TurnID: turn.ID, Item: item})
		}
	}
	if f.reverted && strings.Contains(f.mode, "paginated-items-") {
		fault := strings.TrimPrefix(f.mode, "thread-revert-paginated-items-")
		anchor := params.Direction == "desc"
		if fault == "changed-current" && !anchor || fault == "changed-anchor" && anchor {
			for n := range entries {
				var fields map[string]any
				json.Unmarshal(entries[n].Item, &fields)
				if fields["type"] == "commandExecution" {
					fields["aggregatedOutput"] = "changed omitted output"
					entries[n].Item, _ = json.Marshal(fields)
				}
			}
		}
		if anchor && len(entries) > 0 {
			switch fault {
			case "duplicate":
				entries = append(entries, entries[len(entries)-1])
			case "foreign":
				entries[len(entries)-1].TurnID = domain.NewID()
			case "missing":
				entries = entries[:len(entries)-1]
			case "unsupported":
				entries[len(entries)-1].Item = json.RawMessage(`{"id":"unsupported","type":"unknownItem"}`)
			case "running":
				for n := range entries {
					var fields map[string]any
					json.Unmarshal(entries[n].Item, &fields)
					if fields["type"] == "commandExecution" {
						fields["status"] = "inProgress"
						entries[n].Item, _ = json.Marshal(fields)
					}
				}
			case "oversized":
				entries[len(entries)-1].Item, _ = json.Marshal(map[string]any{"id": "oversized", "type": "agentMessage", "text": strings.Repeat("x", 4<<20)})
			}
		}
	}
	if params.Direction == "desc" {
		slices.Reverse(entries)
	}
	offset := 0
	if params.Cursor != nil && strings.HasPrefix(*params.Cursor, "items-page-") {
		offset, _ = strconv.Atoi(strings.TrimPrefix(*params.Cursor, "items-page-"))
	}
	if offset > len(entries) {
		os.Exit(77)
	}
	data := entries[offset:]
	var next any
	if len(data) > 1 {
		data = data[:1]
		next = "items-page-" + strconv.Itoa(offset+1)
	}
	if f.reverted && params.Direction == "desc" && strings.HasSuffix(f.mode, "-cycle") {
		next = "items-anchor"
	}
	if f.reverted && params.Direction == "desc" && strings.HasSuffix(f.mode, "-missing-page") {
		write(id, map[string]any{"data": []any{}, "nextCursor": "items-page-1", "backwardsCursor": nil})
		return true
	}
	write(id, map[string]any{"data": data, "nextCursor": next, "backwardsCursor": nil})
	return true
}

func TestRevertJoinsOriginalToolItemsAndBothCursorDirections(t *testing.T) {
	for _, mode := range []string{"ready", "null", "empty-projection"} {
		for _, index := range []int{0, 2} {
			t.Run(mode+strconv.Itoa(index), func(t *testing.T) {
				c, capture, source, ids, inputs := revertFixture(t, "paginated-items-"+mode)
				var intent RevertIntent
				proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[index], ids[index], func(value RevertIntent) error { intent = value; return nil })
				if err != nil || proof.TurnsCount != uint32(index) || VerifyRevertIntent(intent, proof) != nil {
					t.Fatal("complete original prefix not proved", err)
				}
				if index > 0 {
					var turn turnWire
					json.Unmarshal(intent.ExpectedHistory[0], &turn)
					if len(turn.Items) != 3 {
						t.Fatal("settled command/file items were not frozen")
					}
				}
				itemRequests := requestsOf(t, capture, "thread/items/list")
				desc, asc := 0, 0
				anchor := false
				for _, request := range itemRequests {
					if request["sortDirection"] == "desc" {
						desc++
						anchor = anchor || request["cursor"] == "items-anchor"
					} else {
						asc++
					}
					if request["threadId"] != string(source.ThreadID) {
						t.Fatal("foreign item hydration")
					}
				}
				if desc == 0 || asc == 0 || index > 0 && mode == "ready" && !anchor || len(requestsOf(t, capture, "thread/revert")) != 1 {
					t.Fatal("missing exact item anchor/current proof or repeated mutation")
				}
			})
		}
	}
}

func TestRevertLegacyNonemptyPrefixAllowsNullItemCursor(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "ready")
	if source.PaginatedHistory {
		t.Fatal("fixture did not bind the legacy history mode")
	}
	var intent RevertIntent
	proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(value RevertIntent) error {
		intent = value
		return nil
	})
	if err != nil || proof.TurnsCount != 2 || len(intent.ExpectedHistory) != 2 || VerifyRevertIntent(intent, proof) != nil {
		t.Fatal("legacy full-turn proof rejected a null item cursor", err)
	}
	if len(requestsOf(t, capture, "thread/items/list")) != 0 || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("legacy full-turn proof hydrated items or repeated Revert")
	}
}

func TestRevertItemEvidenceFaultsQuarantineOriginalSend(t *testing.T) {
	for _, fault := range []string{"changed-anchor", "changed-current", "duplicate", "foreign", "missing", "missing-page", "cycle", "unsupported", "running", "oversized"} {
		t.Run(fault, func(t *testing.T) {
			c, capture, source, ids, inputs := revertFixture(t, "paginated-items-"+fault)
			claims := 0
			action := domain.NewID()
			proof, err := c.RevertThread(context.Background(), action, source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil })
			if err == nil || proof.Revert != nil || c.problem == nil || !c.execution.paused || claims != 1 {
				t.Fatal("partial or changed item proof released context")
			}
			if _, err := c.RevertThread(context.Background(), action, source, inputs[2], ids[2], func(RevertIntent) error { claims++; return nil }); err == nil || claims != 1 || len(requestsOf(t, capture, "thread/revert")) != 1 {
				t.Fatal("quarantined original mutation repeated")
			}
		})
	}
}

func TestRevertNonemptyPaginatedPrefixRequiresNativeItemAnchor(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "paginated-items-missing-anchor")
	claims := 0
	if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(RevertIntent) error {
		claims++
		return nil
	}); err == nil || claims != 1 || c.problem == nil || !c.execution.paused {
		t.Fatal("nonempty retained prefix without an item anchor was accepted", err)
	}
	if len(requestsOf(t, capture, "thread/items/list")) != 0 || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("missing item anchor reached retained-item reads or repeated the mutation")
	}
}

func TestRevertItemRecoveryAndReplacementHydrateWithoutAnotherSend(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "paginated-items-lost")
	var intent RevertIntent
	if _, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(value RevertIntent) error { intent = value; return nil }); err == nil {
		t.Fatal("lost acknowledgment released context")
	}
	// Existing recovery fixture clears only the original process failure. Its
	// immutable intent and complete original command/file history stay intact.
	c.problem, c.execution.paused = nil, false
	proof, err := c.ReconcileRevert(context.Background(), intent)
	if err != nil || VerifyRevertIntent(intent, proof) != nil || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("complete original recovery failed or resent", err)
	}
	replacement, replacementCapture := openThreadFixture(t, "thread-revert-paginated-items-ready")
	settings := threadSettings(t)
	settings.Cwd = source.Effective.Cwd
	if _, err := replacement.ResumeThread(context.Background(), domain.NewID(), source.ThreadID, settings); err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, replacement, "history", map[string]any{"page": map[string]any{"data": intent.ExpectedHistory, "nextCursor": nil, "backwardsCursor": nil}})
	if _, err := replacement.VerifyCompactedContinuation(context.Background(), domain.NewID(), proof); err != nil || replacement.execution.continuationPending {
		t.Fatal("replacement did not prove complete original items", err)
	}
	if len(requestsOf(t, replacementCapture, "thread/items/list")) == 0 || len(requestsOf(t, replacementCapture, "thread/revert")) != 0 {
		t.Fatal("replacement omitted hydration or resent")
	}
}

func TestRevertItemJoinBoundsIdentityOrderAndNumbers(t *testing.T) {
	turns := []json.RawMessage{}
	entries := []revertHistoryItem{}
	for n := 0; n < maxForkTurns; n++ {
		id := domain.NewID()
		item, _ := json.Marshal(map[string]any{"id": strconv.Itoa(n), "type": "agentMessage", "text": "original", "number": json.Number("9007199254740993")})
		turn := fixtureTurn(id, TurnCompleted)
		turn["itemsView"], turn["items"] = "full", []json.RawMessage{item}
		raw, _ := json.Marshal(turn)
		turns = append(turns, raw)
		entries = append(entries, revertHistoryItem{TurnID: id, Item: item})
	}
	joined, err := joinRevertHistory(turns, entries)
	if err != nil || len(joined) != maxForkTurns || !strings.Contains(string(joined[0]), "9007199254740993") {
		t.Fatal("bounded original item proof changed", err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func() ([]json.RawMessage, []revertHistoryItem)
	}{
		{"too-many-turns", func() ([]json.RawMessage, []revertHistoryItem) {
			raw := slices.Clone(turns)
			more := slices.Clone(entries)
			id := domain.NewID()
			var fields map[string]any
			json.Unmarshal(turns[0], &fields)
			fields["id"] = id
			fields["items"] = []any{}
			extra, _ := json.Marshal(fields)
			return append(raw, extra), append(more, revertHistoryItem{TurnID: id, Item: json.RawMessage(`{"id":"extra","type":"agentMessage"}`)})
		}},
		{"duplicate", func() ([]json.RawMessage, []revertHistoryItem) {
			return turns, append(slices.Clone(entries), entries[0])
		}},
		{"foreign", func() ([]json.RawMessage, []revertHistoryItem) {
			values := slices.Clone(entries)
			values[0].TurnID = domain.NewID()
			return turns, values
		}},
		{"order", func() ([]json.RawMessage, []revertHistoryItem) {
			values := slices.Clone(entries)
			values[0], values[1] = values[1], values[0]
			return turns, values
		}},
		{"missing", func() ([]json.RawMessage, []revertHistoryItem) { return turns, entries[1:] }},
		{"byte-limit", func() ([]json.RawMessage, []revertHistoryItem) {
			item, _ := json.Marshal(map[string]any{"id": "extra", "type": "agentMessage", "text": strings.Repeat("x", 4<<20)})
			return turns, append(slices.Clone(entries[:1]), revertHistoryItem{TurnID: entries[0].TurnID, Item: item})
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			raw, items := scenario.mutate()
			if _, err := joinRevertHistory(raw, items); err == nil {
				t.Fatal("invalid complete history admitted")
			}
		})
	}
	if result, err := joinRevertHistory([]json.RawMessage{}, []revertHistoryItem{}); err != nil || result == nil || len(result) != 0 {
		t.Fatal("explicit empty proof lost")
	}
	bounded := []revertHistoryItem{entries[0]}
	for n := 1; n < 4096; n++ {
		item, _ := json.Marshal(map[string]any{"id": "bounded-" + strconv.Itoa(n), "type": "agentMessage"})
		bounded = append(bounded, revertHistoryItem{TurnID: entries[0].TurnID, Item: item})
	}
	if _, err := joinRevertHistory(turns[:1], bounded); err != nil {
		t.Fatal("bounded complete item set rejected", err)
	}
	bounded = append(bounded, revertHistoryItem{TurnID: entries[0].TurnID, Item: json.RawMessage(`{"id":"overflow","type":"agentMessage"}`)})
	if _, err := joinRevertHistory(turns[:1], bounded); err == nil {
		t.Fatal("item count overflow admitted")
	}
}
