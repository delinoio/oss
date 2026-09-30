package worker

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"testing"
)

func TestSubagentClaudeOriginalTaskReplayLateOutputAndNestedOwnership(t *testing.T) {
	c, rpc, original := claudeToolFixture(t, "", "Agent")
	start := original
	start.Kind, start.Content = claude.TaskObserved, nil
	start.NativeID = string(domain.NewID())
	tool, kind, description, agent := "tool_original_one", claude.LocalAgentTask, "Original native child", "general-purpose"
	start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "native_child", ToolID: &tool, Type: &kind, Description: &description, SubagentType: &agent}
	rpc.lose = true
	if handled, err := c.PublishTaskObservation(context.Background(), start); !handled || err == nil || len(c.children) != 0 {
		t.Fatal("lost receipt advanced child ownership")
	}
	index := len(rpc.events) - 1
	rpc.lose = false
	if err := c.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[index] != rpc.requests[index+1] || !bytes.Equal(rpc.events[index], rpc.events[index+1]) {
		t.Fatal("child replay replaced its exact receipt")
	}
	if c.childrenClosed() {
		t.Fatal("live child did not retain cleanup")
	}
	c.resultUsage = true
	content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageSnapshot, ParentToolID: tool, Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &description}, {Kind: claude.ToolUseBlock, Tool: &claude.NativeTool{ID: "nested_agent_tool", Name: "Agent"}}}})
	content.Content[0].Model = "observed-child-model"
	zero, outputTokens := int64(0), int64(7)
	content.Content[0].Usage = &claude.ProviderUsage{Input: &zero, Output: &outputTokens}
	if handled, err := c.PublishObservation(context.Background(), content); !handled || err != nil {
		t.Fatal("late owned child content rejected", err)
	}
	before := len(rpc.events)
	if handled, err := c.PublishUsageObservation(context.Background(), content); handled || err != nil || len(rpc.events) != before {
		t.Fatal("runner reattributed child usage through the root adapter", err)
	}
	child := c.children["native_child"]
	if child.ObservedModel == nil || *child.ObservedModel != "observed-child-model" || child.Output == nil || len(child.Output.Blocks) != 2 || child.RequestedModel != nil {
		t.Fatal("requested model was fabricated or blocks collapsed")
	}
	if child.Usage == nil || child.Usage.Scope != domain.SubagentResponseUsage || *child.Usage.Input != "0" || child.Usage.Total != nil || child.Usage.NativeReport == "" {
		t.Fatal("child provider usage became cumulative or unavailable became zero")
	}
	nested := start
	nested.NativeID = string(domain.NewID())
	nestedTool := "nested_agent_tool"
	nested.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "nested_child", ToolID: &nestedTool, Type: &kind, Description: &description, SubagentType: &agent}
	if _, err := c.PublishTaskObservation(context.Background(), nested); err != nil {
		t.Fatal(err)
	}
	if c.children["nested_child"].ParentID != "native_child" {
		t.Fatal("nested child acquired root parent")
	}
	finished := start
	finished.NativeID = string(domain.NewID())
	status := claude.TaskCompleted
	finished.Task = &claude.NativeTaskObservation{Kind: claude.TaskUpdated, ID: "native_child", Patch: &claude.TaskPatch{Status: &status}}
	if _, err := c.PublishTaskObservation(context.Background(), finished); err != nil || c.childrenClosed() {
		t.Fatal("parent completed running nested child", err)
	}
}
