package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
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

func downgradeInboxFixture(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(dropScheduleFixtureSchema + "DROP INDEX inbox_source; DROP INDEX inbox_read; DROP INDEX inbox_session_read; PRAGMA user_version=9"); err != nil {
		t.Fatal(err)
	}
}

func TestInboxMigrationBackfillsBoundedLegacySourcesAndPreservesBackup(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	session, execution, originals := legacyInboxSources(t, s, MaxPage+3)
	downgradeInboxFixture(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var entries []Record
	for after := domain.ID(""); ; {
		page, err := s.List(ctx, Filter{Kind: domain.InboxKind, SessionID: session, After: after, Limit: MaxPage})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, page...)
		if len(page) < MaxPage {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(entries) != len(originals) {
		t.Fatalf("backfilled %d inbox entries, want %d", len(entries), len(originals))
	}
	terminalCount := 0
	for _, r := range entries {
		entry, err := Decode[domain.InboxEntry](r)
		if err != nil || entry.Validate() != nil || entry.ReadState != domain.InboxUnread || r.Revision != 1 || r.CreatedAt.IsZero() {
			t.Fatal("migration fabricated read state or invalid metadata")
		}
		if entry.Source == domain.ExecutionTerminalInbox {
			terminalCount++
			if entry.SourceID != execution || entry.Terminal.Outcome != domain.ExecutionSucceeded || entry.Terminal.Sequence != MaxPage+6 {
				t.Fatal("migration replaced native completion with the stopped product outcome")
			}
		}
	}
	if terminalCount != 1 {
		t.Fatal("migration invented missing native execution history")
	}
	for _, original := range originals {
		current, err := s.Get(ctx, original.Kind, original.ID)
		if err != nil || current.Revision != original.Revision || string(current.Data) != string(original.Data) {
			t.Fatal("inbox migration rewrote original question/answer/session evidence")
		}
	}
	var version, interactions int
	if s.db.QueryRow("PRAGMA user_version").Scan(&version) != nil || version != SchemaVersion || s.db.QueryRow("SELECT COUNT(*) FROM execution_interactions").Scan(&interactions) != nil || interactions != MaxPage+3 {
		t.Fatal("migration lost native question indexes or schema version")
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("migration did not retain exactly one synchronized original backup")
	}
	backup, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var inboxCount int
	if backup.QueryRow("PRAGMA user_version").Scan(&version) != nil || version != 9 || backup.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='inbox'").Scan(&inboxCount) != nil || inboxCount != 0 {
		t.Fatal("pre-migration backup acquired generated inbox content")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, original := range entries {
		r, err := reopened.Get(ctx, domain.InboxKind, original.ID)
		if err != nil || r.Revision != original.Revision || string(r.Data) != string(original.Data) {
			t.Fatal("reopen replaced migrated inbox identity or content")
		}
	}
}

func TestInboxMigrationRejectsContradictorySourcesWithoutPartialBackfill(t *testing.T) {
	s, root := openTest(t)
	_, _, originals := legacyInboxSources(t, s, 2)
	bad := originals[2]
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.invalid-inbox-source", bad.ID, func(tx *Tx) (any, error) {
		value, err := Decode[domain.ExecutionInteraction](bad)
		if err != nil {
			return nil, err
		}
		value.Questions = nil
		return tx.Put(bad.Kind, bad.ID, bad.Revision, bad.SessionID, bad.ProjectID, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	downgradeInboxFixture(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(context.Background(), root); err == nil {
		reopened.Close()
		t.Fatal("malformed legacy source was silently discarded")
	} else if domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseURI(filepath.Join(root, "state.sqlite"), true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, inbox, indexes int
	if db.QueryRow("PRAGMA user_version").Scan(&version) != nil || version != 9 || db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind='inbox'").Scan(&inbox) != nil || inbox != 0 || db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='inbox_source'").Scan(&indexes) != nil || indexes != 0 {
		t.Fatal("failed migration committed partial inbox records, indexes or schema")
	}
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
