// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type externalToolOrigin string

const (
	dynamicToolOrigin externalToolOrigin = ""
	mcpToolOrigin     externalToolOrigin = "mcp"
	webToolOrigin     externalToolOrigin = "web"
)

// MCP tools execute inside the original native process. Reuse the negotiated
// inert tool lifecycle without granting an item/tool/call responder. In
// particular, native list_mcp_resources and list_mcp_resource_templates emit
// mcpToolCall even when the configured MCP server inventory is empty.
func (c *Client) observeMCPToolLocked(ctx context.Context, native nativewire.Event, turn domain.ID, raw json.RawMessage) (Event, error) {
	id, snapshot, err := decodeMCPTool(raw, native.Method == "item/completed", c.nativeFrameLimit())
	if err != nil {
		return Event{}, err
	}
	event, err := c.observeExternalToolSnapshotLocked(native, turn, id, snapshot, mcpToolOrigin)
	if err == nil && c.logger != nil {
		c.logger.InfoContext(ctx, "codex_native_tool_observed", "owner_id", c.ownerID, "turn_id", turn, "tool_family", mcpToolOrigin, "stage", event.DynamicTool.Stage, "status", snapshot.Status)
	}
	return event, err
}

func decodeMCPTool(raw json.RawMessage, completed bool, limit int) (string, *domain.DynamicToolSnapshot, error) {
	var item struct {
		Type           string                   `json:"type"`
		ID             string                   `json:"id"`
		Server         string                   `json:"server"`
		Tool           string                   `json:"tool"`
		Arguments      json.RawMessage          `json:"arguments"`
		Status         domain.DynamicToolStatus `json:"status"`
		Result         json.RawMessage          `json:"result"`
		Error          json.RawMessage          `json:"error"`
		DurationMS     *int64                   `json:"durationMs"`
		AppContext     json.RawMessage          `json:"appContext"`
		AppUI          json.RawMessage          `json:"mcpAppUi"`
		AppResourceURI *string                  `json:"mcpAppResourceUri"`
		PluginID       *string                  `json:"pluginId"`
		ReadOnlyHint   *bool                    `json:"readOnlyHint"`
	}
	if len(raw) > limit || domain.ValidateJSONUnicodeEscapes(raw) != nil || domain.DecodeBounded(raw, &item, limit) != nil || item.Type != "mcpToolCall" || domain.Text(item.ID, "native MCP item", 1024, true) != nil || domain.Text(item.Server, "native MCP server", 256, false) != nil {
		return "", nil, incompatible()
	}
	identity, err := dynamicIdentity(item.Tool, &item.Server, item.Arguments)
	if err != nil {
		return "", nil, err
	}
	if !completed && item.Status != domain.DynamicRunning || completed && item.Status != domain.DynamicDone && item.Status != domain.DynamicFailed || item.DurationMS != nil && *item.DurationMS < 0 {
		return "", nil, incompatible()
	}
	hasResult, hasError := externalValuePresent(item.Result), externalValuePresent(item.Error)
	if !completed && (hasResult || hasError || item.DurationMS != nil) || hasResult && hasError || item.Status == domain.DynamicDone && hasError {
		return "", nil, incompatible()
	}
	snapshot := &domain.DynamicToolSnapshot{Identity: identity, Status: item.Status, DurationMS: item.DurationMS}
	if hasResult {
		var result struct {
			Content    []json.RawMessage `json:"content"`
			Structured json.RawMessage   `json:"structuredContent"`
			Meta       json.RawMessage   `json:"_meta"`
		}
		if domain.DecodeBounded(item.Result, &result, limit) != nil || result.Content == nil || len(result.Content) > 1024 {
			return "", nil, incompatible()
		}
		content := make([]domain.DynamicToolContent, 0, len(result.Content))
		for _, raw := range result.Content {
			var part struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if json.Unmarshal(raw, &part) != nil {
				return "", nil, incompatible()
			}
			var projected domain.DynamicToolContent
			switch part.Type {
			case "text":
				var text struct {
					Type        string          `json:"type"`
					Text        *string         `json:"text"`
					Annotations json.RawMessage `json:"annotations"`
					Meta        json.RawMessage `json:"_meta"`
				}
				if domain.Decode(raw, &text) != nil || text.Text == nil {
					return "", nil, incompatible()
				}
				projected = domain.DynamicToolContent{Kind: domain.DynamicText, Text: text.Text}
			case "image", "audio":
				kind := domain.DynamicImage
				if part.Type == "audio" {
					kind = domain.DynamicAudio
				}
				projected = domain.DynamicToolContent{Kind: kind, ReferenceDigest: externalToolDigest(raw)}
			case "resource", "resource_link":
				text := "MCP resource retained in native history (sha256: " + externalToolDigest(raw) + ")."
				projected = domain.DynamicToolContent{Kind: domain.DynamicText, Text: &text}
			default:
				return "", nil, incompatible()
			}
			content = append(content, projected)
		}
		if externalValuePresent(result.Structured) {
			text := "MCP structured result retained in native history (sha256: " + externalToolDigest(result.Structured) + ")."
			content = append(content, domain.DynamicToolContent{Kind: domain.DynamicText, Text: &text})
		}
		snapshot.Content = &content
	}
	if hasError {
		var failure struct {
			Message string `json:"message"`
		}
		if domain.Decode(item.Error, &failure) != nil || domain.Text(failure.Message, "native MCP error", domain.MaxMessageText, true) != nil || item.Status != domain.DynamicFailed {
			return "", nil, incompatible()
		}
	}
	if completed {
		success := item.Status == domain.DynamicDone
		snapshot.Success = &success
	}
	return item.ID, snapshot, nil
}

func externalValuePresent(raw json.RawMessage) bool { return len(raw) != 0 && string(raw) != "null" }
func externalToolDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Historical validation never publishes live events, creates response arrivals,
// resolves resource locators or changes tool ownership. Preserve raw bytes in
// the history proof after validating the same bounded settled schema as live.
func settledExternalTool(raw json.RawMessage, kind string) bool {
	switch kind {
	case "mcpToolCall":
		_, snapshot, err := decodeMCPTool(raw, true, enlargedNativeFrameBytes)
		return err == nil && snapshot.Validate() == nil
	case "webSearch":
		_, snapshot, err := decodeWebTool(raw, true)
		return err == nil && snapshot.Validate() == nil
	}
	return false
}
