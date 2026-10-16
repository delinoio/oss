// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// WebSearch items do not carry a success field. Completion settles the native
// lifecycle without manufacturing a successful remote search observation.
func (c *Client) observeWebToolLocked(ctx context.Context, native nativewire.Event, turn domain.ID, raw json.RawMessage) (Event, error) {
	var item struct {
		Type    string             `json:"type"`
		ID      string             `json:"id"`
		Query   string             `json:"query"`
		Action  json.RawMessage    `json:"action"`
		Results *[]json.RawMessage `json:"results"`
	}
	if len(raw) > 512<<10 || domain.ValidateJSONUnicodeEscapes(raw) != nil || domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "webSearch" || domain.Text(item.ID, "native web item", 1024, true) != nil || domain.Text(item.Query, "native web query", domain.MaxMessageText, false) != nil {
		return Event{}, incompatible()
	}
	if externalValuePresent(item.Action) {
		var action struct {
			Type    string    `json:"type"`
			Query   *string   `json:"query"`
			Queries *[]string `json:"queries"`
			URL     *string   `json:"url"`
			Pattern *string   `json:"pattern"`
		}
		if domain.Decode(item.Action, &action) != nil {
			return Event{}, incompatible()
		}
		switch action.Type {
		case "search":
			if action.URL != nil || action.Pattern != nil {
				return Event{}, incompatible()
			}
		case "openPage":
			if action.Query != nil || action.Queries != nil || action.Pattern != nil {
				return Event{}, incompatible()
			}
		case "findInPage":
			if action.Query != nil || action.Queries != nil {
				return Event{}, incompatible()
			}
		case "other":
			if action.Query != nil || action.Queries != nil || action.URL != nil || action.Pattern != nil {
				return Event{}, incompatible()
			}
		default:
			return Event{}, incompatible()
		}
	}
	action, _ := json.Marshal(struct {
		Query  string          `json:"query"`
		Action json.RawMessage `json:"action"`
	}{item.Query, item.Action})
	// Standalone search starts with an empty query and no action, then fills
	// both in the completion. The lifecycle has no input arguments at start;
	// retain its empty-argument identity and commit the final action separately.
	identity, err := dynamicIdentity("web_search", nil, json.RawMessage(`{}`))
	if err != nil {
		return Event{}, err
	}
	snapshot := &domain.DynamicToolSnapshot{Identity: identity, Status: domain.DynamicRunning}
	if native.Method == "item/completed" {
		snapshot.Status = domain.DynamicDone
		text := "Web action retained in native history (sha256: " + externalToolDigest(action) + ")."
		content := []domain.DynamicToolContent{{Kind: domain.DynamicText, Text: &text}}
		if item.Results != nil {
			if len(*item.Results) > 1024 {
				return Event{}, incompatible()
			}
			results, _ := json.Marshal(*item.Results)
			text := "Web results retained in native history (sha256: " + externalToolDigest(results) + ")."
			content = append(content, domain.DynamicToolContent{Kind: domain.DynamicText, Text: &text})
		}
		snapshot.Content = &content
	} else if item.Results != nil {
		return Event{}, incompatible()
	}
	event, err := c.observeExternalToolSnapshotLocked(native, turn, item.ID, snapshot, webToolOrigin)
	if err == nil && c.logger != nil {
		c.logger.InfoContext(ctx, "codex_native_tool_observed", "owner_id", c.ownerID, "turn_id", turn, "tool_family", webToolOrigin, "stage", event.DynamicTool.Stage, "status", snapshot.Status)
	}
	return event, err
}
