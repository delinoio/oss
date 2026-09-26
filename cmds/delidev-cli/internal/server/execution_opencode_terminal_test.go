package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestOpenCodeTerminalRequiresPublishedInputAndFinalSourceWithoutCleanup(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	u := originalOpenCodeUsage()
	u.Source = domain.OpenCodeMessageUsage
	u.NativeID = u.NativeParentID
	e := f.event(domain.ExecutionOpenCodeUsageObserved, 3)
	e.ObservationID = domain.NewID()
	e.OpenCodeUsage = &u
	f.publish(t, e)
	terminal := func(sequence uint64) domain.ExecutionEvent {
		e := f.event(domain.ExecutionTurnFinished, sequence)
		e.Outcome = domain.ExecutionSucceeded
		return e
	}
	if _, err := f.call(f.requestEvent(t, terminal(4))); err == nil {
		t.Fatal("final usage alone fabricated a complete transcript")
	}
	message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: "prt_01960dcbe1fbabcdefghijklmn", NativeParentID: string(f.turn), InputID: f.input.InputID, Role: domain.UserMessage, Text: f.input.Input.Prompt}
	e = f.event(domain.ExecutionMessageStarted, 4)
	e.Message = &message
	f.publish(t, e)
	if _, err := f.call(f.requestEvent(t, terminal(5))); err == nil {
		t.Fatal("streaming original input fabricated a complete transcript")
	}
	e = f.event(domain.ExecutionMessageCompleted, 5)
	e.Message = &message
	f.publish(t, e)
	request := f.publish(t, terminal(6))
	if result, err := f.call(request); err != nil || !result.Msg.Replayed {
		t.Fatal("terminal receipt was not retained")
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, decodeErr := store.Decode[domain.Session](r)
	if err != nil || decodeErr != nil || s.Execution.Outcome != domain.ExecutionSucceeded || s.Outcome != domain.ExecutionSucceeded || s.Execution.CleanupVerified || s.Execution.LastSequence != 6 {
		t.Fatal("native terminal lost outcome or fabricated cleanup")
	}
	inbox, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(inbox) != 1 {
		t.Fatal("terminal retry duplicated or omitted completion inbox")
	}
	e = f.event(domain.ExecutionOpenCodeUsageObserved, 7)
	e.ObservationID = domain.NewID()
	e.OpenCodeUsage = &u
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("completed execution regained publication authority")
	}
}
