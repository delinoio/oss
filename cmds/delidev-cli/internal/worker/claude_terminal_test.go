package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeTerminalFixture(t *testing.T) (*ClaudeContentPublisher, *openCodeBindingRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	t.Helper()
	c, rpc := newClaudeContentFixture(t)
	if err := c.PublishInput(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := c.binding
	o := claude.LifecycleObservation{Kind: claude.InputFinished, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, NativeID: string(domain.NewID()), Accepted: true, Result: &claude.NativeResult{Kind: claude.ResultSuccess, Reason: claude.Completed, Usage: &claude.ResultUsage{}}}
	if _, err := c.PublishUsageObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	command := claude.LifecycleObservation{Kind: claude.CommandObserved, SessionID: o.SessionID, InputID: o.InputID, TurnID: o.TurnID, NativeID: string(domain.NewID()), Accepted: true, Command: claude.CommandCompleted}
	idle := claude.LifecycleObservation{Kind: claude.RunStateObserved, SessionID: o.SessionID, TurnID: o.TurnID, NativeID: string(domain.NewID()), Run: &claude.NativeRunObservation{State: claude.RunIdle}}
	return c, rpc, command, idle
}

func TestClaudeTerminalRequiresSeparateOriginalIdleAndReplaysOnlyItsReceipt(t *testing.T) {
	c, rpc, command, idle := claudeTerminalFixture(t)
	ctx := context.Background()
	before := len(rpc.events)
	if handled, err := c.PublishBoundaryObservation(ctx, command); !handled || err != nil || len(rpc.events) != before {
		t.Fatal("command closure became terminal", err)
	}
	if _, err := c.Complete(ctx, nil); err == nil {
		t.Fatal("unpublished terminal granted cleanup")
	}
	rpc.lose = true
	if handled, err := c.PublishBoundaryObservation(ctx, idle); !handled || err == nil {
		t.Fatal("terminal acknowledgment not lost")
	}
	if c.terminal != nil || c.completion != nil {
		t.Fatal("uncertain publication acquired completion")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) || c.terminal == nil || c.binding.stage != claudeTerminalPublished {
		t.Fatal("original terminal receipt or stage changed")
	}
	if _, err := c.PublishBoundaryObservation(ctx, idle); err == nil {
		t.Fatal("terminal published twice")
	}
	if _, err := c.Complete(ctx, nil); err == nil || c.completion != nil {
		t.Fatal("terminal fabricated process cleanup")
	}
}

func TestClaudeTerminalRejectsUncorrelatedUnsettledAndForeignBoundaries(t *testing.T) {
	for _, scenario := range []string{"missing-command", "command-input", "idle-input", "session", "turn", "reused-id", "pending-task", "pending-compaction", "automatic-failure", "automatic-origin", "missing-usage", "pending-tool", "canceled-success", "interruption"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, command, idle := claudeTerminalFixture(t)
			ctx := context.Background()
			if scenario == "command-input" {
				command.InputID = domain.NewID()
				if _, err := c.PublishBoundaryObservation(ctx, command); err == nil {
					t.Fatal("foreign command accepted")
				}
				return
			}
			if scenario == "canceled-success" {
				command.Command = claude.CommandCancelled
			}
			if scenario != "missing-command" {
				if _, err := c.PublishBoundaryObservation(ctx, command); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "idle-input":
				idle.InputID = c.binding.journal.InputID
			case "session":
				idle.SessionID = domain.NewID()
			case "turn":
				idle.TurnID = string(domain.NewID())
			case "reused-id":
				idle.NativeID = command.NativeID
			case "pending-compaction":
				c.binding.compaction = &domain.ClaudeCompactionState{Pending: &domain.ClaudeCompactionPending{}}
			case "pending-task":
				idle.Run.KnownWork.PendingTasks = 1
			case "automatic-failure":
				idle.Run.ContinuationFailed = true
			case "automatic-origin":
				origin := claude.TaskNotificationOrigin
				c.resultBoundary.Origin = &origin
			case "missing-usage":
				c.resultUsageNativeID = ""
			case "pending-tool":
				c.active = "unfinished"
			case "interruption":
				c.interruption = &claudePublishedInterruption{}
			}
			before := len(rpc.events)
			if _, err := c.PublishBoundaryObservation(ctx, idle); err == nil || len(rpc.events) != before || c.binding.stage != claudeBindingBlocked {
				t.Fatal("unproved native boundary became terminal")
			}
		})
	}
}
