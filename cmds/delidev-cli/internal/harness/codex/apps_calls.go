// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type trackedAppCall struct {
	turn      domain.ID
	identity  domain.CodexAppCallIdentity
	completed bool
}

// Source: pinned 0.162 ThreadItem::McpToolCall. Transport metadata and executable
// MCP App presentation are validated privately; only explicit result data and
// the original selected connector identity enter the product tool observation.
func decodeCodexAppCall(raw json.RawMessage, original domain.CodexAppConfiguration, completed bool) (*Tool, error) {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Status    ToolStatus      `json:"status"`
		Arguments json.RawMessage `json:"arguments"`
		Context   *struct {
			AppID       string  `json:"connectorId"`
			LinkID      *string `json:"linkId"`
			ResourceURI *string `json:"resourceUri"`
			AppName     *string `json:"appName"`
			ActionName  *string `json:"actionName"`
		} `json:"appContext"`
		ResourceURI *string `json:"mcpAppResourceUri,omitempty"`
		UI          *struct {
			ResourceURI string `json:"resourceUri"`
			Mode        string `json:"preferredModelDisplayMode"`
		} `json:"mcpAppUi"`
		PluginID *string `json:"pluginId"`
		ReadOnly *bool   `json:"readOnlyHint"`
		Result   *struct {
			Content    []json.RawMessage `json:"content"`
			Structured json.RawMessage   `json:"structuredContent"`
			Metadata   json.RawMessage   `json:"_meta"`
		} `json:"result"`
		Error *struct {
			Message *string `json:"message"`
		} `json:"error"`
		DurationMS *int64 `json:"durationMs"`
	}
	if original.Validate() != nil || domain.DecodeBounded(raw, &item, 512<<10) != nil || item.Type != "mcpToolCall" || item.Server != "codex_apps" || domain.Text(item.ID, "original app call identity", 1024, true) != nil || item.Context == nil || !slices.Contains(original.AppIDs, item.Context.AppID) || !slices.Contains([]ToolStatus{ToolRunning, ToolCompleted, ToolFailed}, item.Status) || completed == (item.Status == ToolRunning) {
		return nil, incompatible()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, incompatible()
	}
	for _, key := range []string{"type", "id", "server", "tool", "status", "arguments", "appContext", "mcpAppUi", "pluginId", "readOnlyHint", "result", "error", "durationMs"} {
		if _, exists := fields[key]; !exists {
			return nil, incompatible()
		}
	}
	if value, exists := fields["mcpAppResourceUri"]; exists && string(value) == "null" {
		return nil, incompatible()
	}
	var contextFields map[string]json.RawMessage
	if json.Unmarshal(fields["appContext"], &contextFields) != nil {
		return nil, incompatible()
	}
	for _, key := range []string{"connectorId", "linkId", "resourceUri", "appName", "actionName"} {
		if _, exists := contextFields[key]; !exists {
			return nil, incompatible()
		}
	}
	for _, value := range []*string{item.ResourceURI, item.Context.ResourceURI, item.PluginID} {
		if value != nil && domain.Text(*value, "private app presentation", 4096, false) != nil {
			return nil, incompatible()
		}
	}
	if item.UI != nil && (domain.Text(item.UI.ResourceURI, "private app presentation", 4096, true) != nil || item.UI.Mode != "inline" && item.UI.Mode != "fullscreen") {
		return nil, incompatible()
	}
	if item.Error != nil && (item.Error.Message == nil || domain.Text(*item.Error.Message, "private native app error", domain.MaxMessageText, false) != nil) {
		return nil, incompatible()
	}
	call := &domain.CodexAppCallObservation{Identity: domain.CodexAppCallIdentity{AccountID: original.AccountID, Generation: original.Generation, AppID: item.Context.AppID, LinkID: item.Context.LinkID, Tool: item.Tool, Arguments: slices.Clone(item.Arguments), AppName: item.Context.AppName, ActionName: item.Context.ActionName, ReadOnly: item.ReadOnly}, ErrorPresent: item.Error != nil, DurationMS: item.DurationMS}
	if item.Result != nil {
		if len(item.Result.Metadata) == 0 || !json.Valid(item.Result.Metadata) {
			return nil, incompatible()
		}
		call.Result = &domain.CodexAppResult{Content: item.Result.Content, Structured: item.Result.Structured}
	}
	status := map[ToolStatus]domain.ToolStatus{ToolRunning: domain.ToolRunning, ToolCompleted: domain.ToolCompleted, ToolFailed: domain.ToolFailed}[item.Status]
	if call.Validate(status) != nil {
		return nil, incompatible()
	}
	return &Tool{ID: item.ID, Kind: CodexAppTool, Status: item.Status, CodexApp: call}, nil
}

func (c *Client) observeCodexAppCallLocked(native nativewire.Event, turnID domain.ID, raw json.RawMessage) (Event, error) {
	if !c.appsProfile {
		return privateNative(native), nil
	}
	if c.apps == nil || c.managedHome == "" || c.api != nil {
		return Event{}, incompatible()
	}
	tool, err := decodeCodexAppCall(raw, c.apps.original, native.Method == "item/completed")
	if err != nil {
		return Event{}, err
	}
	turn, known := c.execution.turns[turnID]
	if !known {
		return Event{}, incompatible()
	}
	if c.apps.calls == nil {
		c.apps.calls = map[string]trackedAppCall{}
	}
	prior, exists := c.apps.calls[tool.ID]
	kind := ToolStartedEvent
	if native.Method == "item/completed" {
		if !exists || prior.completed || prior.turn != turnID || !prior.identity.SameOriginal(tool.CodexApp.Identity) {
			return Event{}, incompatible()
		}
		prior.completed = true
		c.apps.calls[tool.ID] = prior
		kind = ToolCompletedEvent
	} else {
		if exists || len(c.apps.calls) >= maxTrackedInteractions {
			return Event{}, incompatible()
		}
		c.apps.calls[tool.ID] = trackedAppCall{turn: turnID, identity: tool.CodexApp.Identity.Clone()}
	}
	var acceptance *InteractionStatus
	if kind == ToolCompletedEvent {
		acceptance, err = c.confirmAppQuestionLocked(turnID, tool)
		if err != nil {
			return Event{}, err
		}
	}
	return Event{InteractionState: acceptance, Kind: kind, ThreadID: c.thread, TurnID: turnID, ItemID: tool.ID, Tool: tool, Correlated: true, Late: turn.Turn.Status.terminal()}, nil
}
