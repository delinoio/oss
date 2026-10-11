package store

import (
	"context"
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func putSessionInbox(t *testing.T, tx *Tx, session domain.ID, state domain.InboxReadState) Record {
	t.Helper()
	r, err := tx.Put(domain.InboxKind, domain.NewID(), 0, session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: domain.NewID(), ReadState: state})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMarkSessionInboxReadMarksOnlyTheSessionAcrossInternalPages(t *testing.T) {
	s, _ := openTest(t)
	a, b := domain.NewID(), domain.NewID()
	ctx := context.Background()
	if _, err := s.Mutate(ctx, domain.NewID(), "test.seed", nil, func(tx *Tx) (any, error) {
		for _, session := range []domain.ID{a, b} {
			if _, err := tx.Put(domain.SessionKind, session, 0, session, "", domain.Session{}); err != nil {
				return nil, err
			}
		}
		for i := 0; i < 201; i++ {
			putSessionInbox(t, tx, a, domain.InboxUnread)
		}
		putSessionInbox(t, tx, a, domain.InboxRead)
		for i := 0; i < 3; i++ {
			putSessionInbox(t, tx, b, domain.InboxUnread)
		}
		// Unscoped alerts never belong to a viewed chat.
		putSessionInbox(t, tx, "", domain.InboxUnread)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	var marked uint64
	if _, err := s.Mutate(ctx, domain.NewID(), "inbox.session-read", a, func(tx *Tx) (any, error) {
		count, _, err := tx.MarkSessionInboxRead(a, nil)
		marked = count
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	if marked != 201 {
		t.Fatalf("marked=%d, want 201", marked)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		count, err := tx.UnreadInboxCount()
		if count != 4 {
			t.Fatalf("remaining unread=%d, want 4", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMarkSessionInboxReadRollsBackOnVerifyFailureAndMissingSession(t *testing.T) {
	s, _ := openTest(t)
	a := domain.NewID()
	ctx := context.Background()
	if _, err := s.Mutate(ctx, domain.NewID(), "test.seed", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, a, 0, a, "", domain.Session{}); err != nil {
			return nil, err
		}
		for i := 0; i < 3; i++ {
			putSessionInbox(t, tx, a, domain.InboxUnread)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if _, err := s.Mutate(ctx, domain.NewID(), "inbox.session-read", a, func(tx *Tx) (any, error) {
		_, _, err := tx.MarkSessionInboxRead(a, func(Record) error {
			calls++
			if calls == 2 {
				return errors.New("injected source failure")
			}
			return nil
		})
		return nil, err
	}); err == nil {
		t.Fatal("verification failure committed")
	}
	missing := domain.NewID()
	if _, err := s.Mutate(ctx, domain.NewID(), "inbox.session-read", missing, func(tx *Tx) (any, error) {
		_, _, err := tx.MarkSessionInboxRead(missing, nil)
		return nil, err
	}); err == nil {
		t.Fatal("missing session accepted")
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		count, err := tx.UnreadInboxCount()
		if count != 3 {
			t.Fatalf("unread=%d after rollback, want 3", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
