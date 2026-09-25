package store

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func activityPage(t *testing.T, s *Store, f ActivityFilter) ([]Record, bool, uint64, error) {
	t.Helper()
	if f.Limit == 0 {
		f.Limit = 50
	}
	var rows []Record
	var more bool
	var epoch uint64
	err := s.Read(context.Background(), func(tx *Tx) error { var err error; rows, more, epoch, err = tx.ActivityPage(f); return err })
	return rows, more, epoch, err
}

func TestActivityChronologicalSourcePagesDeletionAndRestart(t *testing.T) {
	s, root := openTest(t)
	first := seedSearch(t, s, "private first prompt", domain.Archived)
	second := seedSearch(t, s, "private second prompt", domain.NotArchived)
	var jobs []Record
	if err := s.Read(context.Background(), func(tx *Tx) error {
		var err error
		jobs, err = tx.List(Filter{Kind: domain.JobKind, Limit: 50})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatal("fixture needs two execution jobs")
	}
	// Clock ordering wins over UUID creation ordering, including exact ties.
	if _, err := s.db.Exec("UPDATE entities SET created_at=? WHERE id=?", int64(1000), jobs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE entities SET created_at=? WHERE id=?", int64(2000), jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	rows, more, epoch, err := activityPage(t, s, ActivityFilter{Limit: 1})
	if err != nil || !more || len(rows) != 1 || rows[0].ID != jobs[0].ID {
		t.Fatal("did not sort by source time", err)
	}
	next, more, _, err := activityPage(t, s, ActivityFilter{Limit: 1, After: rows[0].ID, Epoch: epoch})
	if err != nil || more || len(next) != 1 || next[0].ID != jobs[1].ID {
		t.Fatal("lost next chronological source", err)
	}
	for _, scope := range []ActivityFilter{{SessionID: first.session}, {ProjectID: second.project}} {
		page, _, _, err := activityPage(t, s, scope)
		if err != nil || len(page) != 1 {
			t.Fatal("activity scope mismatch", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	next, _, _, err = activityPage(t, s, ActivityFilter{Limit: 1, After: rows[0].ID, Epoch: epoch})
	if err != nil || len(next) != 1 {
		t.Fatal("restart lost position", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.remove-session", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, first.session, 1) })
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = activityPage(t, s, ActivityFilter{After: rows[0].ID, Epoch: epoch})
	assertCode(t, err, domain.CursorExpired)
	next, _, _, err = activityPage(t, s, ActivityFilter{SessionID: first.session})
	if err != nil || len(next) != 0 {
		t.Fatal("activity exposed a deleted session", err)
	}
	for _, bad := range []ActivityFilter{{SessionID: "bad"}, {ProjectID: "bad"}, {Limit: 201}, {Limit: -1}} {
		_, _, _, err := activityPage(t, s, bad)
		assertCode(t, err, domain.InvalidArgument)
	}
}
