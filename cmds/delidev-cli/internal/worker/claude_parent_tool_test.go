// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestSubagentClaudeParentToolRejectsAnotherChildBeforePublication(t *testing.T) {
	for _, scenario := range []string{"root", "nested", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, start := claudeToolFixture(t, "", "Agent")
			ctx := context.Background()
			tool, kind, description := "tool_original_one", claude.LocalAgentTask, "Original child"
			start.Kind, start.Content, start.NativeID = claude.TaskObserved, nil, string(domain.NewID())
			start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind, Description: &description}
			if _, err := c.PublishTaskObservation(ctx, start); err != nil {
				t.Fatal(err)
			}
			if scenario == "nested" {
				content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageSnapshot, ParentToolID: tool, Blocks: []claude.NativeContentBlock{{Kind: claude.ToolUseBlock, Tool: &claude.NativeTool{ID: "original_nested_tool", Name: "Agent"}}}})
				if _, err := c.PublishObservation(ctx, content); err != nil {
					t.Fatal(err)
				}
				tool = "original_nested_tool"
				start.NativeID = string(domain.NewID())
				start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_nested_child", ToolID: &tool, Type: &kind, Description: &description}
				if _, err := c.PublishTaskObservation(ctx, start); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "completed" {
				status := claude.TaskCompleted
				finished := start
				finished.NativeID = string(domain.NewID())
				finished.Task = &claude.NativeTaskObservation{Kind: claude.TaskUpdated, ID: start.Task.ID, Patch: &claude.TaskPatch{Status: &status}}
				if _, err := c.PublishTaskObservation(ctx, finished); err != nil {
					t.Fatal(err)
				}
			}
			before, children := len(rpc.events), len(c.children)
			duplicate := start
			duplicate.NativeID = string(domain.NewID())
			copy := *start.Task
			copy.ID, duplicate.Task = "foreign_second_child", &copy
			if _, err := c.PublishTaskObservation(ctx, duplicate); err == nil || len(rpc.events) != before || len(c.children) != children || c.children["foreign_second_child"].ID != "" {
				t.Fatal("one parent tool acquired another child or ambiguous output ownership")
			}
		})
	}
}
