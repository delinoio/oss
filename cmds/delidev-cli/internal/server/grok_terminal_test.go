package server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func grokServerTerminalFixture(t *testing.T) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f := grokServerAccepted(t)
	f.publish(t, grokServerText(f))
	usage := grokServerResponse(f, 4, 1)
	f.publish(t, usage)
	e := f.event(domain.ExecutionTurnFinished, 5)
	e.Outcome = domain.ExecutionSucceeded
	e.GrokTerminal = &domain.GrokTextTerminal{Kind: domain.GrokClosedFirstText, NativeEventID: string(f.thread) + "-11", TimestampMS: "1", ElapsedMS: "3", Model: f.input.Configuration.NativeModel, Counts: usage.GrokUsage.Counts, TotalTokens: "16", ModelCalls: "1", APIDurationMS: "2", Turns: "1", ClosureID: domain.NewID(), HistoryDigest: strings.Repeat("ab", 32)}
	return f, e
}
func TestGrokClosedTextTerminalAndVersionOneReport(t *testing.T) {
	f, e := grokServerTerminalFixture(t)
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("lost terminal receipt", err)
	}
	completion := domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 5, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
	f.reportCompletion(t, completion)
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, decodeErr := store.Decode[domain.Session](r)
	if err != nil || decodeErr != nil || s.Execution.GrokTerminal == nil || s.Execution.Outcome != domain.ExecutionSucceeded || !s.Execution.CleanupVerified || s.ActiveExecutionID != "" || s.Dispatch != domain.DispatchPaused {
		t.Fatal("closed text report changed continuation boundary", err, decodeErr)
	}
}
func TestGrokTerminalRejectsPartialForeignAndRegressedProof(t *testing.T) {
	for _, mutation := range []string{"missing", "model", "counter", "event", "outcome", "kind", "closure", "history", "mixed"} {
		t.Run(mutation, func(t *testing.T) {
			f, e := grokServerTerminalFixture(t)
			switch mutation {
			case "missing":
				e.GrokTerminal = nil
			case "model":
				e.GrokTerminal.Model = "foreign"
			case "counter":
				e.GrokTerminal.Counts.Input = "12"
			case "event":
				e.GrokTerminal.NativeEventID = string(f.thread) + "-10"
			case "outcome":
				e.Outcome = domain.ExecutionStopped
			case "kind":
				e.GrokTerminal.Kind = "tool-input"
			case "closure":
				e.GrokTerminal.ClosureID = f.input.TurnRequestID
			case "history":
				e.GrokTerminal.HistoryDigest = ""
			case "mixed":
				e.GrokUsage = &domain.GrokResponseUsage{Ordinal: 2, Counts: e.GrokTerminal.Counts}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid terminal accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("invalid terminal changed state")
			}
		})
	}
}
