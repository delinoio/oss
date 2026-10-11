// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func sessionInboxFixture(t *testing.T, s *Store, count int) (domain.ID, []Record) {
	t.Helper()
	session, _, originals := legacyInboxSources(t, s, count)
	records := []Record{}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.session-inbox", session, func(tx *Tx) (any, error) {
		for _, source := range originals[1:] {
			record, err := tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source.ID, ReadState: domain.InboxUnread})
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, records
}
func validateSessionInboxFixture(tx *Tx, record Record) error {
	entry, err := Decode[domain.InboxEntry](record)
	if err != nil {
		return err
	}
	source, err := tx.Get(domain.InteractionKind, entry.SourceID)
	if err != nil {
		return err
	}
	if entry.Validate() != nil || source.SessionID != record.SessionID || source.ProjectID != record.ProjectID {
		return inboxConflict()
	}
	return nil
}
func TestSessionInboxReadPagesAndOriginalReplay(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, records := sessionInboxFixture(t, s, 202)
	other, otherRows := sessionInboxFixture(t, s, 1)
	_ = other
	var unscoped Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.unscoped", nil, func(tx *Tx) (any, error) {
		if _, err := tx.SetInboxReadState(records[201].ID, 1, domain.InboxRead); err != nil {
			return nil, err
		}
		var err error
		unscoped, err = tx.Put(domain.InboxKind, domain.NewID(), 0, "", "", domain.InboxEntry{Source: domain.SubscriptionRecoveryInbox, SourceID: domain.NewID(), ReadState: domain.InboxUnread, Recovery: &domain.InboxSubscriptionRecovery{AccountID: domain.NewID(), ConnectionID: domain.NewID(), ObservedAt: time.Now().UTC()}})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, domain.SessionKind, session)
	request := domain.NewID()
	identity := struct{ SessionID domain.ID }{session}
	result, err := s.Mutate(ctx, request, "inbox.session-read", identity, func(tx *Tx) (any, error) { return tx.MarkSessionInboxRead(session, validateSessionInboxFixture) })
	if err != nil {
		t.Fatal(err)
	}
	var receipt SessionInboxReadReceipt
	if json.Unmarshal(result.Data, &receipt) != nil || receipt.MarkedCount != 201 || receipt.SessionID != session || receipt.ObservedAt.IsZero() {
		t.Fatal("lost original compact receipt")
	}
	for _, row := range records {
		current, err := s.Get(ctx, domain.InboxKind, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		entry, _ := Decode[domain.InboxEntry](current)
		if current.Revision != 2 || entry.ReadState != domain.InboxRead {
			t.Fatal("read did not retain exactly one independent revision")
		}
		var events int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM events WHERE entity_id=? AND revision=2", row.ID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events != 1 {
			t.Fatal("read revision event repeated")
		}
	}
	for _, row := range []Record{otherRows[0], unscoped} {
		current, _ := s.Get(ctx, domain.InboxKind, row.ID)
		entry, _ := Decode[domain.InboxEntry](current)
		if current.Revision != 1 || entry.ReadState != domain.InboxUnread {
			t.Fatal("another scope was acknowledged")
		}
	}
	after, _ := s.Get(ctx, domain.SessionKind, session)
	if before.Revision != after.Revision || string(before.Data) != string(after.Data) {
		t.Fatal("session execution state changed")
	}
	var later Record
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.later-alert", nil, func(tx *Tx) (any, error) {
		if _, err := tx.SetInboxReadState(records[0].ID, 2, domain.InboxUnread); err != nil {
			return nil, err
		}
		entry, err := Decode[domain.InboxEntry](records[0])
		if err != nil {
			return nil, err
		}
		original, err := tx.Get(domain.InteractionKind, entry.SourceID)
		if err != nil {
			return nil, err
		}
		value, err := Decode[domain.ExecutionInteraction](original)
		if err != nil {
			return nil, err
		}
		source, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, session, "", value)
		if err != nil {
			return nil, err
		}
		later, err = tx.CreateInboxEntry(session, "", domain.InboxEntry{Source: domain.InteractionInbox, SourceID: source.ID, ReadState: domain.InboxUnread})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Mutate(ctx, request, "inbox.session-read", identity, func(*Tx) (any, error) { t.Fatal("replay rescanned the session"); return nil, nil })
	if err != nil || !replay.Replayed || string(replay.Data) != string(result.Data) {
		t.Fatal("original count/time receipt changed")
	}
	for _, id := range []domain.ID{records[0].ID, later.ID} {
		r, _ := s.Get(ctx, domain.InboxKind, id)
		v, _ := Decode[domain.InboxEntry](r)
		if v.ReadState != domain.InboxUnread {
			t.Fatal("replay marked a later unread entry")
		}
	}
	_, err = s.Mutate(ctx, request, "inbox.session-read", struct{ SessionID domain.ID }{other}, func(*Tx) (any, error) { t.Fatal("changed request admitted"); return nil, nil })
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("session substitution was accepted")
	}
}
func TestSessionInboxReadRollsBackEveryInternalPage(t *testing.T) {
	for _, failure := range []string{"source", "cancel", "storage"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := openTest(t)
			session, records := sessionInboxFixture(t, s, 201)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "storage" {
				_, err := s.db.Exec(fmt.Sprintf("CREATE TRIGGER fail_session_read BEFORE UPDATE ON entities WHEN NEW.id='%s' AND NEW.revision=2 BEGIN SELECT RAISE(ABORT,'fixture-write-failure'); END", records[200].ID))
				if err != nil {
					t.Fatal(err)
				}
			}
			request := domain.NewID()
			checked := 0
			_, err := s.Mutate(ctx, request, "inbox.session-read", session, func(tx *Tx) (any, error) {
				return tx.MarkSessionInboxRead(session, func(tx *Tx, row Record) error {
					checked++
					if checked == 201 {
						if failure == "source" {
							return inboxConflict()
						}
						if failure == "cancel" {
							cancel()
						}
					}
					return validateSessionInboxFixture(tx, row)
				})
			})
			if err == nil {
				t.Fatal("partial operation accepted")
			}
			for _, row := range records {
				r, err := s.Get(context.Background(), domain.InboxKind, row.ID)
				if err != nil || r.Revision != 1 {
					t.Fatal("earlier page was committed")
				}
			}
			var receipts int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM receipts WHERE id=?", request).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatal("failed mutation retained receipt")
			}
		})
	}
}
