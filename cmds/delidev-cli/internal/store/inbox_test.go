package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func legacyInboxSources(t *testing.T, s *Store, count int) (domain.ID, domain.ID, []Record) {
	t.Helper()
	session, execution, input, job := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	var originals []Record
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.inbox-sources", count, func(tx *Tx) (any, error) {
		progress := &domain.ExecutionProgress{JobID: job, ExecutionID: execution, InputID: input, NativeThreadID: "thread", NativeTurnID: "turn", LastSequence: uint64(count + 3), Outcome: domain.ExecutionSucceeded}
		sr, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{Outcome: domain.ExecutionStopped, Execution: progress})
		if err != nil {
			return nil, err
		}
		originals = append(originals, sr)
		raw, _ := json.Marshal(domain.ExecutionJobInput{Version: 1, SessionID: session, ExecutionID: execution, InputID: input})
		if _, err := tx.PutJob(job, 0, session, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: domain.NewID(), Input: raw}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.QueueKind, input, 0, session, "", domain.QueuedInput{Sequence: 1, ExecutionID: execution, Delivery: domain.InputAccepted, Prompt: "Original prompt"}); err != nil {
			return nil, err
		}
		for i := range count {
			id := domain.NewID()
			value := domain.ExecutionInteraction{ExecutionID: execution, NativeThreadID: "thread", NativeTurnID: "turn", NativeItemID: fmt.Sprintf("item-%d", i), NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: fmt.Sprintf("request-%d", i)}, Type: domain.UserQuestionInteraction, Questions: &domain.QuestionRequest{Blocking: true, Questions: []domain.Question{{ID: "choice", Text: "Original question", Other: true}}}, Closure: domain.InteractionNativeClosed, FirstSequence: uint64(i + 3), LastSequence: uint64(i + 3)}
			if i == 0 {
				value.Response = &domain.QuestionResponse{ID: domain.NewID(), State: domain.QuestionResponseCanceled, Input: domain.QuestionResponseInput{Answers: map[string][]string{"choice": {"Retained answer"}}}}
			}
			r, err := tx.Put(domain.InteractionKind, id, 0, session, "", value)
			if err != nil {
				return nil, err
			}
			originals = append(originals, r)
			question, _ := json.Marshal(value.Questions)
			if err := tx.BindExecutionInteraction(session, execution, id, "thread", value.NativeRequestID, len(question)); err != nil {
				return nil, err
			}
			if err := tx.CloseExecutionInteraction(session, execution, id, "thread", value.NativeRequestID, domain.InteractionNativeClosed); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, execution, originals
}

func TestInboxSourceUniquenessAndIndependentReadRevisions(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, _, originals := legacyInboxSources(t, s, 1)
	question := originals[1]
	entry := domain.InboxEntry{Source: domain.InteractionInbox, SourceID: question.ID, ReadState: domain.InboxUnread}
	var inbox Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.inbox-create", question.ID, func(tx *Tx) (any, error) {
		var err error
		inbox, err = tx.CreateInboxEntry(session, "", entry)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.inbox-duplicate", question.ID, func(tx *Tx) (any, error) { return tx.CreateInboxEntry(session, "", entry) })
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("a different receipt duplicated source ownership: %v", err)
	}
	mark := func(revision uint64, state domain.InboxReadState) (Record, error) {
		var r Record
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.inbox-mark", state, func(tx *Tx) (any, error) {
			var err error
			r, err = tx.SetInboxReadState(inbox.ID, revision, state)
			return nil, err
		})
		return r, err
	}
	read, err := mark(1, domain.InboxRead)
	if err != nil || read.Revision != 2 {
		t.Fatalf("mark read failed: %v", err)
	}
	if same, err := mark(2, domain.InboxRead); err != nil || same.Revision != 2 {
		t.Fatal("idempotent read state advanced its revision")
	}
	if _, err := mark(1, domain.InboxUnread); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale read state replaced a current client observation")
	}
	unread, err := mark(2, domain.InboxUnread)
	if err != nil || unread.Revision != 3 {
		t.Fatalf("mark unread failed: %v", err)
	}
	current, err := s.Get(ctx, question.Kind, question.ID)
	if err != nil || current.Revision != question.Revision || !reflect.DeepEqual(current.Data, question.Data) {
		t.Fatal("read-state mutations changed original native/response evidence")
	}
	value, err := Decode[domain.InboxEntry](unread)
	if err != nil || !reflect.DeepEqual(value, entry) {
		t.Fatal("read-state mutation changed source metadata")
	}
}
