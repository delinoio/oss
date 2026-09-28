package worker

import (
	"bytes"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"testing"
)

type denialCompletionController struct {
	c     *ClaudeContentPublisher
	calls int
	fail  bool
}

func (f *denialCompletionController) FinishOriginalDenial(_ context.Context, owner, session, input domain.ID, turn string, arrival domain.ID) (claude.NativeResult, error) {
	f.calls++
	b := f.c.binding
	if f.fail || owner != b.journal.JobID || session != b.journal.SessionID || input != b.journal.InputID || turn != b.turn || arrival != f.c.interruption.settlement.ArrivalID {
		return claude.NativeResult{}, publicationUncertain()
	}
	return claude.NativeResult{Kind: claude.ResultExecutionError, Reason: claude.AbortedTools, Error: true}, nil
}
func denialBoundaryFixture(t *testing.T) (*ClaudeContentPublisher, *claudeReplyFixtureRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	t.Helper()
	c, rpc, content, result := claudeInterruptionPublicationFixture(t)
	for _, o := range []claude.LifecycleObservation{content, result} {
		if _, err := c.PublishObservation(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	command := claude.LifecycleObservation{Kind: claude.CommandObserved, SessionID: content.SessionID, InputID: c.binding.journal.InputID, TurnID: content.TurnID, NativeID: string(domain.NewID()), Accepted: true, Command: claude.CommandCancelled}
	idle := claude.LifecycleObservation{Kind: claude.RunStateObserved, SessionID: content.SessionID, TurnID: content.TurnID, NativeID: string(domain.NewID()), Run: &claude.NativeRunObservation{State: claude.RunIdle}}
	return c, rpc, command, idle
}
func TestClaudeDenialCompletionRequiresCleanupAndReplaysOnlyOriginalReceipt(t *testing.T) {
	c, rpc, command, idle := denialBoundaryFixture(t)
	ctx := context.Background()
	controller := &denialCompletionController{c: c}
	for _, o := range []claude.LifecycleObservation{command, idle} {
		if handled, err := c.PublishBoundaryObservation(ctx, o); !handled || err != nil {
			t.Fatal(err)
		}
	}
	before := len(rpc.events)
	rpc.lose = true
	if _, err := c.CompleteDenial(ctx, controller); err == nil || c.completion != nil || controller.calls != 1 {
		t.Fatal("lost denial terminal acquired report")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rpc.events[before], rpc.events[before+1]) || rpc.requests[before] != rpc.requests[before+1] {
		t.Fatal("denial terminal changed original receipt")
	}
	completion, err := c.CompleteDenial(ctx, controller)
	if err != nil || completion.Outcome != domain.ExecutionStopped || !completion.CleanupVerified || controller.calls != 1 || c.terminal != nil || c.stop != nil {
		t.Fatal("denial acquired correlated result or repeated cleanup", err)
	}
	if _, err := c.CompleteDenial(ctx, controller); err != nil || controller.calls != 1 {
		t.Fatal("acknowledged denial repeated cleanup")
	}
}
func TestClaudeDenialCompletionRejectsForeignAndUnsettledBoundaries(t *testing.T) {
	for _, scenario := range []string{"missing-result", "foreign-session", "command-input", "command", "idle-input", "idle-accepted", "duplicate", "pending-task", "automatic", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, command, idle := denialBoundaryFixture(t)
			ctx := context.Background()
			controller := &denialCompletionController{c: c}
			o := command
			switch scenario {
			case "missing-result":
				c.interruption.resultID = ""
			case "foreign-session":
				o.SessionID = domain.NewID()
			case "command-input":
				o.InputID = domain.NewID()
			case "command":
				o.Command = claude.CommandCompleted
			default:
				if _, err := c.PublishBoundaryObservation(ctx, command); err != nil {
					t.Fatal(err)
				}
				o = idle
				switch scenario {
				case "idle-input":
					o.InputID = c.binding.journal.InputID
				case "idle-accepted":
					o.Accepted = true
				case "duplicate":
					o.NativeID = command.NativeID
				case "pending-task":
					o.Run.KnownWork.PendingTasks = 1
				case "automatic":
					o.Run.ContinuationFailed = true
				case "cleanup":
					controller.fail = true
				}
			}
			before := len(rpc.events)
			_, err := c.PublishBoundaryObservation(ctx, o)
			if scenario == "cleanup" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.CompleteDenial(ctx, controller)
			}
			if err == nil || len(rpc.events) != before || c.completion != nil {
				t.Fatal("unproved denial published")
			}
		})
	}
}
