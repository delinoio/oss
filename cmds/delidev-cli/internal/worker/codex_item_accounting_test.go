// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

const itemAccountingLimit = 10000

func itemAccountingIdentity() domain.DynamicToolIdentity {
	return domain.DynamicToolIdentity{Tool: "fixture_tool", ArgumentsDigest: strings.Repeat("a", 64)}
}

func itemAccountingDynamicEvent(c *CodexEventPublisher, native string, stage domain.DynamicToolStage) codex.Event {
	update := &domain.DynamicToolUpdate{NativeID: native, Stage: stage}
	switch stage {
	case domain.DynamicStarted:
		update.Snapshot = &domain.DynamicToolSnapshot{Identity: itemAccountingIdentity(), Status: "inProgress"}
	case domain.DynamicCompleted:
		update.Snapshot = &domain.DynamicToolSnapshot{Identity: itemAccountingIdentity(), Status: "completed"}
	}
	return codex.Event{Kind: codex.DynamicToolEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native, Correlated: true, DynamicTool: update}
}

// itemAccountingStartEvents returns one "start" event per retained-item family
// for the given native ID.
func itemAccountingStartEvents(c *CodexEventPublisher, native string) map[string]codex.Event {
	text := "inert result"
	duration := uint64(10)
	base := codex.Event{ThreadID: c.thread, TurnID: c.turn, ItemID: native, Correlated: true}
	message, tool, artifact, output, dynamic := base, base, base, base, itemAccountingDynamicEvent(c, native, domain.DynamicStarted)
	message.Kind = codex.MessageStartedEvent
	message.Message = &codex.Message{ID: native, Role: codex.AssistantRole}
	tool.Kind = codex.ToolStartedEvent
	tool.Tool = &codex.Tool{ID: native, Kind: codex.SleepTool, Status: codex.ToolRunning, SleepDurationMS: &duration}
	artifact.Kind = codex.ArtifactStartedEvent
	artifact.Artifact = &codex.Artifact{ID: native, Kind: codex.ReasoningArtifact}
	output.Kind = codex.FunctionOutputEvent
	output.FunctionOutput = &domain.FunctionOutput{Name: "tool", Text: &text}
	output.FunctionOutputStage = domain.FunctionOutputStarted
	return map[string]codex.Event{"message": message, "tool": tool, "artifact": artifact, "function-output": output, "dynamic-tool": dynamic}
}

// itemAccountingPad retains count synthetic messages so the shared total is
// exactly count plus whatever the publisher already holds.
func itemAccountingPad(c *CodexEventPublisher, count int, prefix string) {
	for i := 0; i < count; i++ {
		c.messages[prefix+strconv.Itoa(i)] = domain.ExecutionMessageUpdate{}
	}
}

func itemAccountingTotal(c *CodexEventPublisher) int {
	return len(c.messages) + len(c.tools) + len(c.artifacts) + len(c.outputs) + len(c.dynamicTools)
}

func TestItemAccountingKnownIncludesDynamicOnlyIdentity(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	if c.itemKnown("dynamic-only") {
		t.Fatal("unknown native ID reported known")
	}
	c.dynamicTools["dynamic-only"] = dynamicToolPublication{ID: domain.NewID()}
	if !c.itemKnown("dynamic-only") {
		t.Fatal("native ID held only in dynamicTools was not known")
	}
	if c.itemKnown("other") {
		t.Fatal("unrelated native ID reported known")
	}
}

func TestItemAccountingLimitCountsDynamicTools(t *testing.T) {
	c, _ := codexTerminalStatusFixture(t)
	itemAccountingPad(c, itemAccountingLimit-2, "message-")
	c.dynamicTools["dynamic-a"] = dynamicToolPublication{ID: domain.NewID()}
	if c.itemLimitReached() {
		t.Fatal("limit reported before the shared total reached the bound")
	}
	c.dynamicTools["dynamic-b"] = dynamicToolPublication{ID: domain.NewID()}
	if itemAccountingTotal(c) != itemAccountingLimit || !c.itemLimitReached() {
		t.Fatal("dynamic tools were not counted in the shared bound")
	}
}

func TestItemAccountingDynamicIdentityBlocksOtherFamilies(t *testing.T) {
	for _, family := range []string{"message", "tool", "artifact", "function-output", "dynamic-tool"} {
		t.Run(family, func(t *testing.T) {
			c, rpc, _ := dynamicWorkerFixture(t)
			before, total := len(rpc.events), itemAccountingTotal(c)
			handled, err := c.PublishCore(context.Background(), itemAccountingStartEvents(c, "dynamic-call")[family])
			_ = handled
			if err == nil {
				t.Fatal("start reusing a retained dynamic native ID was accepted")
			}
			if len(rpc.events) != before || itemAccountingTotal(c) != total {
				t.Fatal("rejected start emitted an event or changed retained items")
			}
		})
	}
}

func TestItemAccountingBoundaryRejectsEveryFamilyAfterDynamicFill(t *testing.T) {
	for _, family := range []string{"message", "tool", "artifact", "function-output", "dynamic-tool"} {
		t.Run(family, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.publisher.input.NativeDynamicTools = true
			itemAccountingPad(c, itemAccountingLimit-2, "message-")
			// One dynamic tool fills slot 9,999 and must be admitted.
			if _, err := c.PublishCore(context.Background(), itemAccountingDynamicEvent(c, "dynamic-fill", domain.DynamicStarted)); err != nil {
				t.Fatal(err)
			}
			if itemAccountingTotal(c) != itemAccountingLimit-1 {
				t.Fatal("dynamic fill was not retained")
			}
			// One more regular item fills slot 10,000 and must be admitted.
			if _, err := c.PublishCore(context.Background(), itemAccountingStartEvents(c, "last-regular")["message"]); err != nil {
				t.Fatal(err)
			}
			if itemAccountingTotal(c) != itemAccountingLimit {
				t.Fatal("last regular item was not retained")
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), itemAccountingStartEvents(c, "overflow")[family]); err == nil {
				t.Fatal("new item was admitted beyond the shared bound")
			}
			if len(rpc.events) != before || itemAccountingTotal(c) != itemAccountingLimit {
				t.Fatal("rejected overflow emitted an event or changed retained items")
			}
		})
	}
}

func TestItemAccountingBoundaryRejectsNewDynamicAfterRegularFill(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.publisher.input.NativeDynamicTools = true
	itemAccountingPad(c, itemAccountingLimit, "message-")
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), itemAccountingDynamicEvent(c, "dynamic-overflow", domain.DynamicStarted)); err == nil {
		t.Fatal("new dynamic tool was admitted beyond the shared bound")
	}
	if len(rpc.events) != before || len(c.dynamicTools) != 0 {
		t.Fatal("rejected dynamic overflow emitted an event or retained state")
	}
}

func TestItemAccountingKnownDynamicUpdateSucceedsAtBound(t *testing.T) {
	c, rpc, _ := dynamicWorkerFixture(t)
	itemAccountingPad(c, itemAccountingLimit-itemAccountingTotal(c), "message-")
	if itemAccountingTotal(c) != itemAccountingLimit || !c.itemLimitReached() {
		t.Fatal("fixture did not reach the shared bound")
	}
	before := len(rpc.events)
	if handled, err := c.PublishCore(context.Background(), itemAccountingDynamicEvent(c, "dynamic-call", domain.DynamicCompleted)); !handled || err != nil {
		t.Fatal("update of a known dynamic tool at the bound was rejected", err)
	}
	if len(rpc.events) != before+1 || itemAccountingTotal(c) != itemAccountingLimit {
		t.Fatal("known dynamic update did not publish exactly once or changed the count")
	}
	if retained := c.dynamicTools["dynamic-call"]; retained.Tool.Snapshot == nil || retained.Tool.Snapshot.Status != "completed" {
		t.Fatal("known dynamic update was not retained")
	}
}
