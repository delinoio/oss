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

func grokServerStopFixture(t *testing.T, kind domain.GrokStopKind) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f := grokServerAccepted(t)
	text := grokServerText(f)
	sequence := uint64(3)
	if kind != domain.GrokInterruptedBeforeText {
		f.publish(t, text)
		sequence++
	}
	usage := grokServerResponse(f, 4, 1)
	if kind == domain.GrokCompletedDuringStop {
		f.publish(t, usage)
		sequence++
	}
	e := f.event(domain.ExecutionTurnFinished, sequence)
	digest := sha256.Sum256([]byte(text.GrokText.Text))
	contextTokens := "18446744073709551615"
	v := &domain.GrokStopObservation{Kind: domain.GrokInterruptedText, RequestID: domain.NewID(), InputID: f.input.InputID, InputRequestID: f.input.TurnRequestID, MessageID: text.GrokText.ID, NativeEventID: string(f.thread) + "-12", TimestampMS: "1", ElapsedMS: "2", Model: f.input.Configuration.NativeModel, Category: domain.GrokMidTurnAbort, ContextTokens: &contextTokens, OutputDigest: hex.EncodeToString(digest[:]), TextChunks: 1, Delivered: true, Idle: true, CleanupJoined: true, Retries: []domain.GrokStopRetry{{NativeEventID: string(f.thread) + "-11", TimestampMS: "1", Kind: domain.GrokRetrying, Error: domain.GrokHTTPRetry, Attempt: "1", MaxRetries: "3"}}}
	if kind == domain.GrokInterruptedBeforeText {
		empty := sha256.Sum256(nil)
		v.Kind, v.MessageID, v.TextChunks, v.OutputDigest = kind, "", 0, hex.EncodeToString(empty[:])
	}
	if kind == domain.GrokCompletedDuringStop {
		v.Kind, v.Category, v.ContextTokens = domain.GrokCompletedDuringStop, "", nil
		v.Retries = nil
		v.Completed = &domain.GrokStopCompletion{Counts: usage.GrokUsage.Counts, TotalTokens: "16", ModelCalls: "1", APIDurationMS: "2", Turns: "1"}
	}
	e.Outcome, e.GrokStop = v.Outcome(), v
	return f, e
}

func TestGrokStopPreservesOriginalOutcomePartialTextAndSeparateCleanup(t *testing.T) {
	for _, kind := range []domain.GrokStopKind{domain.GrokInterruptedBeforeText, domain.GrokInterruptedText, domain.GrokCompletedDuringStop} {
		f, e := grokServerStopFixture(t, kind)
		requestOpenCodeStopFixture(t, f)
		request := f.publish(t, e)
		if res, err := f.call(request); err != nil || !res.Msg.Replayed {
			t.Fatal("Stop receipt lost", err)
		}
		ctx := context.Background()
		if kind != domain.GrokInterruptedBeforeText {
			r, _ := f.service.Store.Get(ctx, domain.MessageKind, e.GrokStop.MessageID)
			message, err := store.Decode[domain.ExecutionMessage](r)
			if err != nil || message.State != domain.MessageComplete || (message.GrokText.Interruption != nil) == (kind == domain.GrokCompletedDuringStop) {
				t.Fatal("partial output changed response semantics", err)
			}
		}
		r, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		s, _ := store.Decode[domain.Session](r)
		if s.Execution.GrokStop == nil || s.Execution.GrokTerminal != nil || s.Execution.CleanupVerified || s.Execution.Outcome != e.Outcome {
			t.Fatal("Stop invented history or workspace cleanup")
		}
		if kind != domain.GrokCompletedDuringStop && (s.Execution.LatestUsageID != "" || s.Execution.GrokContent != nil && s.Execution.GrokContent.Responses != 0) {
			t.Fatal("interrupted usage was fabricated")
		}
		if kind == domain.GrokInterruptedBeforeText && (s.Execution.GrokContent != nil || s.Execution.GrokStop.MessageID != "") {
			t.Fatal("pre-text interruption fabricated a message")
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
	for _, name := range []string{"not-canceled", "input", "operation", "model", "message", "digest", "chunks", "event", "delivery", "cleanup", "category", "context", "usage", "outcome", "reused-request", "retry-event", "retry-count", "retry-collision", "mixed", "counter", "hidden-output"} {
		t.Run(name, func(t *testing.T) {
			kind := domain.GrokInterruptedText
			if name == "counter" {
				kind = domain.GrokCompletedDuringStop
			}
			f, e := grokServerStopFixture(t, kind)
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
			case "hidden-output":
				empty := sha256.Sum256(nil)
				e.GrokStop.Kind, e.GrokStop.MessageID, e.GrokStop.TextChunks, e.GrokStop.OutputDigest = domain.GrokInterruptedBeforeText, "", 0, hex.EncodeToString(empty[:])
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

func TestGrokBeforeTextStopRejectsInventedOutputAndUnownedFacts(t *testing.T) {
	for _, name := range []string{"not-canceled", "message", "chunks", "digest", "usage", "context", "rounded-context", "input", "operation", "model", "outcome"} {
		t.Run(name, func(t *testing.T) {
			f, e := grokServerStopFixture(t, domain.GrokInterruptedBeforeText)
			if name != "not-canceled" {
				requestOpenCodeStopFixture(t, f)
			}
			v := e.GrokStop
			switch name {
			case "message":
				v.MessageID = domain.NewID()
			case "chunks":
				v.TextChunks = 1
			case "digest":
				v.OutputDigest = strings.Repeat("ab", 32)
			case "usage":
				v.Completed = &domain.GrokStopCompletion{}
			case "context":
				v.ContextTokens = nil
			case "rounded-context":
				*v.ContextTokens = "18446744073709551616"
			case "input":
				v.InputID = domain.NewID()
			case "operation":
				v.InputRequestID = domain.NewID()
			case "model":
				v.Model = "foreign"
			case "outcome":
				e.Outcome = domain.ExecutionSucceeded
			}
			ctx := context.Background()
			before, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unproved pre-text Stop accepted")
			}
			after, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected pre-text Stop changed state")
			}
		})
	}
}
