package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func grokServerStopFixture(t *testing.T, completed bool) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f := grokServerAccepted(t)
	text := grokServerText(f)
	f.publish(t, text)
	sequence := uint64(4)
	usage := grokServerResponse(f, 4, 1)
	if completed {
		f.publish(t, usage)
		sequence++
	}
	e := f.event(domain.ExecutionTurnFinished, sequence)
	digest := sha256.Sum256([]byte(text.GrokText.Text))
	contextTokens := "18446744073709551615"
	v := &domain.GrokStopObservation{Kind: domain.GrokInterruptedText, RequestID: domain.NewID(), InputID: f.input.InputID, InputRequestID: f.input.TurnRequestID, MessageID: text.GrokText.ID, NativeEventID: string(f.thread) + "-12", TimestampMS: "1", ElapsedMS: "2", Model: f.input.Configuration.NativeModel, Category: domain.GrokMidTurnAbort, ContextTokens: &contextTokens, OutputDigest: hex.EncodeToString(digest[:]), TextChunks: 1, Delivered: true, Idle: true, CleanupJoined: true, Retries: []domain.GrokStopRetry{{NativeEventID: string(f.thread) + "-11", TimestampMS: "1", Kind: domain.GrokRetrying, Error: domain.GrokHTTPRetry, Attempt: "1", MaxRetries: "3"}}}
	if completed {
		v.Kind, v.Category, v.ContextTokens = domain.GrokCompletedDuringStop, "", nil
		v.Retries = nil
		v.Completed = &domain.GrokStopCompletion{Counts: usage.GrokUsage.Counts, TotalTokens: "16", ModelCalls: "1", APIDurationMS: "2", Turns: "1"}
	}
	e.Outcome, e.GrokStop = v.Outcome(), v
	return f, e
}

func TestGrokStopPreservesOriginalOutcomePartialTextAndSeparateCleanup(t *testing.T) {
	for _, completed := range []bool{false, true} {
		f, e := grokServerStopFixture(t, completed)
		requestOpenCodeStopFixture(t, f)
		request := f.publish(t, e)
		if res, err := f.call(request); err != nil || !res.Msg.Replayed {
			t.Fatal("Stop receipt lost", err)
		}
		ctx := context.Background()
		r, _ := f.service.Store.Get(ctx, domain.MessageKind, e.GrokStop.MessageID)
		message, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil || message.State != domain.MessageComplete || (message.GrokText.Interruption != nil) == completed {
			t.Fatal("partial output changed response semantics", err)
		}
		r, _ = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		s, _ := store.Decode[domain.Session](r)
		if s.Execution.GrokStop == nil || s.Execution.GrokTerminal != nil || s.Execution.CleanupVerified || s.Execution.Outcome != e.Outcome {
			t.Fatal("Stop invented history or workspace cleanup")
		}
		if !completed && (s.Execution.LatestUsageID != "" || s.Execution.GrokContent.Responses != 0) {
			t.Fatal("interrupted usage was fabricated")
		}
		proof := domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: e.Sequence, Outcome: e.Outcome, CleanupVerified: true}
		f.reportCompletion(t, proof)
		r, _ = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		s, _ = store.Decode[domain.Session](r)
		if !s.Execution.CleanupVerified || s.ActiveExecutionID != "" || s.Dispatch != domain.DispatchPaused || s.Outcome != domain.ExecutionStopped {
			t.Fatal("Stop report lost product cancellation or granted continuation")
		}
	}
}

func TestGrokStopRejectsUnownedAndChangedFactsAtomically(t *testing.T) {
	for _, name := range []string{"not-canceled", "input", "operation", "model", "message", "digest", "chunks", "event", "delivery", "cleanup", "category", "context", "usage", "outcome", "reused-request", "retry-event", "retry-count", "retry-collision", "mixed", "counter"} {
		t.Run(name, func(t *testing.T) {
			f, e := grokServerStopFixture(t, name == "counter")
			original := e.GrokStop.MessageID
			if name != "not-canceled" {
				requestOpenCodeStopFixture(t, f)
			}
			switch name {
			case "input":
				e.GrokStop.InputID = domain.NewID()
			case "operation":
				e.GrokStop.InputRequestID = domain.NewID()
			case "model":
				e.GrokStop.Model = "foreign"
			case "message":
				e.GrokStop.MessageID = domain.NewID()
			case "digest":
				e.GrokStop.OutputDigest = strings.Repeat("ab", 32)
			case "chunks":
				e.GrokStop.TextChunks++
			case "event":
				e.GrokStop.NativeEventID = string(f.thread) + "-10"
			case "delivery":
				e.GrokStop.Delivered = false
			case "cleanup":
				e.GrokStop.CleanupJoined = false
			case "category":
				e.GrokStop.Category = "unknown"
			case "context":
				e.GrokStop.ContextTokens = nil
			case "usage":
				e.GrokStop.Completed = &domain.GrokStopCompletion{}
			case "outcome":
				e.Outcome = domain.ExecutionSucceeded
			case "reused-request":
				e.GrokStop.RequestID = f.input.ThreadRequestID
			case "retry-event":
				e.GrokStop.Retries[0].NativeEventID = e.GrokStop.NativeEventID
			case "retry-count":
				e.GrokStop.Retries[0].Attempt = "01"
			case "retry-collision":
				e.GrokStop.Retries[0].NativeEventID = string(f.thread) + "-10"
			case "mixed":
				e.GrokTerminal = &domain.GrokTextTerminal{}
			case "counter":
				e.GrokStop.Completed.Counts.Input = "12"
			}
			ctx := context.Background()
			before, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			textBefore, _ := f.service.Store.Get(ctx, domain.MessageKind, original)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unproved Stop accepted")
			}
			after, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			textAfter, _ := f.service.Store.Get(ctx, domain.MessageKind, original)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(textBefore, textAfter) {
				t.Fatal("rejected Stop partly changed retained state")
			}
		})
	}
}
