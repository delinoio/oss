// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSessionInboxReadPagesReceiptAndAtomicRollback(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, _, originals := legacyInboxSources(t, s, 203)
	var entries []Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.session-inbox", session, func(tx *Tx) (any, error) {
		for _, source := range originals[1:203] {
			r, err := tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source.ID, ReadState: domain.InboxUnread})
			if err != nil {
				return nil, err
			}
			entries = append(entries, r)
		}
		_, err := tx.SetInboxReadState(entries[201].ID, 1, domain.InboxRead)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	visited := 0
	_, err = s.Mutate(ctx, id, "inbox.session-read", session, func(tx *Tx) (any, error) {
		return tx.MarkSessionInboxRead(session, func(r Record) error {
			visited++
			if visited == 201 {
				return domain.Fail(domain.RecoveryRequired, "Broken retained source.", "Preserve it.")
			}
			return nil
		})
	})
	if domain.SafeError(err).Code != domain.RecoveryRequired || visited != 201 {
		t.Fatalf("second-page rollback: %d %v", visited, err)
	}
	check := func(count uint64) {
		t.Helper()
		if err := s.Read(ctx, func(tx *Tx) error {
			got, err := tx.UnreadInboxCount()
			if err != nil {
				return err
			}
			if got != count {
				t.Fatalf("unread=%d want=%d", got, count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(201)
	var marked uint64
	first, err := s.Mutate(ctx, id, "inbox.session-read", session, func(tx *Tx) (any, error) {
		marked, err = tx.MarkSessionInboxRead(session, func(r Record) error {
			v, err := Decode[domain.InboxEntry](r)
			if err != nil {
				return err
			}
			_, err = tx.Get(domain.InteractionKind, v.SourceID)
			return err
		})
		return marked, err
	})
	if err != nil || marked != 201 || first.Replayed {
		t.Fatalf("paged acknowledgment: %d %v", marked, err)
	}
	check(0)
	var later Record
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.later-inbox", session, func(tx *Tx) (any, error) {
		var err error
		later, err = tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: originals[203].ID, ReadState: domain.InboxUnread})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.restore-unread", session, func(tx *Tx) (any, error) { return tx.SetInboxReadState(entries[0].ID, 2, domain.InboxUnread) })
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Mutate(ctx, id, "inbox.session-read", session, func(tx *Tx) (any, error) { t.Fatal("receipt replay reselected entries"); return nil, nil })
	if err != nil || !replay.Replayed || string(replay.Data) != string(first.Data) {
		t.Fatal("original receipt changed", err)
	}
	check(2)
	if err := s.Read(ctx, func(tx *Tx) error {
		late, err := tx.Get(domain.InboxKind, later.ID)
		if err != nil {
			return err
		}
		if late.Revision != 1 {
			t.Fatal("later alert changed on replay")
		}
		for i, r := range entries {
			got, err := tx.Get(domain.InboxKind, r.ID)
			if err != nil {
				return err
			}
			want := uint64(2)
			if i == 0 {
				want = 3
			}
			if got.Revision != want {
				t.Fatalf("entry %d revision=%d want=%d", i, got.Revision, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionInboxReadCancellationAndStorageFailureRollback(t *testing.T) {
	for _, failure := range []string{"canceled", "storage"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := openTest(t)
			session, _, originals := legacyInboxSources(t, s, 201)
			ctx := context.Background()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.session-inbox", session, func(tx *Tx) (any, error) {
				for _, r := range originals[1:] {
					if _, err := tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: r.ID, ReadState: domain.InboxUnread}); err != nil {
						return nil, err
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			visited := 0
			request := domain.NewID()
			_, err = s.Mutate(canceled, request, "inbox.session-read", session, func(tx *Tx) (any, error) {
				return tx.MarkSessionInboxRead(session, func(record Record) error {
					visited++
					if visited == 201 {
						if failure == "canceled" {
							cancel()
						} else {
							_, err := tx.tx.ExecContext(tx.ctx, "CREATE TEMP TRIGGER session_read_failure BEFORE UPDATE ON entities WHEN NEW.kind='inbox' BEGIN SELECT RAISE(ABORT,'fixture failure'); END")
							return err
						}
					}
					return nil
				})
			})
			if err == nil || visited != 201 {
				t.Fatal("second-page failure did not reject", err)
			}
			if err := s.Read(ctx, func(tx *Tx) error {
				got, err := tx.UnreadInboxCount()
				if err != nil {
					return err
				}
				if got != 201 {
					t.Fatalf("partial count %d", got)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			result, err := s.Mutate(ctx, request, "inbox.session-read", session, func(tx *Tx) (any, error) { return tx.MarkSessionInboxRead(session, func(Record) error { return nil }) })
			if err != nil || result.Replayed {
				t.Fatal("failed operation retained a receipt", err)
			}
		})
	}
}

func TestSessionInboxReadChangesOnlyOriginalSessionAggregate(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a, _, sourcesA := legacyInboxSources(t, s, 2)
	b, _, sourcesB := legacyInboxSources(t, s, 1)
	var scoped []Record
	var unscoped []Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.scoped-inbox", a, func(tx *Tx) (any, error) {
		for _, source := range append(sourcesA[1:], sourcesB[1:]...) {
			r, err := tx.CreateInboxEntry(source.SessionID, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source.ID, ReadState: domain.InboxUnread})
			if err != nil {
				return nil, err
			}
			scoped = append(scoped, r)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mark := func(session domain.ID, want uint64) {
		t.Helper()
		_, err := s.Mutate(ctx, domain.NewID(), "inbox.session-read", session, func(tx *Tx) (any, error) {
			count, err := tx.MarkSessionInboxRead(session, func(Record) error { return nil })
			if count != want {
				t.Fatalf("marked=%d want=%d", count, want)
			}
			return count, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func(want uint64) {
		t.Helper()
		if err := s.Read(ctx, func(tx *Tx) error {
			got, err := tx.UnreadInboxCount()
			if got != want {
				t.Fatalf("count=%d want=%d", got, want)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	count(3)
	mark(a, 2)
	count(1)
	// Unscoped aggregate fixtures deliberately contain no joined source payload;
	// session acknowledgment must never select or inspect these other owners.
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.unscoped-counts", nil, func(tx *Tx) (any, error) {
		for _, kind := range []domain.InboxSource{domain.OperationalInbox, domain.SubscriptionRecoveryInbox} {
			r, err := tx.Put(domain.InboxKind, domain.NewID(), 0, "", "", map[string]any{"source": kind, "source_id": domain.NewID(), "read_state": "unread"})
			if err != nil {
				return nil, err
			}
			unscoped = append(unscoped, r)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mark(a, 0)
	count(3)
	mark(b, 1)
	count(2)
	if err := s.Read(ctx, func(tx *Tx) error {
		for _, original := range unscoped {
			r, err := tx.Get(domain.InboxKind, original.ID)
			if err != nil {
				return err
			}
			if r.Revision != 1 {
				t.Fatal("unscoped entry changed")
			}
		}
		for _, original := range sourcesA {
			r, err := tx.Get(original.Kind, original.ID)
			if err != nil {
				return err
			}
			if r.Revision != original.Revision {
				t.Fatal("source/session revision changed")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
