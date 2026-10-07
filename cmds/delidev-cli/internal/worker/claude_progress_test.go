package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func claudeProgressFixture(t *testing.T) (*ClaudeBindingPublisher, *openCodeBindingRPC, claude.LifecycleObservation, claude.LifecycleObservation) {
	t.Helper()
	b, rpc := newClaudeBindingFixture(t, domain.PlanMode)
	init, accepted, applied := claudeBindingObservations(b)
	claimClaudeInput(t, b)
	if err := b.BindSession(context.Background(), init, applied); err != nil {
		t.Fatal(err)
	}
	status := claude.SessionRequesting
	o := claude.LifecycleObservation{Kind: claude.ProgressObserved, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, NativeID: string(domain.NewID()), Progress: &claude.NativeProgressObservation{Kind: claude.SessionStatusObserved, Status: &status}}
	return b, rpc, o, accepted
}

func TestClaudeProgressKeepsPreAcceptanceAndSharedOutboxSequence(t *testing.T) {
	b, rpc, o, accepted := claudeProgressFixture(t)
	ctx := context.Background()
	rpc.lose = true

	if handled, err := b.PublishProgressObservation(ctx, o); !handled || err == nil {
		t.Fatal("original progress acknowledgment not lost")
	}
	if b.AcceptInput(ctx, accepted) == nil {
		t.Fatal("input overtook pending progress")
	}
	rpc.lose = false
	if err := b.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if b.stage != claudeSessionBound || len(rpc.events) != 3 || rpc.requests[1] != rpc.requests[2] || !bytes.Equal(rpc.events[1], rpc.events[2]) {
		t.Fatal("progress receipt changed or accepted input")
	}
	if err := b.AcceptInput(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if b.sequence != 3 {
		t.Fatal("acceptance replaced original status sequence")
	}
	c, err := OpenClaudeContentPublisher(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	o.Accepted, o.NativeID = true, string(domain.NewID())
	o.Progress = &claude.NativeProgressObservation{Kind: claude.ThinkingTokensEstimated, Thinking: &claude.NativeThinkingEstimate{Tokens: ^uint64(0), Delta: 0}}
	if handled, err := b.PublishProgressObservation(ctx, o); !handled || err != nil {
		t.Fatal(err)
	}
	var event domain.ExecutionEvent
	if domain.Decode(rpc.events[len(rpc.events)-1], &event) != nil || event.ClaudeProgress == nil || event.ClaudeProgress.Observation.Thinking.Tokens != "18446744073709551615" || event.ClaudeProgress.Observation.Thinking.Delta != "0" || event.Sequence != 6 {
		t.Fatal("estimate rounded or replaced shared content sequence")
	}
}

func TestClaudeProgressRejectsForeignMixedAndMisorderedObservations(t *testing.T) {
	for _, scenario := range []string{"input", "turn", "session", "accepted", "early-thinking", "mixed", "duplicate", "init-identity"} {
		t.Run(scenario, func(t *testing.T) {
			b, rpc, o, _ := claudeProgressFixture(t)
			switch scenario {
			case "input":
				o.InputID = domain.NewID()
			case "turn":
				o.TurnID = string(domain.NewID())
			case "session":
				o.SessionID = domain.NewID()
			case "accepted":
				o.Accepted = true
			case "early-thinking":
				o.Progress = &claude.NativeProgressObservation{Kind: claude.ThinkingTokensEstimated, Thinking: &claude.NativeThinkingEstimate{}}
			case "mixed":
				o.Progress.ToolID = "foreign"
			case "duplicate":
				if _, err := b.PublishProgressObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			case "init-identity":
				o.NativeID = b.turn
			}
			before := len(rpc.events)
			if scenario == "session" {
				_, err := b.PublishProgressObservation(context.Background(), o)
				if err != nil {
					t.Fatal("session metadata blocked observation", err)
				}
				return
			}
			if handled, err := b.PublishProgressObservation(context.Background(), o); !handled || err == nil || len(rpc.events) != before {
				t.Fatal("invalid progress published")
			}
		})
	}
}

func TestClaudeAPIRetryProgressReplaysOnlyOriginalReceipt(t *testing.T) {
	for _, afterAcceptance := range []bool{false, true} {
		b, rpc, o, accepted := claudeProgressFixture(t)
		ctx := context.Background()
		if afterAcceptance {
			if err := b.AcceptInput(ctx, accepted); err != nil {
				t.Fatal(err)
			}
			o.Accepted = true
		}
		o.Progress = &claude.NativeProgressObservation{Kind: claude.APIRetryObserved, APIRetry: &claude.NativeAPIRetryObservation{Attempt: 9007199254740993, MaxRetries: ^uint64(0), Error: "unknown"}}
		before := b.sequence
		rpc.lose = true
		if handled, err := b.PublishProgressObservation(ctx, o); !handled || err == nil {
			t.Fatal("retry acknowledgment not lost")
		}
		rpc.lose = false
		if err := b.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		n := len(rpc.events)
		if !bytes.Equal(rpc.events[n-1], rpc.events[n-2]) || rpc.requests[n-1] != rpc.requests[n-2] || b.sequence != before+1 {
			t.Fatal("retry publication changed the original receipt")
		}
		var event domain.ExecutionEvent
		if domain.Decode(rpc.events[n-1], &event) != nil || event.ClaudeProgress == nil {
			t.Fatal("retry lost")
		}
		v := event.ClaudeProgress.Observation
		if v.InputAccepted != afterAcceptance || v.APIRetry == nil || v.APIRetry.Attempt != "9007199254740993" || v.APIRetry.MaxRetries != "18446744073709551615" || v.APIRetry.DelayMS != "0" || v.APIRetry.ErrorStatus != nil {
			t.Fatal("retry values changed")
		}
		o.NativeID = string(domain.NewID())
		o.Progress.Status = new(claude.SessionStatus)
		n = len(rpc.events)
		if handled, err := b.PublishProgressObservation(ctx, o); !handled || err == nil || len(rpc.events) != n {
			t.Fatal("mixed retry was published")
		}
	}
}
