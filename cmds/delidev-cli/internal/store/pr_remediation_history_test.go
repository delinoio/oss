package store

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPRRemediationHistoryOrdersOriginalAttemptsAndDetectsBindingChanges(t *testing.T) {
	s, _ := openTest(t)
	f := newRemediationStoreFixture(t, s)
	ids := []domain.ID{}
	for range 3 {
		a, err := f.reserve(t, domain.PRRemediationManual)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
		_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.cancel-attempt", nil, func(tx *Tx) (any, error) { return tx.CancelPRRemediation(a.ID, a.Revision) })
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		first, more, err := tx.ListPRRemediationAttempts(f.set.ID, "", 2)
		if err != nil || !more || len(first) != 2 || first[0].ID != ids[2] || first[1].ID != ids[1] {
			t.Fatal("original newest-first history", err)
		}
		last, more, err := tx.ListPRRemediationAttempts(f.set.ID, first[1].ID, 2)
		if err != nil || more || len(last) != 1 || last[0].ID != ids[0] {
			t.Fatal("original history continuation", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	active, err := f.reserve(t, domain.PRRemediationManual)
	if err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	var revision uint64
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		r, _, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return err
		}
		revision = r.Revision
		fingerprint, err = tx.PRRemediationHistoryFingerprint(r.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.bind(t, active)
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		r, _, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return err
		}
		current, err := tx.PRRemediationHistoryFingerprint(r.ID)
		if err != nil || current == fingerprint || r.Revision != revision {
			t.Fatal("binding did not independently invalidate its history fingerprint", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPRRemediationHistoryRejectsMissingOriginalSequence(t *testing.T) {
	s, _ := openTest(t)
	f := newRemediationStoreFixture(t, s)
	a, err := f.reserve(t, domain.PRRemediationManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE pr_remediation_attempts SET sequence=2 WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(notificationOwner(), func(tx *Tx) error { _, err := tx.PRRemediationHistoryFingerprint(f.set.ID); return err }); err == nil {
		t.Fatal("missing original sequence was silently paginated")
	}
}
