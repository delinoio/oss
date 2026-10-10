package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func sessionReadSources(t *testing.T, s *Store, count int) (domain.ID, []Record) {
	t.Helper()
	session, _, sources := legacyInboxSources(t, s, count)
	records := []Record{}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.session-inbox", session, func(tx *Tx) (any, error) {
		for _, source := range sources[1:] {
			r, err := tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source.ID, ReadState: domain.InboxUnread})
			if err != nil {
				return nil, err
			}
			records = append(records, r)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, records
}

func TestSessionInboxReadInternalPagesAndImmutableReplay(t *testing.T) {
	s, _ := openTest(t)
	session, records := sessionReadSources(t, s, 202)
	other, _ := sessionReadSources(t, s, 2)
	var unscoped Record
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.unscoped", nil, func(tx *Tx) (any, error) {
		value := domain.InboxEntry{Source: domain.SubscriptionRecoveryInbox, SourceID: domain.NewID(), ReadState: domain.InboxUnread, Recovery: &domain.InboxSubscriptionRecovery{AccountID: domain.NewID(), ConnectionID: domain.NewID(), ObservedAt: time.Now().UTC()}}
		var err error
		unscoped, err = tx.Put(domain.InboxKind, domain.NewID(), 0, "", "", value)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.existing-read", nil, func(tx *Tx) (any, error) { return tx.SetInboxReadState(records[0].ID, 1, domain.InboxRead) })
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	type receipt struct {
		Count uint64
		Time  string
	}
	apply := func(tx *Tx) (any, error) {
		n, err := tx.MarkSessionInboxRead(session, func(r Record) error {
			entry, err := Decode[domain.InboxEntry](r)
			if err != nil || entry.Validate() != nil {
				return inboxConflict()
			}
			source, err := tx.Get(domain.InteractionKind, entry.SourceID)
			if err != nil {
				return err
			}
			if source.SessionID != session {
				return inboxConflict()
			}
			return nil
		})
		return receipt{n, tx.ObservationTime().String()}, err
	}
	result, err := s.Mutate(context.Background(), request, "inbox.session-read", session, apply)
	if err != nil {
		t.Fatal(err)
	}
	var original receipt
	if domain.Decode(result.Data, &original) != nil || original.Count != 201 {
		t.Fatalf("all internal pages not acknowledged: %s", result.Data)
	}
	err = s.Read(context.Background(), func(tx *Tx) error {
		for _, before := range records {
			after, err := tx.Get(domain.InboxKind, before.ID)
			if err != nil {
				return err
			}
			value, _ := Decode[domain.InboxEntry](after)
			if after.Revision != 2 || value.ReadState != domain.InboxRead {
				return fmt.Errorf("entry revision/read state changed incorrectly")
			}
			var events int
			if err := tx.tx.QueryRowContext(tx.ctx, "SELECT COUNT(*) FROM events WHERE kind='inbox' AND entity_id=?", before.ID).Scan(&events); err != nil {
				return err
			}
			if events != 2 {
				return fmt.Errorf("entry did not get exactly one read event: %d", events)
			}
		}
		operational, err := tx.Get(domain.InboxKind, unscoped.ID)
		if err != nil {
			return err
		}
		if operational.Revision != 1 {
			return fmt.Errorf("unscoped alert changed")
		}
		rows, _, _, err := tx.InboxPage(InboxFilter{SessionID: other, ReadState: domain.InboxUnread, Limit: MaxPage})
		if err != nil {
			return err
		}
		if len(rows) != 2 {
			return fmt.Errorf("other session changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.later-unread", nil, func(tx *Tx) (any, error) { return tx.SetInboxReadState(records[1].ID, 2, domain.InboxUnread) })
	if err != nil {
		t.Fatal(err)
	}
	// A later source belongs to A but never belongs to the original receipt.
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.later-alert", nil, func(tx *Tx) (any, error) {
		original, err := tx.Get(domain.InboxKind, records[1].ID)
		if err != nil {
			return nil, err
		}
		entry, err := Decode[domain.InboxEntry](original)
		if err != nil {
			return nil, err
		}
		source, err := tx.Get(domain.InteractionKind, entry.SourceID)
		if err != nil {
			return nil, err
		}
		question, err := Decode[domain.ExecutionInteraction](source)
		if err != nil {
			return nil, err
		}
		sourceID := domain.NewID()
		question.NativeItemID = "later-item"
		question.NativeRequestID.Text = "later-request"
		if _, err := tx.Put(domain.InteractionKind, sourceID, 0, session, "", question); err != nil {
			return nil, err
		}
		return tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: sourceID, ReadState: domain.InboxUnread})
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Mutate(context.Background(), request, "inbox.session-read", session, func(*Tx) (any, error) { t.Fatal("receipt rescanned later alerts"); return nil, nil })
	if err != nil || !replay.Replayed || string(replay.Data) != string(result.Data) {
		t.Fatal("original count/time receipt changed", err)
	}
	err = s.Read(context.Background(), func(tx *Tx) error {
		rows, _, _, err := tx.InboxPage(InboxFilter{SessionID: session, ReadState: domain.InboxUnread, Limit: MaxPage})
		if err == nil && len(rows) != 2 {
			return fmt.Errorf("later alert/unread was overwritten")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionInboxReadLaterPageFailureRollsBackMarksAndReceipt(t *testing.T) {
	for _, failure := range []string{"source", "cancellation", "storage"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := openTest(t)
			session, records := sessionReadSources(t, s, 201)
			request := domain.NewID()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			seen := 0
			_, err := s.Mutate(ctx, request, "inbox.session-read", session, func(tx *Tx) (any, error) {
				return tx.MarkSessionInboxRead(session, func(Record) error {
					seen++
					if seen != 201 {
						return nil
					}
					switch failure {
					case "source":
						return inboxConflict()
					case "cancellation":
						cancel()
						return nil
					default:
						_, err := tx.tx.ExecContext(tx.ctx, "UPDATE missing_inbox_fixture_table SET missing=1")
						return err
					}
				})
			})
			if err == nil || seen != 201 {
				t.Fatal("later page failure was not injected", err, seen)
			}
			err = s.Read(context.Background(), func(tx *Tx) error {
				for _, original := range records {
					current, err := tx.Get(domain.InboxKind, original.ID)
					if err != nil {
						return err
					}
					if current.Revision != 1 {
						return fmt.Errorf("partial mark committed")
					}
				}
				var n int
				if err := tx.tx.QueryRowContext(tx.ctx, "SELECT COUNT(*) FROM receipts WHERE id=?", request).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					return fmt.Errorf("failed transaction retained receipt")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionInboxReadAuthoritativeAggregateThreeOneZero(t *testing.T) {
	s, _ := openTest(t)
	a, _ := sessionReadSources(t, s, 2)
	b, _ := sessionReadSources(t, s, 1)
	count := func(want uint64) {
		t.Helper()
		err := s.Read(context.Background(), func(tx *Tx) error {
			got, err := tx.UnreadInboxCount()
			if err == nil && got != want {
				return fmt.Errorf("count %d, want %d", got, want)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mark := func(session domain.ID, want uint64) {
		t.Helper()
		_, err := s.Mutate(context.Background(), domain.NewID(), "inbox.session-read", session, func(tx *Tx) (any, error) {
			got, err := tx.MarkSessionInboxRead(session, func(Record) error { return nil })
			if err == nil && got != want {
				return nil, fmt.Errorf("marked %d, want %d", got, want)
			}
			return got, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	count(3)
	mark(a, 2)
	count(1)
	mark(b, 1)
	count(0)
}
