// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Revert has two independent native pagination lanes. Full turn pages remain
// required: the pinned native compatibility path hydrates all their items. The
// separate item stream must prove the same complete ordered history, including
// the response's inclusive backwards anchor, before any checkpoint is usable.
func (c *Client) revertTurnsLocked(ctx context.Context, direction string, turnCursor, itemCursor *string, empty bool) ([]json.RawMessage, error) {
	turns, err := c.contextTurnsLocked(ctx, direction, turnCursor, empty)
	if err != nil {
		return nil, err
	}
	if c.execution.thread.History != PaginatedHistory && itemCursor == nil {
		// Legacy full-item history has no thread/items/list support. This preserves
		// its existing closed null-cursor proof without inferring paginated support.
		entries := []revertHistoryItem{}
		for _, raw := range turns {
			var turn turnWire
			if domain.DecodeBounded(raw, &turn, c.nativeFrameLimit()) != nil {
				return nil, compactionUncertain()
			}
			for _, item := range turn.Items {
				entries = append(entries, revertHistoryItem{Turn: turn.ID, Item: item})
			}
		}
		return joinRevertItems(turns, entries)
	}
	if !c.revertHistory {
		return nil, compactionUncertain()
	}
	entries, err := c.revertItemsLocked(ctx, direction, itemCursor)
	if err != nil {
		return nil, err
	}
	return joinRevertItems(turns, entries)
}

type revertHistoryItem struct {
	Turn      domain.ID       `json:"turnId"`
	Item      json.RawMessage `json:"item"`
	Started   json.RawMessage `json:"startedAtMs"`
	Completed json.RawMessage `json:"completedAtMs"`
}

func (c *Client) revertItemsLocked(ctx context.Context, direction string, cursor *string) ([]revertHistoryItem, error) {
	entries := []revertHistoryItem{}
	seen := map[string]bool{}
	if cursor != nil {
		if domain.Text(*cursor, "native item cursor", 4096, true) != nil {
			return nil, compactionUncertain()
		}
		seen[*cursor] = true
	}
	size := 0
	for {
		reply, err := c.wire.Call(ctx, domain.NewID(), "thread/items/list", struct {
			Thread    domain.ID `json:"threadId"`
			Limit     int       `json:"limit"`
			Direction string    `json:"sortDirection"`
			Cursor    *string   `json:"cursor,omitempty"`
		}{c.thread, 50, direction, cursor})
		var page struct {
			Data []json.RawMessage `json:"data"`
			Next json.RawMessage   `json:"nextCursor"`
			Back json.RawMessage   `json:"backwardsCursor"`
		}
		if err != nil || reply.ErrorCode != nil || domain.DecodeBounded(reply.Result, &page, c.nativeFrameLimit()) != nil || page.Data == nil || len(page.Data) > 50 {
			return nil, compactionUncertain()
		}
		next, err := revertItemCursor(page.Next)
		if err != nil {
			return nil, err
		}
		if _, err := revertItemCursor(page.Back); err != nil {
			return nil, err
		}
		for _, raw := range page.Data {
			size += len(raw)
			var entry revertHistoryItem
			if size > 4<<20 || domain.DecodeBounded(raw, &entry, c.nativeFrameLimit()) != nil || entry.Turn.Validate() != nil || len(entry.Item) == 0 {
				return nil, compactionUncertain()
			}
			start, err := revertItemTimestamp(entry.Started)
			if err != nil {
				return nil, err
			}
			finish, err := revertItemTimestamp(entry.Completed)
			if err != nil || start != nil && finish != nil && *finish < *start {
				return nil, compactionUncertain()
			}
			entries = append(entries, entry)
		}
		if next == nil {
			break
		}
		if len(page.Data) == 0 || seen[*next] {
			return nil, compactionUncertain()
		}
		seen[*next] = true
		cursor = next
	}
	if direction == "desc" {
		slices.Reverse(entries)
	}
	return entries, nil
}

func revertItemCursor(raw json.RawMessage) (*string, error) {
	var cursor *string
	if len(raw) == 0 || json.Unmarshal(raw, &cursor) != nil || cursor != nil && domain.Text(*cursor, "native item cursor", 4096, true) != nil {
		return nil, compactionUncertain()
	}
	return cursor, nil
}

func revertItemTimestamp(raw json.RawMessage) (*int64, error) {
	var value *int64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value != nil && (*value < 0 || *value > 9007199254740991) {
		return nil, compactionUncertain()
	}
	return value, nil
}

// The independent stream is authoritative only when it equals every item in
// the full turn projection. Never fill a partial projection by guessing omitted
// content or accept a turn whose command/file settlement has changed.
func joinRevertItems(turns []json.RawMessage, entries []revertHistoryItem) ([]json.RawMessage, error) {
	if len(turns) > maxForkTurns {
		return nil, compactionUncertain()
	}
	size := 0
	positions := map[domain.ID]int{}
	projected := make([]turnWire, len(turns))
	for index, raw := range turns {
		size += len(raw)
		if size > 4<<20 || domain.DecodeBounded(raw, &projected[index], 4<<20) != nil || projected[index].ID.Validate() != nil || !projected[index].Status.terminal() || projected[index].ItemsView != "full" || len(projected[index].Items) == 0 || positions[projected[index].ID] != 0 {
			return nil, compactionUncertain()
		}
		positions[projected[index].ID] = index + 1
	}
	joined := make([][]json.RawMessage, len(turns))
	seen := map[string]bool{}
	last := 0
	for _, entry := range entries {
		position := positions[entry.Turn]
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if position == 0 || position < last || json.Unmarshal(entry.Item, &item) != nil || domain.Text(item.ID, "native history item", 1024, true) != nil || seen[item.ID] {
			return nil, compactionUncertain()
		}
		switch item.Type {
		case "userMessage", "agentMessage", "reasoning", "plan", "contextCompaction":
		case "commandExecution", "fileChange", "imageView":
			if _, err := decodeTool(entry.Item, item.Type, true); err != nil {
				return nil, compactionUncertain()
			}
		default:
			return nil, compactionUncertain()
		}
		seen[item.ID], last = true, position
		joined[position-1] = append(joined[position-1], entry.Item)
	}
	for index := range turns {
		if !slices.EqualFunc(projected[index].Items, joined[index], equivalentForkJSON) {
			return nil, compactionUncertain()
		}
	}
	return turns, nil
}
