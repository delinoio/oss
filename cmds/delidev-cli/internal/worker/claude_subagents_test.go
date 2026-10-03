// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestSubagentClaudeRootUsageRejectsChangedAcknowledgedChild(t *testing.T) {
	for _, name := range []string{"session", "input", "turn", "acceptance", "event", "parent", "model", "cleared-model", "usage"} {
		t.Run(name, func(t *testing.T) {
			c, rpc, start := claudeToolFixture(t, "", "Agent")
			start.Kind, start.Content, start.NativeID = claude.TaskObserved, nil, string(domain.NewID())
			tool, kind, description, agent := "tool_original_one", claude.LocalAgentTask, "Original child", "general-purpose"
			start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind, Description: &description, SubagentType: &agent}
			if _, err := c.PublishTaskObservation(context.Background(), start); err != nil {
				t.Fatal(err)
			}
			input := int64(1)
			content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted, ParentToolID: tool, Usage: &claude.ProviderUsage{Input: &input}})
			content.Content[0].Model = "original-child-model"
			if _, err := c.PublishObservation(context.Background(), content); err != nil {
				t.Fatal(err)
			}
			before := len(rpc.events)
			switch name {
			case "session":
				content.SessionID = domain.NewID()
			case "input":
				content.InputID = domain.NewID()
			case "turn":
				content.TurnID = string(domain.NewID())
			case "acceptance":
				content.Accepted = false
			case "event":
				content.NativeID = string(domain.NewID())
			case "parent":
				content.Content[0].ParentToolID = "foreign-tool"
			case "model":
				content.Content[0].Model = "changed-model"
			case "cleared-model":
				content.Content[0].Model = ""
			case "usage":
				changed := int64(2)
				content.Content[0].Usage = &claude.ProviderUsage{Input: &changed}
			}
			if _, err := c.PublishUsageObservation(context.Background(), content); err == nil || c.binding.stage != claudeBindingBlocked || len(rpc.events) != before {
				t.Fatal("changed child report bypassed root ownership or published usage", err)
			}
		})
	}
}

func TestSubagentClaudeUsageRetainsOriginalMissingModelAcrossReceiptReplay(t *testing.T) {
	c, rpc, start := claudeToolFixture(t, "", "Agent")
	start.Kind, start.Content, start.NativeID = claude.TaskObserved, nil, string(domain.NewID())
	tool, kind := "tool_original_one", claude.LocalAgentTask
	start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind}
	if _, err := c.PublishTaskObservation(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	first := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageStarted, ParentToolID: tool})
	first.Content[0].Model = "observed-child-model"
	if _, err := c.PublishObservation(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	input := int64(1)
	missing := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageUpdated, ParentToolID: tool, Usage: &claude.ProviderUsage{Input: &input}})
	missing.Content[0].Model = ""
	rpc.lose = true
	if _, err := c.PublishObservation(context.Background(), missing); err == nil || len(c.childUsageModels) != 0 {
		t.Fatal("lost receipt installed original model evidence")
	}
	rpc.lose = false
	if err := c.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	child := c.children["original_child"]
	if child.ObservedModel == nil || *child.ObservedModel != "observed-child-model" {
		t.Fatal("missing report model erased the last available observation")
	}
	before := len(rpc.events)
	if handled, err := c.PublishUsageObservation(context.Background(), missing); handled || err != nil || len(rpc.events) != before {
		t.Fatal("exact original missing model was rejected or billed after receipt replay", err)
	}
	missing.Content[0].Model = "observed-child-model"
	if _, err := c.PublishUsageObservation(context.Background(), missing); err == nil || c.binding.stage != claudeBindingBlocked || len(rpc.events) != before {
		t.Fatal("invented model presence bypassed exact child report validation", err)
	}
}

func TestSubagentClaudeTaskReceiptsKeepSourceAndLateFlags(t *testing.T) {
	c, rpc, start := claudeToolFixture(t, "", "Agent")
	start.Kind, start.Content, start.NativeID = claude.TaskObserved, nil, string(domain.NewID())
	tool, kind := "tool_original_one", claude.LocalAgentTask
	start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind}
	if _, err := c.PublishTaskObservation(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	text := "Original assistant output"
	content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ProviderMessageSnapshot, ParentToolID: tool, Model: "observed-model", Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &text}}})
	content.Content[0].Model = "observed-model"
	if _, err := c.PublishObservation(context.Background(), content); err != nil {
		t.Fatal(err)
	}
	finished := start
	finished.NativeID = string(domain.NewID())
	status, yes := claude.TaskCompleted, true
	finished.Task = &claude.NativeTaskObservation{Kind: claude.TaskNotification, ID: "original_child", ToolID: &tool, Status: &status, SkipTranscript: &yes, Ambient: &yes}
	rpc.lose = true
	if _, err := c.PublishTaskObservation(context.Background(), finished); err == nil || c.children["original_child"].Task.SkipTranscript != nil {
		t.Fatal("unacknowledged task metadata altered history eligibility")
	}
	rpc.lose = false
	if err := c.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	var event domain.ExecutionEvent
	if err := json.Unmarshal(rpc.events[len(rpc.events)-1], &event); err != nil {
		t.Fatal(err)
	}
	report := event.Subagents[0]
	if report.Output != nil || report.ObservedModel != nil || report.RequestedModel != nil || report.Usage != nil {
		t.Fatal("task receipt falsely attributed retained content/model/usage")
	}
	child := c.children["original_child"]
	if child.Output == nil || child.ObservedModel == nil || *child.ObservedModel != "observed-model" || child.Task.SkipTranscript == nil || !*child.Task.SkipTranscript || child.Task.Ambient == nil || !*child.Task.Ambient {
		t.Fatal("late flags or last available output/model were lost")
	}
}

func TestSubagentClaudeForwardedInputDoesNotBecomeOutput(t *testing.T) {
	c, rpc, start := claudeToolFixture(t, "", "Agent")
	start.Kind, start.Content, start.NativeID = claude.TaskObserved, nil, string(domain.NewID())
	tool, kind := "tool_original_one", claude.LocalAgentTask
	start.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind}
	if _, err := c.PublishTaskObservation(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	text := "Forwarded user context"
	content := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ChildInputObserved, ParentToolID: tool, Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &text}}})
	if _, err := c.PublishObservation(context.Background(), content); err != nil {
		t.Fatal(err)
	}
	var event domain.ExecutionEvent
	if err := json.Unmarshal(rpc.events[len(rpc.events)-1], &event); err != nil {
		t.Fatal(err)
	}
	if event.Subagents[0].Output != nil || c.children["original_child"].Output != nil {
		t.Fatal("forwarded user input became child assistant output")
	}
}
