// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (c *Client) observeAppProgressLocked(native nativewire.Event) (Event, error) {
	if !c.appsProfile {
		return privateNative(native), nil
	}
	var value struct {
		ThreadID domain.ID `json:"threadId"`
		TurnID   domain.ID `json:"turnId"`
		ItemID   string    `json:"itemId"`
		Message  *string   `json:"message"`
	}
	if domain.Decode(native.Params, &value) != nil || value.ThreadID.Validate() != nil || value.TurnID.Validate() != nil || domain.Text(value.ItemID, "original app call", 1024, true) != nil || value.Message == nil || domain.Text(*value.Message, "original app progress", domain.MaxMessageText, false) != nil {
		return Event{}, incompatible()
	}
	if value.ThreadID != c.thread {
		return privateNative(native), nil
	}
	if c.apps == nil {
		return Event{}, incompatible()
	}
	call, exists := c.apps.calls[value.ItemID]
	turn, known := c.execution.turns[value.TurnID]
	if !exists || !known || call.completed || call.turn != value.TurnID || call.progress >= 1024 {
		return Event{}, incompatible()
	}
	call.progress++
	c.apps.calls[value.ItemID] = call
	tool := &Tool{ID: value.ItemID, Kind: CodexAppTool, Status: ToolRunning, CodexApp: &domain.CodexAppCallObservation{Identity: call.identity.Clone(), Progress: value.Message}}
	return Event{Kind: ToolUpdatedEvent, ThreadID: c.thread, TurnID: value.TurnID, ItemID: value.ItemID, Tool: tool, Correlated: true, Late: turn.Turn.Status.terminal()}, nil
}
