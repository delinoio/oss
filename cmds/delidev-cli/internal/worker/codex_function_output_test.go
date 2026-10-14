// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexFunctionOutputRetainsOneItemAndIndependentTerminal(t *testing.T) {
	for _, completedOnly := range []bool{false, true} {
		c, rpc := codexTerminalStatusFixture(t)
		text := "inert result"
		e := codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "original-output", Correlated: true, FunctionOutput: &domain.FunctionOutput{Name: "tool", Text: &text}, FunctionOutputStage: domain.FunctionOutputCompleted}
		before := len(rpc.events)
		if !completedOnly {
			e.FunctionOutputStage = domain.FunctionOutputStarted
			if handled, err := c.PublishCore(context.Background(), e); !handled || err != nil {
				t.Fatal(err)
			}
		}
		e.FunctionOutputStage = domain.FunctionOutputCompleted
		if handled, err := c.PublishCore(context.Background(), e); !handled || err != nil || c.finished {
			t.Fatal("result became turn terminal", err)
		}
		count := 1
		if !completedOnly {
			count = 2
		}
		if len(rpc.events) != before+count {
			t.Fatal("result invented a start or duplicated publication")
		}
		if !completedOnly {
			var started, completed domain.ExecutionEvent
			if domain.Decode(rpc.events[before], &started) != nil || domain.Decode(rpc.events[before+1], &completed) != nil || started.FunctionOutput.ID != completed.FunctionOutput.ID {
				t.Fatal("completion replaced original item")
			}
		}
		terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
		if _, err := c.PublishCore(context.Background(), terminal); err != nil || !c.finished {
			t.Fatal("independent terminal lost", err)
		}
	}
}
func TestCodexFunctionOutputRejectsForeignChangedAndDuplicateEvidence(t *testing.T) {
	for _, change := range []string{"turn", "thread", "late", "content", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			text := "original"
			e := codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "output", Correlated: true, FunctionOutput: &domain.FunctionOutput{Name: "tool", Text: &text}, FunctionOutputStage: domain.FunctionOutputStarted}
			if _, err := c.PublishCore(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			e.FunctionOutputStage = domain.FunctionOutputCompleted
			switch change {
			case "turn":
				e.TurnID = domain.NewID()
			case "thread":
				e.ThreadID = domain.NewID()
			case "late":
				e.Late = true
			case "content":
				text = "changed"
			case "duplicate":
				if _, err := c.PublishCore(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), e); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("foreign/changed output acquired authority")
			}
		})
	}
}

func TestCodexFunctionOutputRemainsPrivateWithoutNegotiation(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	text := "private legacy-peer result"
	event := codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "original-output", Correlated: true, FunctionOutput: &domain.FunctionOutput{Name: "tool", Text: &text}, FunctionOutputStage: domain.FunctionOutputCompleted}
	before := len(rpc.events)
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.blocked || len(rpc.events) != before {
		t.Fatal("unsupported peer received the private function output", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished {
		t.Fatal("private output prevented independent turn completion", err)
	}
}

func codexPrivateOutputEvent(c *CodexEventPublisher, id string, stage domain.FunctionOutputStage, text string) codex.Event {
	return codex.Event{Kind: codex.FunctionOutputEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: id, Correlated: true, FunctionOutput: &domain.FunctionOutput{Name: "tool", Text: &text}, FunctionOutputStage: stage}
}

func TestCodexPrivateFunctionOutputRetainsIdentityWithoutPublication(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	before := len(rpc.events)
	for _, stage := range []domain.FunctionOutputStage{domain.FunctionOutputStarted, domain.FunctionOutputCompleted} {
		if handled, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "private-output", stage, "private")); !handled || err != nil {
			t.Fatal("private output was rejected", err)
		}
	}
	if len(rpc.events) != before || len(c.outputs) != 1 || !c.outputs["private-output"].Completed || !c.itemKnown("private-output") {
		t.Fatal("private output did not retain exactly one completed identity without publishing")
	}
	// A matching completion does not consume another slot; duplicates and changes are rejected.
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "private-output", domain.FunctionOutputCompleted, "private")); err == nil || !c.blocked {
		t.Fatal("duplicate private completion was accepted")
	}
	c, rpc = codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "private-output", domain.FunctionOutputStarted, "private")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "private-output", domain.FunctionOutputCompleted, "changed")); err == nil || !c.blocked || len(rpc.events) != before {
		t.Fatal("changed private digest was accepted")
	}
}

func TestCodexPrivateFunctionOutputReservesNativeIDFromOtherFamilies(t *testing.T) {
	duration := uint64(10)
	for _, family := range []string{"message", "tool", "artifact"} {
		t.Run(family, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.functionOutputsSupported = false
			if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "shared-id", domain.FunctionOutputStarted, "private")); err != nil {
				t.Fatal(err)
			}
			event := codex.Event{ThreadID: c.thread, TurnID: c.turn, ItemID: "shared-id", Correlated: true}
			switch family {
			case "message":
				event.Kind = codex.MessageStartedEvent
				event.Message = &codex.Message{ID: "shared-id", Role: codex.AssistantRole}
			case "tool":
				event.Kind = codex.ToolStartedEvent
				event.Tool = &codex.Tool{ID: "shared-id", Kind: codex.SleepTool, Status: codex.ToolRunning, SleepDurationMS: &duration}
			case "artifact":
				event.Kind = codex.ArtifactStartedEvent
				event.Artifact = &codex.Artifact{ID: "shared-id", Kind: codex.ReasoningArtifact}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("later item claimed a privately retained native ID")
			}
		})
	}
}

func TestCodexPrivateFunctionOutputRejectsIDOwnedByOtherFamily(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	c.messages["owned"] = domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "owned"}
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "owned", domain.FunctionOutputStarted, "private")); err == nil || !c.blocked || len(rpc.events) != before || len(c.outputs) != 0 {
		t.Fatal("private output acquired a second identity for an owned ID")
	}
}

func TestCodexPrivateFunctionOutputSharesItemBound(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	for i := 0; i < 9998; i++ {
		c.messages[fmt.Sprintf("filler-%d", i)] = domain.ExecutionMessageUpdate{}
	}
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "at-9999", domain.FunctionOutputStarted, "private")); err != nil {
		t.Fatal("private output was rejected below the bound", err)
	}
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "at-9999", domain.FunctionOutputCompleted, "private")); err != nil {
		t.Fatal("matching completion consumed another slot", err)
	}
	if len(c.messages)+len(c.outputs) != 9999 {
		t.Fatal("unexpected shared item count")
	}
	// Bring the shared count to the limit, then a new different item must be rejected.
	c.messages["filler-last"] = domain.ExecutionMessageUpdate{}
	before := len(rpc.events)
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "over", domain.FunctionOutputStarted, "private")); err == nil || !c.blocked || len(rpc.events) != before {
		t.Fatal("private output exceeded the shared item bound")
	}
}

func TestCodexPrivateFunctionOutputAllowsUnusedIDsAndTerminal(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	c.functionOutputsSupported = false
	if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, "private-output", domain.FunctionOutputCompleted, "private")); err != nil {
		t.Fatal(err)
	}
	before := len(rpc.events)
	message := codex.Event{Kind: codex.MessageStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: "different-id", Correlated: true, Message: &codex.Message{ID: "different-id", Role: codex.AssistantRole}}
	if handled, err := c.PublishCore(context.Background(), message); !handled || err != nil || len(rpc.events) != before+1 {
		t.Fatal("an unused ID was rejected on an unnegotiated connection", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished {
		t.Fatal("private output prevented independent turn completion", err)
	}
}

func TestCodexPrivateFunctionOutputRejectsInvalidIdentityAndStageBeforeRetention(t *testing.T) {
	cases := map[string]struct {
		id    string
		stage domain.FunctionOutputStage
	}{
		"empty id":      {id: "", stage: domain.FunctionOutputStarted},
		"oversized id":  {id: string(make([]byte, 1025)), stage: domain.FunctionOutputStarted},
		"unknown stage": {id: "private-output", stage: domain.FunctionOutputStage("unknown")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.functionOutputsSupported = false
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), codexPrivateOutputEvent(c, tc.id, tc.stage, "private")); err == nil || !c.blocked {
				t.Fatal("invalid private output evidence was accepted")
			}
			if len(c.outputs) != 0 || len(rpc.events) != before {
				t.Fatal("invalid private output evidence changed retained state")
			}
		})
	}
}
