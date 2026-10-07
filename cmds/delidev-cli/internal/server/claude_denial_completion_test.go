package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func denialCompletionPublicationFixture(t *testing.T) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f, c, result := claudeInterruptionFixture(t, domain.ClaudeUserQuestion, true, false, true)
	m := &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: string(f.input.InputID), Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
	for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e := f.event(kind, c.Sequence+uint64(i))
		e.Message = m
		f.publish(t, e)
	}
	c.Sequence += 2
	result.Sequence += 2
	f.publish(t, c)
	f.publish(t, result)
	e := f.event(domain.ExecutionTurnFinished, result.Sequence+1)
	e.Outcome = domain.ExecutionStopped
	e.ClaudeDenial = &domain.ClaudeDenialCompletion{InputID: f.input.InputID, InteractionID: c.ClaudeInterruption.Observation.InteractionID, ArrivalID: c.ClaudeInterruption.Observation.ArrivalID, ContextID: c.ClaudeInterruption.ID, ResultID: result.ClaudeInterruption.ID, CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), CleanupVerified: true}
	return f, e
}
func TestClaudeDenialCompletionRetainsOriginalResultAndIndependentRecoveryAndCleanupReport(t *testing.T) {
	f, e := denialCompletionPublicationFixture(t)
	receipt := f.publish(t, e)
	if replay, err := f.call(receipt); err != nil || !replay.Msg.Replayed {
		t.Fatal("denial receipt lost", err)
	}
	row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](row)
	if err != nil || s.Execution.ClaudeDenial == nil || s.Execution.ClaudeTerminal != nil || s.Execution.ClaudeStop != nil || s.Execution.CleanupVerified || s.Outcome != domain.ExecutionStopped || s.Execution.Outcome != domain.ExecutionStopped || s.Recovery != domain.NeedsRecovery || s.Dispatch != domain.DispatchPaused {
		t.Fatal("denial terminal changed input outcome or cleanup", err)
	}
	report := domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(e.NativeThreadID), NativeTurnID: domain.NativeIdentity(e.NativeTurnID), LastSequence: e.Sequence, Outcome: domain.ExecutionStopped, CleanupVerified: true}
	f.reportCompletion(t, report)
	row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, err = store.Decode[domain.Session](row)
	if err != nil || !s.Execution.CleanupVerified || s.Recovery != domain.NeedsRecovery || s.ActiveExecutionID != f.input.ExecutionID || s.Dispatch != domain.DispatchPaused || s.NextExecutionIntent != "" {
		t.Fatal("denial cleanup erased original recovery or granted continuation", err)
	}
}
func TestClaudeDenialCompletionRejectsMixedForeignOrMissingFactsAtomically(t *testing.T) {
	for _, scenario := range []string{"input", "arrival", "context", "result", "interaction", "reused", "native-input", "cleanup", "turn", "outcome", "mixed", "progress-after-result"} {
		t.Run(scenario, func(t *testing.T) {
			f, e := denialCompletionPublicationFixture(t)
			v := e.ClaudeDenial
			switch scenario {
			case "input":
				v.InputID = domain.NewID()
			case "arrival":
				v.ArrivalID = domain.NewID()
			case "context":
				v.ContextID = domain.NewID()
			case "result":
				v.ResultID = domain.NewID()
			case "interaction":
				v.InteractionID = domain.NewID()
			case "reused":
				v.CommandNativeID = string(f.input.InputID)
			case "native-input":
				v.NativeInputID = &v.InputID
			case "cleanup":
				v.CleanupVerified = false
			case "turn":
				v.IdleNativeID = e.NativeTurnID
			case "outcome":
				e.Outcome = domain.ExecutionSucceeded
			case "mixed":
				e.ClaudeTerminal = &domain.ClaudeTerminalObservation{}
			case "progress-after-result":
				e = claudeProgressEvent(f, e.Sequence, true)
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			_, err := f.call(f.requestEvent(t, e))
			if scenario == "cleanup" {
				if err != nil {
					t.Fatal("unconfirmed cleanup blocked the native observation", err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid denial acquired completion")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if after.Revision != before.Revision {
				t.Fatal("denial rejection partially committed")
			}
		})
	}
}
