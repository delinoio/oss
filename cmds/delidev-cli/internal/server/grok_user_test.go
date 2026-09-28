package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func assertGrokUserHistory(t *testing.T, db *store.Store, input domain.ExecutionJobInput, session domain.Session) {
	t.Helper()
	p := session.Execution
	if p == nil || p.GrokTerminal == nil || p.GrokTerminal.User == nil || p.GrokUserMessageID.Validate() != nil {
		t.Fatal("original user history absent")
	}
	r, err := db.Get(context.Background(), domain.MessageKind, p.GrokUserMessageID)
	message, decodeErr := store.Decode[domain.ExecutionMessage](r)
	if err != nil || decodeErr != nil || message.GrokUser == nil || *message.GrokUser != *p.GrokTerminal.User || message.NativeID != message.GrokUser.NativeEventID || message.NativeThreadID != p.NativeThreadID || message.NativeTurnID != p.NativeTurnID || message.ExecutionID != input.ExecutionID || message.InputID != input.InputID || message.Role != domain.UserMessage || message.State != domain.MessageComplete || message.Text != input.Input.Prompt || message.FirstSequence != p.LastSequence || message.LastSequence != p.LastSequence {
		t.Fatal("closed original user record changed", err, decodeErr)
	}
	if err := db.Read(context.Background(), func(tx *store.Tx) error {
		first, err := tx.GrokFirstTextMessage(input.ExecutionID)
		if err == nil && first.ID <= r.ID {
			t.Error("closed user record lost original conversation order")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGrokUserHistoryPublishesAtomicallyWithOriginalTerminal(t *testing.T) {
	f, e := grokServerTerminalFixture(t, true)
	before, _ := store.Decode[domain.Session](grokUserSession(t, f))
	if _, err := f.service.Store.Get(context.Background(), domain.MessageKind, before.Execution.GrokUserMessageID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("acceptance or assistant response fabricated user history", err)
	}
	request := f.publish(t, e)
	record := grokUserSession(t, f)
	session, _ := store.Decode[domain.Session](record)
	assertGrokUserHistory(t, f.service.Store, f.input, session)
	user, _ := f.service.Store.Get(context.Background(), domain.MessageKind, session.Execution.GrokUserMessageID)
	if replay, err := f.call(request); err != nil || !replay.Msg.Replayed {
		t.Fatal("original closed history receipt lost", err)
	}
	after, _ := f.service.Store.Get(context.Background(), domain.MessageKind, session.Execution.GrokUserMessageID)
	if !reflect.DeepEqual(user, after) || !reflect.DeepEqual(record, grokUserSession(t, f)) {
		t.Fatal("receipt replay rewrote original history")
	}
}

func TestGrokUserHistoryRejectsForeignMissingAndReorderedRecords(t *testing.T) {
	for _, change := range []string{"missing", "body", "model", "source", "index", "timestamp", "foreign-event", "assistant-event", "terminal-event", "missing-reservation", "reserved-collision"} {
		t.Run(change, func(t *testing.T) {
			f, e := grokServerTerminalFixture(t, change != "missing-reservation")
			if change == "missing-reservation" {
				e.GrokTerminal.User = &domain.GrokUserHistory{Source: domain.GrokClosedFirstText, NativeEventID: string(f.thread) + "-2", TimestampMS: "0", PromptIndex: "0", Model: f.input.Configuration.NativeModel, InputDigest: domain.GrokUserInputDigest(f.input.Input.Prompt)}
			}
			u := e.GrokTerminal.User
			switch change {
			case "missing":
				e.GrokTerminal.User = nil
			case "body":
				u.InputDigest = domain.GrokUserInputDigest("changed")
			case "model":
				u.Model = "foreign"
			case "source":
				u.Source = "live"
			case "index":
				u.PromptIndex = "1"
			case "timestamp":
				u.TimestampMS = "01"
			case "foreign-event":
				u.NativeEventID = string(domain.NewID()) + "-2"
			case "assistant-event":
				u.NativeEventID = string(f.thread) + "-10"
			case "terminal-event":
				u.NativeEventID = e.GrokTerminal.NativeEventID
			case "reserved-collision":
				session, _ := store.Decode[domain.Session](grokUserSession(t, f))
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.occupy-reserved-message", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.MessageKind, session.Execution.GrokUserMessageID, 0, f.input.SessionID, "", domain.ExecutionMessage{Text: "Original unrelated fixture"})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before := grokUserSession(t, f)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid original user record accepted")
			}
			if !reflect.DeepEqual(before, grokUserSession(t, f)) {
				t.Fatal("invalid user record partially completed execution")
			}
		})
	}
}

func grokUserSession(t *testing.T, f *publicationFixture) store.Record {
	t.Helper()
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestGrokUserReservationCannotCrossHarnessOrEvent(t *testing.T) {
	for _, scenario := range []string{"foreign-harness", "wrong-event", "occupied-identity", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			var f *publicationFixture
			if scenario == "foreign-harness" {
				f = newPublicationFixture(t)
			} else {
				f = newGrokPublicationFixture(t, domain.ExecuteMode)
			}
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			e := f.event(domain.ExecutionInputAccepted, 2)
			e.GrokUserMessageID = domain.NewID()
			switch scenario {
			case "wrong-event":
				e.Kind = domain.ExecutionTurnFinished
				e.Outcome = domain.ExecutionSucceeded
			case "occupied-identity":
				e.GrokUserMessageID = f.input.InputID
			case "malformed":
				e.GrokUserMessageID = "unknown"
			}
			before := grokUserSession(t, f)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("foreign user reservation accepted")
			}
			if !reflect.DeepEqual(before, grokUserSession(t, f)) {
				t.Fatal("invalid reservation consumed queue accounting")
			}
		})
	}
}
