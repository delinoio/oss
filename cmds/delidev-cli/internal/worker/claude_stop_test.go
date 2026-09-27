package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudeStopFixtureController struct {
	observation claude.InterruptObservation
	sends       int
	closes      int
	failCleanup bool
}

func (a *claudeStopFixtureController) Interrupt(ctx context.Context, request domain.ID, claim func(context.Context, claude.InterruptClaim) error) (claude.InterruptObservation, error) {
	a.observation.Claim.RequestID = request
	if err := claim(ctx, a.observation.Claim); err != nil {
		return a.observation, err
	}
	a.sends++
	a.observation.Claimed, a.observation.Attempted, a.observation.Acknowledged = true, true, true
	return a.observation, nil
}
func (a *claudeStopFixtureController) Close() error {
	a.closes++
	if a.failCleanup {
		return publicationUncertain()
	}
	a.observation.CleanupJoined = true
	return nil
}
func (a *claudeStopFixtureController) InspectInterrupt() (claude.InterruptObservation, error) {
	return a.observation, nil
}

func claudeStopWorkerFixture(t *testing.T) (*ClaudeContentPublisher, *openCodeBindingRPC, *claudeStopFixtureController, []claude.LifecycleObservation) {
	t.Helper()
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	i, text, empty := uint32(0), "Original partial", ""
	for _, content := range []claude.ContentEvent{{Kind: claude.ProviderMessageStarted}, {Kind: claude.ContentStarted, Index: &i, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &empty}}, {Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.TextDelta, Delta: &text}} {
		if _, err := c.PublishObservation(ctx, claudeContentObservation(c, content)); err != nil {
			t.Fatal(err)
		}
	}
	b := c.binding
	a := &claudeStopFixtureController{observation: claude.InterruptObservation{Claim: claude.InterruptClaim{Version: 1, OwnerID: b.journal.JobID, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn}}}
	if err := c.RequestStop(ctx, a, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	partial := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentInterrupted, Index: &i, Block: &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &text}, Usage: &claude.ProviderUsage{}})
	marker := domain.ClaudeStopContextText
	contextEvent := claude.LifecycleObservation{Kind: claude.ContentObserved, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, NativeID: string(domain.NewID()), Accepted: true, Content: []claude.ContentEvent{{Kind: claude.NativeInterruptContext, Blocks: []claude.NativeContentBlock{{Kind: claude.TextBlock, Text: &marker}}}}}
	result := claude.LifecycleObservation{Kind: claude.InterruptResultObserved, SessionID: b.journal.SessionID, TurnID: b.turn, NativeID: string(domain.NewID()), Result: &claude.NativeResult{Kind: claude.ResultExecutionError, Reason: claude.AbortedStreaming, Error: true, Usage: &claude.ResultUsage{}}}
	command := claude.LifecycleObservation{Kind: claude.CommandObserved, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, NativeID: string(domain.NewID()), Accepted: true, Command: claude.CommandCancelled}
	idle := claude.LifecycleObservation{Kind: claude.RunStateObserved, SessionID: b.journal.SessionID, TurnID: b.turn, NativeID: string(domain.NewID()), Run: &claude.NativeRunObservation{State: claude.RunIdle}}
	return c, rpc, a, []claude.LifecycleObservation{partial, contextEvent, result, command, idle}
}

func TestClaudeStopReplaysOnlyOriginalPublicationAfterJoinedCleanup(t *testing.T) {
	c, rpc, api, events := claudeStopWorkerFixture(t)
	ctx := context.Background()
	for _, e := range events {
		if handled, err := c.ObserveStop(e); !handled || err != nil {
			t.Fatal("original Stop observation rejected", err)
		}
	}
	if _, err := c.StoppedCompletion(); err == nil {
		t.Fatal("idle fabricated cleanup")
	}
	api.observation.Idle = true
	api.observation.NativeResult = &claude.NativeResult{Kind: claude.ResultExecutionError, Reason: claude.AbortedStreaming, Error: true}
	rpc.lose = true
	before := len(rpc.events)
	if _, err := c.PublishStopped(ctx, api); err == nil {
		t.Fatal("Stop acknowledgment was not lost")
	}
	if _, err := c.StoppedCompletion(); err == nil {
		t.Fatal("lost terminal acknowledgment authorized a report")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	proof, err := c.StoppedCompletion()
	if err != nil || proof.Outcome != domain.ExecutionStopped || proof.Version != 1 || !proof.CleanupVerified || api.sends != 1 || api.closes != 1 || rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
		t.Fatal("Stop replay repeated native work or changed its original report", err)
	}
	if c.RequestStop(ctx, api, domain.NewID()) == nil || api.sends != 1 {
		t.Fatal("completed Stop replayed native interruption")
	}
}

func TestClaudeStopRejectsMissingOriginalResultOwnershipAndCleanup(t *testing.T) {
	for _, scenario := range []string{"partial-text", "result-input", "result-reason", "idle-input", "unfinished-work", "cleanup", "foreign-controller", "correlated-controller", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			c, rpc, api, events := claudeStopWorkerFixture(t)
			switch scenario {
			case "partial-text":
				value := "altered"
				events[0].Content[0].Block.Text = &value
			case "result-input":
				events[2].InputID = c.binding.journal.InputID
			case "result-reason":
				events[2].Result.Reason = claude.Completed
			case "idle-input":
				events[4].InputID = c.binding.journal.InputID
			case "unfinished-work":
				events[4].Run.KnownWork.PendingTasks = 1
			case "duplicate":
				events[2].NativeID = events[1].NativeID
			}
			before, rejected := len(rpc.events), false
			for _, e := range events {
				if _, err := c.ObserveStop(e); err != nil {
					rejected = true
					break
				}
			}
			cleanupCase := scenario == "cleanup" || scenario == "foreign-controller" || scenario == "correlated-controller"
			if !cleanupCase && !rejected {
				t.Fatal("altered Stop observations accepted")
			}
			if cleanupCase {
				api.observation.Idle = true
				api.observation.NativeResult = &claude.NativeResult{Kind: claude.ResultExecutionError, Reason: claude.AbortedStreaming, Error: true}
				if scenario == "cleanup" {
					api.failCleanup = true
				}
				if scenario == "foreign-controller" {
					api.observation.Claim.OwnerID = domain.NewID()
				}
				if scenario == "correlated-controller" {
					api.observation.ResultCorrelated = true
				}
				if _, err := c.PublishStopped(context.Background(), api); err == nil {
					t.Fatal("unproved owned cleanup published")
				}
			}
			if len(rpc.events) != before {
				t.Fatal("failed Stop changed publication")
			}
		})
	}
}
