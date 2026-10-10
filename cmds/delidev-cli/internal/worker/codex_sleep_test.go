// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestCodexSleepKeepsExactDurationAndSeparateTurnResult(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	duration := uint64(10)
	native := &codex.Tool{ID: "original-sleep", Kind: codex.SleepTool, Status: codex.ToolRunning, SleepDurationMS: &duration}
	event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
	before := len(rpc.events)
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
		t.Fatal(err)
	}
	native.Status, event.Kind = codex.ToolCompleted, codex.ToolCompletedEvent
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || len(rpc.events) != before+2 {
		t.Fatal("sleep completion became turn result", err)
	}
	terminal := codex.Event{Kind: codex.TurnCompletedEvent, ThreadID: c.thread, TurnID: c.turn, Correlated: true, Turn: &codex.Turn{ID: c.turn, Status: codex.TurnCompleted}}
	if handled, err := c.PublishCore(context.Background(), terminal); !handled || err != nil || !c.finished || len(rpc.events) != before+3 {
		t.Fatal("separate terminal lost", err)
	}
}
func TestCodexSleepRejectsChangedDurationAndInterruptedOwnership(t *testing.T) {
	for _, change := range []string{"duration", "turn", "thread", "interrupted", "duplicate", "output"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			duration := uint64(10)
			native := &codex.Tool{ID: "original-sleep", Kind: codex.SleepTool, Status: codex.ToolRunning, SleepDurationMS: &duration}
			event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
			if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
				t.Fatal(err)
			}
			native.Status, event.Kind = codex.ToolCompleted, codex.ToolCompletedEvent
			switch change {
			case "duration":
				duration = 11
			case "turn":
				event.TurnID = domain.NewID()
			case "thread":
				event.ThreadID = domain.NewID()
			case "interrupted":
				event.Late = true
			case "duplicate":
				if _, err := c.PublishCore(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			case "output":
				event.Kind = codex.ToolOutputEvent
				event.TextDelta = "not a command"
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("sleep relaxed original authority")
			}
		})
	}
}
