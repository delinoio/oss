// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
	"time"
)

func prActivityRows(t *testing.T, s *Store) []Record {
	t.Helper()
	var result []Record
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		rows, err := tx.List(Filter{Kind: domain.ProblemKind, Limit: 200})
		if err != nil {
			return err
		}
		for _, row := range rows {
			var v struct {
				Type domain.PRProblemRecordType `json:"type"`
			}
			if json.Unmarshal(row.Data, &v) == nil && (v.Type == domain.PRActivityRecord || v.Type == domain.PRHandlingVerificationRecord) {
				result = append(result, row)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

// These private fixtures model an older backup. No production writer publishes
// new retired snapshots or retroactively manufactures missing observations.
func seedLegacyPRActivity(t *testing.T, s *Store, f *remediationStoreFixture, attempt Record) {
	t.Helper()
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.legacy-activity", nil, func(tx *Tx) (any, error) {
		_, set, err := tx.GetPRProblemSet(f.set.ID)
		if err != nil {
			return nil, err
		}
		source, problem, err := tx.GetPRProblem(f.problems[0].ID)
		if err != nil {
			return nil, err
		}
		bound, err := Decode[domain.PRRemediationAttempt](attempt)
		if err != nil {
			return nil, err
		}
		actor := domain.PRProblemDismissal{RequestID: domain.NewID(), ActorType: domain.OwnerDevice, At: time.Now().UTC()}
		base := domain.PRActivity{Version: 1, Type: domain.PRActivityRecord, Action: domain.PRActivityObserved, SourceID: source.ID, SourceRevision: source.Revision, SetID: f.set.ID, RemoteRepositoryID: set.Target.RemoteRepositoryID, PullRequestID: set.Target.PullRequestID, Number: set.Target.Number, Owner: problem.Target.Owner, Name: problem.Target.Name, Problems: f.problems, Actor: actor}
		if base.Validate() != nil {
			t.Fatal("invalid legacy problem")
		}
		if _, err = tx.Put(domain.ProblemKind, domain.NewID(), 0, "", "", base); err != nil {
			return nil, err
		}
		base.Action, base.SourceID, base.SourceRevision, base.Mode, base.AttemptState = domain.PRActivityAttempt, attempt.ID, 1, bound.Mode, domain.PRRemediationReserved
		if base.Validate() != nil {
			t.Fatal("invalid legacy reservation")
		}
		if _, err = tx.Put(domain.ProblemKind, domain.NewID(), 0, "", "", base); err != nil {
			return nil, err
		}
		base.SourceRevision, base.AttemptState = attempt.Revision, domain.PRRemediationBound
		if base.Validate() != nil {
			t.Fatal("invalid legacy bound transition")
		}
		if _, err = tx.Put(domain.ProblemKind, domain.NewID(), 0, bound.SessionID, f.project, base); err != nil {
			return nil, err
		}
		verification := domain.PRHandlingVerification{Version: 1, Type: domain.PRHandlingVerificationRecord, SetID: f.set.ID, Problems: f.problems, ProofDigest: strings.Repeat("e", 64), Actor: actor}
		if verification.Validate() != nil {
			t.Fatal("invalid legacy verification")
		}
		_, err = tx.Put(domain.ProblemKind, domain.NewID(), 0, "", "", verification)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestActivityRetirementKeepsCanonicalDismissalReceiptsAndRollback(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	observation := problemObservationFixture()
	set := collectProblemFixture(t, s, 0, observation)
	observation.RepositoryID = domain.NewID()
	set = collectProblemFixture(t, s, set.Revision, observation)
	problems := readProblemFixture(t, s, set.ID)
	p, _ := Decode[domain.PRProblem](problems[0])
	request := domain.NewID()
	mutate := func(tx *Tx) (any, error) {
		return tx.DismissPRProblem(problems[0].ID, problems[0].Revision, p.ContentVersion)
	}
	first, err := s.Mutate(notificationOwner(), request, "fixture.retired-dismiss", nil, mutate)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Mutate(notificationOwner(), request, "fixture.retired-dismiss", nil, mutate)
	if err != nil || !replay.Replayed || string(first.Data) != string(replay.Data) {
		t.Fatal("canonical receipt changed", err)
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.retired-rollback", nil, func(tx *Tx) (any, error) {
		v, _ := Decode[domain.PRProblem](problems[1])
		if _, err := tx.DismissPRProblem(problems[1].ID, problems[1].Revision, v.ContentVersion); err != nil {
			return nil, err
		}
		return nil, errors.New("fixture rollback")
	})
	if err == nil {
		t.Fatal("rollback accepted")
	}
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		_, updated, err := tx.GetPRProblem(problems[0].ID)
		if err != nil {
			return err
		}
		if updated.State != domain.PRProblemDismissed || updated.Dismissal == nil || updated.Dismissal.RequestID != request {
			t.Fatal("canonical dismissal lost")
		}
		_, untouched, err := tx.GetPRProblem(problems[1].ID)
		if err != nil {
			return err
		}
		if untouched.State != domain.PRProblemUnhandled {
			t.Fatal("rollback changed original source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(prActivityRows(t, s)) != 0 {
		t.Fatal("retired snapshots published")
	}
}

func TestActivityRetirementKeepsCanonicalAttemptTransitions(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	f := newRemediationStoreFixture(t, s)
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	a = f.bind(t, a)
	request := domain.NewID()
	a, _, err = f.start(t, a, request, f.observation)
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := f.start(t, a, request, f.observation); err != nil || !replay.Replayed {
		t.Fatal("original attempt receipt changed", err)
	}
	a = f.finish(t, a, false)
	a = f.finish(t, a, false)
	a = f.finish(t, a, true)
	v, err := Decode[domain.PRRemediationAttempt](a)
	if err != nil || v.State != domain.PRRemediationFinished {
		t.Fatal("canonical attempt not finished", err)
	}
	if len(prActivityRows(t, s)) != 0 {
		t.Fatal("retired attempt snapshots published")
	}
}

func TestLegacyActivityDeletionKeepsSharedOriginalEvidence(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	f := newRemediationStoreFixture(t, s)
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	a = f.bind(t, a)
	seedLegacyPRActivity(t, s, f, a)
	bound, _ := Decode[domain.PRRemediationAttempt](a)
	if len(prActivityRows(t, s)) != 4 {
		t.Fatal("missing legacy fixtures")
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.delete-legacy", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.SessionKind, bound.SessionID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(r.Kind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := prActivityRows(t, s)
	if len(rows) != 2 {
		t.Fatal("dependent legacy snapshots survived or shared evidence erased", len(rows))
	}
	for _, row := range rows {
		if row.SessionID != "" {
			t.Fatal("bound legacy snapshot survived")
		}
	}
	if len(readProblemFixture(t, s, f.set.ID)) != 1 {
		t.Fatal("canonical shared problem lost")
	}
}
