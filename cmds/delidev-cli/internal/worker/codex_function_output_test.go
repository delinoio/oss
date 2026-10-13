// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
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
