// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestSubagentClaudeIdleWaitsForEveryChildAndRetainsOriginalReceipt(t *testing.T) {
	c, rpc, results := claudeToolFixture(t, "", "Agent")
	ctx := context.Background()
	for i, tool := range []string{"tool_original_one", "tool_original_two"} {
		kind, description := claude.LocalAgentTask, "Original child"
		id := []string{"first_child", "second_child"}[i]
		o := claude.LifecycleObservation{Kind: claude.TaskObserved, SessionID: c.binding.journal.SessionID, InputID: c.binding.journal.InputID, TurnID: c.binding.turn, NativeID: string(domain.NewID()), Accepted: true, Task: &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: id, ToolID: &tool, Type: &kind, Description: &description}}
		if _, err := c.PublishTaskObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.PublishObservation(ctx, results); err != nil {
		t.Fatal(err)
	}
	b := c.binding
	result := claude.LifecycleObservation{Kind: claude.InputFinished, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, NativeID: string(domain.NewID()), Accepted: true, Result: &claude.NativeResult{Kind: claude.ResultSuccess, Reason: claude.Completed, Usage: &claude.ResultUsage{}}}
	if _, err := c.PublishUsageObservation(ctx, result); err != nil {
		t.Fatal(err)
	}
	command := claude.LifecycleObservation{Kind: claude.CommandObserved, SessionID: result.SessionID, InputID: result.InputID, TurnID: result.TurnID, NativeID: string(domain.NewID()), Accepted: true, Command: claude.CommandCompleted}
	idle := claude.LifecycleObservation{Kind: claude.RunStateObserved, SessionID: result.SessionID, TurnID: result.TurnID, NativeID: string(domain.NewID()), Run: &claude.NativeRunObservation{State: claude.RunIdle}}
	if _, err := c.PublishBoundaryObservation(ctx, command); err != nil {
		t.Fatal(err)
	}
	before := len(rpc.events)
	if handled, err := c.PublishBoundaryObservation(ctx, idle); !handled || err != nil || len(rpc.events) != before || c.terminalPublished() || c.pendingBoundaryReady() || c.pendingTerminal == nil {
		t.Fatal("root idle rejected live children or established cleanup", err)
	}
	for i, id := range []string{"first_child", "second_child"} {
		status := claude.TaskCompleted
		o := claude.LifecycleObservation{Kind: claude.TaskObserved, SessionID: result.SessionID, InputID: result.InputID, TurnID: result.TurnID, NativeID: string(domain.NewID()), Accepted: true, Task: &claude.NativeTaskObservation{Kind: claude.TaskUpdated, ID: id, Patch: &claude.TaskPatch{Status: &status}}}
		if _, err := c.PublishTaskObservation(ctx, o); err != nil {
			t.Fatal("late child completion rejected", err)
		}
		if c.pendingBoundaryReady() != (i == 1) || c.terminalPublished() {
			t.Fatal("first child completed its sibling or skipped final history inspection")
		}
	}
	if _, err := c.Complete(ctx, nil); err == nil {
		t.Fatal("pending root boundary fabricated process cleanup")
	}
	rpc.lose = true
	before = len(rpc.events)
	if handled, err := c.PublishPendingBoundary(ctx); !handled || err == nil || c.terminal != nil {
		t.Fatal("lost terminal acknowledgment acquired completion")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) || c.terminal == nil || c.terminal.IdleNativeID != idle.NativeID || c.terminal.CommandNativeID != command.NativeID || c.terminal.ResultNativeID != result.NativeID || c.pendingTerminal != nil || !c.terminalPublished() {
		t.Fatal("deferred terminal replaced original boundary or receipt")
	}
	before = len(rpc.events)
	if handled, err := c.PublishPendingBoundary(ctx); handled || err != nil || len(rpc.events) != before {
		t.Fatal("acknowledged deferred boundary published twice")
	}
}
