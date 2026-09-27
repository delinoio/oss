package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeStopPublicationFixture(t *testing.T) (*publicationFixture, domain.ExecutionEvent) {
	t.Helper()
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	message := &domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: string(f.input.InputID), Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
	for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		e := f.event(kind, uint64(3+i))
		e.Message = message
		f.publish(t, e)
	}
	index, partial := uint32(0), "Original stopped response."
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original_stop", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	e := f.event(domain.ExecutionClaudeMessageObserved, 5)
	e.ClaudeMessage = &u
	f.publish(t, e)
	u.Mutation, u.Index, u.Block = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: domain.ClaudeText}
	e.Sequence++
	f.publish(t, e)
	u.Mutation, u.Block, u.Delta = domain.ClaudeBlockAppend, nil, &partial
	e.Sequence++
	f.publish(t, e)
	v := &domain.ClaudeStopObservation{ContentEvidence: domain.ClaudeAbortedAssistant, RequestID: domain.NewID(), InputID: f.input.InputID, MessageID: u.ID, NativeMessageID: u.NativeID, InterruptedNativeID: string(domain.NewID()), ContextNativeID: string(domain.NewID()), ResultNativeID: string(domain.NewID()), CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), Text: partial, Context: domain.ClaudeStopContextText, Kind: domain.ClaudeResultExecutionError, Reason: domain.ClaudeAbortedStreaming, Error: true, Command: domain.ClaudeCommandCancelled, Usage: &domain.ClaudeResultUsage{}, Acknowledged: true, Idle: true, CleanupVerified: true}
	e = f.event(domain.ExecutionTurnFinished, 8)
	e.Outcome, e.ClaudeStop = domain.ExecutionStopped, v
	return f, e
}

func TestClaudeStopFinalizesOnlyOriginalPartialAndRequiresSeparateReport(t *testing.T) {
	f, e := claudeStopPublicationFixture(t)
	requestOpenCodeStopFixture(t, f)
	request := f.publish(t, e)
	if replay, err := f.call(request); err != nil || !replay.Msg.Replayed {
		t.Fatal("original Stop receipt changed", err)
	}
	row, _ := f.service.Store.Get(context.Background(), domain.MessageKind, e.ClaudeStop.MessageID)
	m, err := store.Decode[domain.ExecutionMessage](row)
	if err != nil || m.State != domain.MessageComplete || m.Claude.Blocks[0].State != domain.ClaudeBlockInterrupted || m.Claude.Blocks[0].Block.Text != e.ClaudeStop.Text || m.Claude.Interruption.NativeEventID != e.ClaudeStop.InterruptedNativeID {
		t.Fatal("Stop completed or replaced original partial content", err)
	}
	row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, _ := store.Decode[domain.Session](row)
	if s.Execution.ClaudeStop == nil || s.Execution.ClaudeTerminal != nil || s.Execution.CleanupVerified || s.Execution.Outcome != domain.ExecutionStopped {
		t.Fatal("Stop invented native input outcome or workspace cleanup")
	}
	completion := f.completion()
	completion.LastSequence, completion.Outcome = e.Sequence, domain.ExecutionStopped
	f.reportCompletion(t, completion)
	row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, _ = store.Decode[domain.Session](row)
	if !s.Execution.CleanupVerified || s.Dispatch != domain.DispatchPaused || s.Recovery != domain.NoRecovery || s.ActiveExecutionID != "" {
		t.Fatal("Stop report lost cleanup or authorized another input")
	}
}

func TestClaudeStopRejectsForeignOwnershipAndRollsBackPartialClosure(t *testing.T) {
	for _, scenario := range []string{"not-canceled", "input", "message", "text", "init-reuse", "retry-reuse", "unfinished-other", "unconfirmed", "success", "mixed"} {
		t.Run(scenario, func(t *testing.T) {
			f, e := claudeStopPublicationFixture(t)
			original := e.ClaudeStop.MessageID
			if scenario != "not-canceled" {
				requestOpenCodeStopFixture(t, f)
			}
			switch scenario {
			case "input":
				e.ClaudeStop.InputID = domain.NewID()
			case "message":
				e.ClaudeStop.MessageID = domain.NewID()
			case "text":
				e.ClaudeStop.Text = "Changed partial"
			case "init-reuse":
				e.ClaudeStop.ContextNativeID = e.NativeTurnID
			case "retry-reuse":
				e.ClaudeStop.Retries = []domain.ClaudeStopRetryObservation{{NativeEventID: e.NativeTurnID, Attempt: "1", MaxRetries: "2", DelayMS: "10", Error: domain.ClaudeAPIUnknown}}
			case "unfinished-other":
				pending := f.event(domain.ExecutionClaudeMessageObserved, e.Sequence)
				pending.ClaudeMessage = &domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_other", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
				f.publish(t, pending)
				e.Sequence++
			case "unconfirmed":
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.unconfirmed-stop", nil, func(tx *store.Tx) (any, error) {
					r, s, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return nil, err
					}
					s.Execution.UnconfirmedResponses = 1
					return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
				})
				if err != nil {
					t.Fatal(err)
				}
			case "success":
				e.Outcome = domain.ExecutionSucceeded
			case "mixed":
				e.ClaudeTerminal = &domain.ClaudeTerminalObservation{}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unproved Stop accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			row, _ := f.service.Store.Get(context.Background(), domain.MessageKind, original)
			m, _ := store.Decode[domain.ExecutionMessage](row)
			if before.Revision != after.Revision || m.State != domain.MessageStreaming || m.Claude.Interruption != nil {
				t.Fatal("rejected Stop partially finalized original content")
			}
		})
	}
}
