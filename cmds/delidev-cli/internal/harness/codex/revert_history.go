// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Revert has a separate complete-item proof. Legacy full turn pages remain the
// closed legacy authority; a returned item cursor never permits that shortcut.
func (c *Client) revertHistoryLocked(ctx context.Context, direction string, turnsCursor, itemsCursor *string, empty bool) ([]json.RawMessage, error) {
	hydrate := c.execution.thread.History == PaginatedHistory || itemsCursor != nil
	turns, err := c.contextTurnPagesLocked(ctx, direction, turnsCursor, empty, hydrate)
	if err != nil {
		return nil, err
	}
	if !hydrate {
		entries := []revertHistoryItem{}
		for _, raw := range turns {
			var wire turnWire
			if json.Unmarshal(raw, &wire) != nil {
				return nil, compactionUncertain()
			}
			for _, item := range wire.Items {
				if len(entries) >= 4096 {
					return nil, compactionUncertain()
				}
				entries = append(entries, revertHistoryItem{TurnID: wire.ID, Item: item})
			}
		}
		return joinRevertHistory(turns, entries, c.historyAggregateLimit())
	}
	entries := []revertHistoryItem{}
	seenItems, seenCursors := map[string]bool{}, map[string]bool{}
	if itemsCursor != nil {
		if domain.Text(*itemsCursor, "native item cursor", 4096, true) != nil {
			return nil, compactionUncertain()
		}
		seenCursors[*itemsCursor] = true
	}
	budget := revertScanBudget{history: c.historyAggregateLimit()}
	for {
		reply, err := c.call(ctx, domain.NewID(), "thread/items/list", struct {
			Thread    domain.ID `json:"threadId"`
			Limit     int       `json:"limit"`
			Direction string    `json:"sortDirection"`
			Cursor    *string   `json:"cursor,omitempty"`
		}{c.thread, 50, direction, itemsCursor})
		var page struct {
			Data []revertHistoryItem `json:"data"`
			Next json.RawMessage     `json:"nextCursor"`
			Back json.RawMessage     `json:"backwardsCursor"`
		}
		if err != nil || reply.ErrorCode != nil || domain.DecodeBounded(reply.Result, &page, c.nativeFrameLimit()) != nil || page.Data == nil || len(page.Data) > 50 || len(page.Next) == 0 || len(page.Back) == 0 {
			return nil, compactionUncertain()
		}
		var next, back *string
		if !budget.admit(len(reply.Result), page.Data) || json.Unmarshal(page.Next, &next) != nil || json.Unmarshal(page.Back, &back) != nil || back != nil && domain.Text(*back, "native item backwards cursor", 4096, true) != nil {
			return nil, compactionUncertain()
		}
		for _, entry := range page.Data {
			id, err := verifyRevertHistoryItem(entry.Item)
			if err != nil || entry.TurnID.Validate() != nil || seenItems[id] || len(entries) >= 4096 || entry.StartedAt != nil && *entry.StartedAt < 0 || entry.CompletedAt != nil && *entry.CompletedAt < 0 || entry.StartedAt != nil && entry.CompletedAt != nil && *entry.CompletedAt < *entry.StartedAt {
				return nil, compactionUncertain()
			}
			seenItems[id] = true
			entries = append(entries, entry)
		}
		if next == nil {
			break
		}
		if len(page.Data) == 0 || domain.Text(*next, "native item continuation cursor", 4096, true) != nil || seenCursors[*next] {
			return nil, compactionUncertain()
		}
		seenCursors[*next] = true
		itemsCursor = next
	}
	if direction == "desc" {
		slices.Reverse(entries)
	}
	return joinRevertHistory(turns, entries, c.historyAggregateLimit())
}

// maxRevertEnvelopeBytes bounds the discarded item-page transport structure of
// one paginated Revert scan, independent of the retained history budget. At most
// 4,096 items in non-empty pages of 50 stay far below it.
const maxRevertEnvelopeBytes = 4 << 20

// revertScanBudget accounts one paginated Revert scan. Retained item bytes follow
// the profile-aware history budget, matching the final joined history. The
// turn IDs, timestamps, cursors and JSON structure of each page are discarded by
// joinRevertHistory, so they only count against their own fixed bound.
type revertScanBudget struct {
	history, items, envelopes int
}

// admit records one decoded page and reports whether both bounds still hold.
func (b *revertScanBudget) admit(resultBytes int, entries []revertHistoryItem) bool {
	pageItems := 0
	for _, entry := range entries {
		pageItems += len(entry.Item)
	}
	b.items += pageItems
	b.envelopes += max(resultBytes-pageItems, 0)
	return b.items <= b.history && b.envelopes <= maxRevertEnvelopeBytes
}

type revertHistoryItem struct {
	TurnID      domain.ID       `json:"turnId"`
	Item        json.RawMessage `json:"item"`
	StartedAt   *int64          `json:"startedAtMs"`
	CompletedAt *int64          `json:"completedAtMs"`
}

func verifyRevertHistoryItem(raw json.RawMessage) (string, error) {
	var item struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &item) != nil || domain.Text(item.ID, "native history item", 1024, true) != nil {
		return "", compactionUncertain()
	}
	switch item.Type {
	case "userMessage", "agentMessage", "reasoning", "plan", "contextCompaction":
	case "commandExecution", "fileChange", "imageView", "sleep":
		if _, err := decodeTool(raw, item.Type, true); err != nil {
			return "", compactionUncertain()
		}
	case "mcpToolCall", "webSearch":
		if !settledExternalTool(raw, item.Type) {
			return "", compactionUncertain()
		}
	case "dynamicToolCall":
		if _, _, err := decodeDynamicTool(raw, true); err != nil {
			return "", compactionUncertain()
		}
	case "functionCallOutput":
		if _, _, err := decodeFunctionOutput(raw); err != nil {
			return "", compactionUncertain()
		}
	case "imageGeneration":
		if _, err := decodeImageGeneration(raw, true); err != nil {
			return "", compactionUncertain()
		}
	default:
		return "", compactionUncertain()
	}
	return item.ID, nil
}

// Item pages must cover every projected item in the same order. Omitted item
// pages are joined, never treated as equality of partial turn projections.
func joinRevertHistory(turns []json.RawMessage, entries []revertHistoryItem, limit int) ([]json.RawMessage, error) {
	if len(turns) > maxForkTurns || len(entries) > 4096 {
		return nil, compactionUncertain()
	}
	indexes := map[domain.ID]int{}
	items := make([][]json.RawMessage, len(turns))
	for n, raw := range turns {
		turn, err := decodeTurn(raw)
		if err != nil || !turn.Status.terminal() || indexes[turn.ID] != 0 { // Store one-based indexes.
			return nil, compactionUncertain()
		}
		indexes[turn.ID] = n + 1
	}
	previous := 0
	seen := map[string]bool{}
	for _, entry := range entries {
		index := indexes[entry.TurnID]
		id, err := verifyRevertHistoryItem(entry.Item)
		if err != nil || seen[id] || len(seen) >= 4096 || index == 0 || index < previous {
			return nil, compactionUncertain()
		}
		seen[id] = true
		previous = index
		items[index-1] = append(items[index-1], entry.Item)
	}
	result := make([]json.RawMessage, 0, len(turns))
	size := 0
	for n, raw := range turns {
		var wire turnWire
		if json.Unmarshal(raw, &wire) != nil || len(items[n]) == 0 {
			return nil, compactionUncertain()
		}
		position := 0
		for _, projected := range wire.Items {
			id, err := verifyRevertHistoryItem(projected)
			if err != nil {
				return nil, err
			}
			found := false
			for position < len(items[n]) {
				candidate := items[n][position]
				position++
				candidateID, err := verifyRevertHistoryItem(candidate)
				if err != nil {
					return nil, err
				}
				if candidateID == id {
					if !equivalentForkJSON(projected, candidate) {
						return nil, compactionUncertain()
					}
					found = true
					break
				}
			}
			if !found {
				return nil, compactionUncertain()
			}
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, compactionUncertain()
		}
		fields["items"], _ = json.Marshal(items[n])
		joined, err := json.Marshal(fields)
		if err != nil {
			return nil, compactionUncertain()
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(joined))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, compactionUncertain()
		}
		normal, err := json.Marshal(value)
		size += len(normal)
		if err != nil || size > limit || len(result) >= maxForkTurns {
			return nil, compactionUncertain()
		}
		result = append(result, normal)
	}
	return result, nil
}
