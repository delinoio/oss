package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func readExecutionInbox(t *testing.T, f *publicationFixture, source domain.InboxSource, id domain.ID) (store.Record, domain.InboxEntry) {
	t.Helper()
	var record store.Record
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		record, err = tx.InboxBySource(source, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.Decode[domain.InboxEntry](record)
	if err != nil || entry.Validate() != nil {
		t.Fatal("invalid retained inbox entry")
	}
	return record, entry
}

func markFixtureInbox(t *testing.T, f *publicationFixture, r store.Record, state domain.InboxReadState) store.Record {
	t.Helper()
	var updated store.Record
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.inbox.read", r.ID, func(tx *store.Tx) (any, error) {
		var err error
		updated, err = tx.SetInboxReadState(r.ID, r.Revision, state)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestExecutionInboxReadStateCannotInvalidateQuestionResponseControl(t *testing.T) {
	f, id := questionResponseFixture(t)
	inbox, entry := readExecutionInbox(t, f, domain.InteractionInbox, id)
	if inbox.ID == id || inbox.SessionID != f.input.SessionID || inbox.Revision != 1 || entry.ReadState != domain.InboxUnread || entry.Terminal != nil {
		t.Fatal("question did not retain an independent unread inbox identity")
	}
	req := questionRPCRequest(t, id, responseInput())
	if _, err := respondQuestionRPC(f, f.service.Identity, req); err != nil {
		t.Fatal(err)
	}
	inbox, entry = readExecutionInbox(t, f, domain.InteractionInbox, id)
	if inbox.Revision != 1 || entry.ReadState != domain.InboxUnread {
		t.Fatal("responding implicitly marked the inbox request read")
	}
	before, original := readPublishedInteraction(t, f, id)
	inbox = markFixtureInbox(t, f, inbox, domain.InboxRead)
	after, same := readPublishedInteraction(t, f, id)
	if after.Revision != before.Revision || !reflect.DeepEqual(original, same) {
		t.Fatal("mark-read changed question content or active response control revision")
	}
	claim := &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: before.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), JobId: string(f.job), ResponseId: req.Mutation.RequestId}
	if _, err := claimQuestion(f, claim); err != nil {
		t.Fatalf("mark-read invalidated a queued native response control: %v", err)
	}
	e := f.event(domain.ExecutionTurnFinished, 4)
	e.Outcome = domain.ExecutionStopped
	published := f.publish(t, e)
	current, entry := readExecutionInbox(t, f, domain.InteractionInbox, id)
	if current.ID != inbox.ID || current.Revision != inbox.Revision || entry.ReadState != domain.InboxRead {
		t.Fatal("closure/terminal publication reset read state or replaced the original inbox reference")
	}
	terminal, completed := readExecutionInbox(t, f, domain.ExecutionTerminalInbox, f.input.ExecutionID)
	if completed.ReadState != domain.InboxUnread || completed.Terminal.Outcome != domain.ExecutionStopped || completed.Terminal.Sequence != 4 || completed.Terminal.InputID != f.input.InputID || completed.Terminal.JobID != f.job {
		t.Fatal("terminal publication lost immutable completion evidence")
	}
	terminal = markFixtureInbox(t, f, terminal, domain.InboxRead)
	if result, err := f.call(published); err != nil || !result.Msg.Replayed {
		t.Fatalf("terminal receipt did not replay: %v", err)
	}
	replayed, completed := readExecutionInbox(t, f, domain.ExecutionTerminalInbox, f.input.ExecutionID)
	if replayed.ID != terminal.ID || replayed.Revision != terminal.Revision || completed.ReadState != domain.InboxRead {
		t.Fatal("publication replay duplicated completion or reset its read state")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("questions and completion did not remain together in the inbox")
	}
}

func TestExecutionInboxRetainsNativeOutcomeSeparatelyFromStoppedSession(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.stop", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(f.job) })
			if err != nil {
				t.Fatal(err)
			}
			e := f.event(domain.ExecutionTurnFinished, 3)
			e.Outcome = outcome
			f.publish(t, e)
			_, entry := readExecutionInbox(t, f, domain.ExecutionTerminalInbox, f.input.ExecutionID)
			if entry.Terminal.Outcome != outcome || entry.Terminal.NativeThreadID != string(f.thread) || entry.Terminal.NativeTurnID != string(f.turn) {
				t.Fatal("inbox changed the observed native outcome")
			}
			r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](r)
			if err != nil || session.Outcome != domain.ExecutionStopped || session.Execution.CleanupVerified {
				t.Fatal("inbox publication changed product Stop state or claimed process cleanup")
			}
		})
	}
}

func TestExecutionInboxCannotSurviveRolledBackPublication(t *testing.T) {
	for _, kind := range []domain.ExecutionEventKind{domain.ExecutionInteractionRequested, domain.ExecutionTurnFinished} {
		t.Run(string(kind), func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			e := f.event(domain.ExecutionTurnFinished, 3)
			e.Outcome = domain.ExecutionSucceeded
			if kind == domain.ExecutionInteractionRequested {
				e = f.interactionEvent(3, domain.NewID(), "rollback")
			}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.publication-rollback", kind, func(tx *store.Tx) (any, error) {
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
				if err := applyExecutionEvent(tx, job, f.input, f.device, sr, &session, ir, &q, e); err != nil {
					return nil, err
				}
				rows, err := tx.List(store.Filter{Kind: domain.InboxKind, SessionID: f.input.SessionID, Limit: 2})
				if err != nil || len(rows) != 1 {
					t.Error("publication did not create its inbox entry inside the transaction")
				}
				return nil, domain.Fail(domain.Conflict, "Fixture rollback.", "Retry the original publication.")
			})
			if domain.SafeError(err).Code != domain.Conflict {
				t.Fatal(err)
			}
			rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, Limit: 10})
			if err != nil || len(rows) != 0 {
				t.Fatal("failed publication left an orphan inbox entry")
			}
			f.publish(t, e)
		})
	}
}
