// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

// Publish completion-only evidence without fabricating execution or retrieval.
func (c *CodexEventPublisher) publishFunctionCallOutput(ctx context.Context, event codex.Event) error {
	if event.TurnID != c.turn || c.itemKnown(event.ItemID) || c.itemLimitReached() || event.FunctionCallOutput == nil || event.FunctionCallOutput.Validate() != nil {
		return publicationUncertain()
	}
	id := domain.NewID()
	snapshot := domain.ToolSnapshot{Kind: domain.FunctionCallOutputTool, Status: domain.ToolResultObserved, FunctionCallOutput: event.FunctionCallOutput}
	update := domain.ExecutionToolUpdate{ID: id, NativeID: event.ItemID, Snapshot: &snapshot}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionToolCompleted, Tool: &update}); err != nil {
		return err
	}
	c.tools[event.ItemID] = codexToolPublication{ID: id, Kind: domain.FunctionCallOutputTool, Completed: true}
	return nil
}
