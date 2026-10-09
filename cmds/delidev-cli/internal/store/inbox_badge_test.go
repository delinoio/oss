package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestUnreadInboxCountAllRetainedSources(t *testing.T) {
	s, _ := openTest(t)
	sources := []domain.InboxSource{domain.InteractionInbox, domain.ExecutionTerminalInbox, domain.SubscriptionRecoveryInbox, domain.OperationalInbox}
	_, err := s.Mutate(context.Background(), domain.NewID(), "test.unread-count", nil, func(tx *Tx) (any, error) {
		for i := 0; i < 260; i++ {
			state := domain.InboxUnread
			if i%5 == 0 {
				state = domain.InboxRead
			}
			_, err := tx.Put(domain.InboxKind, domain.NewID(), 0, "", "", map[string]any{"source": sources[i%4], "source_id": domain.NewID(), "read_state": state})
			if err != nil {
				return nil, err
			}
		}
		// No joined source is necessary for aggregate metadata. A non-Inbox record
		// that happens to contain the same read-state must never enter the count.
		_, err := tx.Put(domain.ProjectKind, domain.NewID(), 0, "", "", map[string]string{"name": "Unrelated", "read_state": "unread"})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		count, err := tx.UnreadInboxCount()
		if count != 208 {
			t.Fatalf("count=%d, want 208", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func TestUnreadInboxCountZero(t *testing.T) {
	s, _ := openTest(t)
	if err := s.Read(context.Background(), func(tx *Tx) error {
		count, err := tx.UnreadInboxCount()
		if count != 0 {
			t.Fatal(count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
