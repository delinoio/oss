// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// This fixture supplies one original transaction observation without changing
// production clocks or exposing a Worker-provided timestamp. Clock changes are
// excluded from receipt identity exactly as they are in the real RPC.
func timingPublication(t *testing.T, f *publicationFixture, event domain.ExecutionEvent, observed time.Time, request domain.ID) store.Result {
	t.Helper()
	result, err := f.service.Store.Mutate(context.Background(), request, "fixture.original-timing-publication", event, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, f.input.InputID)
		if err != nil {
			return nil, err
		}
		q, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		job, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		if err := applyExecutionEventAt(tx, job, f.input, f.device, sr, &session, ir, &q, event, observed); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return executionEventReceipt{Sequence: event.Sequence}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func timingSession(t *testing.T, f *publicationFixture) domain.Session {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func timingMessage(t *testing.T, f *publicationFixture, id domain.ID) domain.ExecutionMessage {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTurnTimingOriginalAcceptanceTerminalAndReceiptReplay(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			if timingSession(t, f).Execution.TurnTiming != nil {
				t.Fatal("thread binding started a turn timer")
			}
			queued := time.Date(2026, 10, 9, 9, 59, 0, 0, time.UTC)
			accepted := queued.Add(time.Minute)
			terminal := accepted.Add(12 * time.Second)
			start := f.event(domain.ExecutionInputAccepted, 2)
			startReceipt := domain.NewID()
			timingPublication(t, f, start, accepted, startReceipt)
			original := timingSession(t, f).Execution.TurnTiming
			if original == nil || !original.AcceptedAt.Equal(accepted) || original.TerminalAt != nil {
				t.Fatal("acceptance used queue/startup timing")
			}
			if !timingPublication(t, f, start, accepted.Add(time.Hour), startReceipt).Replayed {
				t.Fatal("acceptance receipt did not replay")
			}
			user := domain.NewID()
			for i, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
				e := f.event(kind, uint64(3+i))
				e.Message = &domain.ExecutionMessageUpdate{ID: user, NativeID: "original-primary", Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
				f.publish(t, e)
			}
			before := timingMessage(t, f, user)
			if !reflect.DeepEqual(before.TurnTiming, original) {
				t.Fatal("late primary message lost original acceptance")
			}
			end := f.event(domain.ExecutionTurnFinished, 5)
			end.Outcome = outcome
			endReceipt := domain.NewID()
			timingPublication(t, f, end, terminal, endReceipt)
			s := timingSession(t, f)
			m := timingMessage(t, f, user)
			if s.Execution.TurnTiming == nil || s.Execution.TurnTiming.TerminalAt == nil || s.Execution.TurnTiming.TerminalAt.Sub(s.Execution.TurnTiming.AcceptedAt) != 12*time.Second || !reflect.DeepEqual(s.Execution.TurnTiming, m.TurnTiming) || s.Execution.CleanupVerified || s.Execution.Outcome != outcome {
				t.Fatal("terminal timing changed native outcome or cleanup")
			}
			before.TurnTiming = m.TurnTiming
			if !reflect.DeepEqual(before, m) {
				t.Fatal("timing changed native message contents/state/sequence")
			}
			if !timingPublication(t, f, end, terminal.Add(time.Hour), endReceipt).Replayed || !reflect.DeepEqual(timingSession(t, f).Execution.TurnTiming, s.Execution.TurnTiming) {
				t.Fatal("terminal receipt recaptured timing")
			}
		})
	}
}

func TestTurnTimingSteerNeverRestartsOrClaimsPrimaryTiming(t *testing.T) {
	f, request := newSteerFixture(t)
	original := *timingSession(t, f).Execution.TurnTiming
	accepted, err := callSteer(f, request)
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := claimSteer(t, f, accepted.Msg.Steer)
	event := f.event(domain.ExecutionSteerObserved, 3)
	event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(request.Mutation.RequestId), InputID: domain.ID(request.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeAccepted, Evidence: domain.SteerNativeHistory}
	f.publish(t, event)
	if !reflect.DeepEqual(&original, timingSession(t, f).Execution.TurnTiming) {
		t.Fatal("Steer reset the original timing")
	}
	r, err := f.service.Store.Get(context.Background(), domain.QueueKind, domain.ID(request.Mutation.Id))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := store.Decode[domain.QueuedInput](r)
	id := domain.NewID()
	event = f.event(domain.ExecutionMessageStarted, 4)
	event.Message = &domain.ExecutionMessageUpdate{ID: id, NativeID: "original-steer", Role: domain.UserMessage, InputID: r.ID, Text: q.Prompt}
	f.publish(t, event)
	if timingMessage(t, f, id).TurnTiming != nil {
		t.Fatal("Steer acquired another primary timer")
	}
}

func TestTurnTimingGrokLateUserAndProviderElapsedRemainIndependent(t *testing.T) {
	f, e := grokServerTerminalFixture(t, true)
	original := *timingSession(t, f).Execution.TurnTiming
	f.publish(t, e)
	s := timingSession(t, f)
	m := timingMessage(t, f, s.Execution.GrokUserMessageID)
	if !reflect.DeepEqual(m.TurnTiming, s.Execution.TurnTiming) || !m.TurnTiming.AcceptedAt.Equal(original.AcceptedAt) || m.TurnTiming.TerminalAt == nil || s.Execution.GrokTerminal.ElapsedMS != "3" {
		t.Fatal("Grok late user/provider observation replaced common timing")
	}
}

func TestTurnTimingCannotBeSuppliedByWorker(t *testing.T) {
	f := newPublicationFixture(t)
	event := f.event(domain.ExecutionInputAccepted, 2)
	raw, _ := json.Marshal(event)
	var d map[string]any
	json.Unmarshal(raw, &d)
	d["turn_timing"] = map[string]string{"accepted_at": "2026-10-09T10:00:00Z"}
	raw, _ = json.Marshal(d)
	var decoded domain.ExecutionEvent
	if domain.Decode(raw, &decoded) == nil {
		t.Fatal("Worker supplied authoritative timing")
	}
}

func TestTurnTimingTerminalRollsBackChangedOriginalMessageScope(t *testing.T) {
	for _, changed := range []string{"acceptance", "execution", "thread", "turn"} {
		t.Run(changed, func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			ids := []domain.ID{domain.NewID(), domain.NewID()}
			sequence := uint64(3)
			for _, id := range ids {
				for _, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
					e := f.event(kind, sequence)
					e.Message = &domain.ExecutionMessageUpdate{ID: id, NativeID: string(id), Role: domain.UserMessage, InputID: f.input.InputID, Text: f.input.Input.Prompt}
					f.publish(t, e)
					sequence++
				}
			}
			original := timingSession(t, f).Execution.TurnTiming
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.changed-timing-scope", changed, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.MessageKind, ids[1])
				if err != nil {
					return nil, err
				}
				m, err := store.Decode[domain.ExecutionMessage](r)
				if err != nil {
					return nil, err
				}
				switch changed {
				case "acceptance":
					m.TurnTiming.AcceptedAt = m.TurnTiming.AcceptedAt.Add(time.Second)
				case "execution":
					m.ExecutionID = domain.NewID()
				case "thread":
					m.NativeThreadID = "other-thread"
				case "turn":
					m.NativeTurnID = "other-turn"
				}
				return tx.Put(domain.MessageKind, r.ID, r.Revision, r.SessionID, r.ProjectID, m)
			})
			if err != nil {
				t.Fatal(err)
			}
			terminal := f.event(domain.ExecutionTurnFinished, sequence)
			terminal.Outcome = domain.ExecutionSucceeded
			if _, err := f.call(f.requestEvent(t, terminal)); err == nil {
				t.Fatal("changed timing ownership authorized terminal publication")
			}
			if !reflect.DeepEqual(timingSession(t, f).Execution.TurnTiming, original) || timingMessage(t, f, ids[0]).TurnTiming.TerminalAt != nil {
				t.Fatal("rolled-back publication leaked timing")
			}
		})
	}
}

func TestTurnTimingLegacyTerminalNeverBackfillsAcceptance(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.legacy-timing-absent", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		s.Execution.TurnTiming = nil
		return tx.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	end := f.event(domain.ExecutionTurnFinished, 3)
	end.Outcome = domain.ExecutionSucceeded
	f.publish(t, end)
	if timingSession(t, f).Execution.TurnTiming != nil {
		t.Fatal("legacy timing was backfilled")
	}
}

func TestTurnTimingInheritedForkCopyKeepsOriginalAttributionWithoutSharedMutableTiming(t *testing.T) {
	accepted := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	terminal := accepted.Add(12 * time.Second)
	source := domain.ExecutionMessage{ExecutionID: domain.NewID(), InputID: domain.NewID(), NativeThreadID: "source-thread", NativeTurnID: "source-turn", NativeID: "source-item", NativeParentID: "source-parent", Role: domain.UserMessage, Text: "Original immutable input", State: domain.MessageComplete, FirstSequence: 3, LastSequence: 4, TurnTiming: &domain.TurnTiming{AcceptedAt: accepted, TerminalAt: &terminal}}
	session, message, runtime := domain.NewID(), domain.NewID(), domain.NewID()
	child := inheritOpenCodeForkMessage(source, session, message, runtime, "child-thread", "child-turn", "child-item", "child-parent")
	if child.Inherited == nil || child.Inherited.SessionID != session || child.Inherited.MessageID != message || child.Inherited.ExecutionID != source.ExecutionID || child.Inherited.InputID != source.InputID || child.ExecutionID != runtime || child.InputID != "" || !reflect.DeepEqual(child.TurnTiming, source.TurnTiming) || child.Text != source.Text {
		t.Fatal("Fork changed source timing or granted child input authority")
	}
	*child.TurnTiming.TerminalAt = terminal.Add(time.Hour)
	if source.TurnTiming.TerminalAt.Sub(source.TurnTiming.AcceptedAt) != 12*time.Second {
		t.Fatal("child mutable timing replaced original source")
	}
}

func TestTurnTimingOpenCodePrimaryPartsShareOriginalAtomicTerminal(t *testing.T) {
	f := newOpenCodePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	accepted := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	timingPublication(t, f, f.event(domain.ExecutionInputAccepted, 2), accepted, domain.NewID())
	ids := []domain.ID{domain.NewID(), domain.NewID()}
	for index, id := range ids {
		message := domain.ExecutionMessageUpdate{ID: id, NativeID: []string{"prt_01960dcbe1fbabcdefghijklmn", "prt_01960dcbe1fcabcdefghijklmn"}[index], NativeParentID: string(f.turn), InputID: f.input.InputID, Role: domain.UserMessage, Text: f.input.Input.Prompt}
		for offset, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
			e := f.event(kind, uint64(3+index*2+offset))
			e.Message = &message
			f.publish(t, e)
		}
		if m := timingMessage(t, f, id); m.TurnTiming == nil || !m.TurnTiming.AcceptedAt.Equal(accepted) || m.TurnTiming.TerminalAt != nil {
			t.Fatal("OpenCode user part replaced the original accepted observation")
		}
	}
	usage := originalOpenCodeUsage()
	usage.Source, usage.NativeID = domain.OpenCodeMessageUsage, usage.NativeParentID
	e := f.event(domain.ExecutionOpenCodeUsageObserved, 7)
	e.ObservationID, e.OpenCodeUsage = domain.NewID(), &usage
	f.publish(t, e)
	end := f.event(domain.ExecutionTurnFinished, 8)
	end.Outcome = domain.ExecutionSucceeded
	timingPublication(t, f, end, accepted.Add(12*time.Second), domain.NewID())
	original := timingSession(t, f).Execution.TurnTiming
	for _, id := range ids {
		if m := timingMessage(t, f, id); !reflect.DeepEqual(m.TurnTiming, original) || m.TurnTiming.TerminalAt.Sub(m.TurnTiming.AcceptedAt) != 12*time.Second || m.InputID != f.input.InputID || m.NativeParentID != string(f.turn) {
			t.Fatal("OpenCode split primary history lost atomic timing or original ownership")
		}
	}
}
