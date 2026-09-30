package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prActivityRows(t *testing.T, s *Store) []Record {
	t.Helper()
	rows, _, _, err := activityPage(t, s, ActivityFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(rows, func(r Record) bool { return r.Kind != domain.ProblemKind })
}

func TestPRActivityOriginalVersionsDismissalAliasesRollbackAndRestart(t *testing.T) {
	s, root := openTest(t)
	observation := problemObservationFixture()
	observation.Feedback.Entries[0].Body = "activity-private-feedback-sentinel"
	observation.Feedback.Entries[0].ContentVersion = observation.Feedback.Entries[0].Version()
	set := collectProblemFixture(t, s, 0, observation)
	before := prActivityRows(t, s)
	if len(before) != 2 {
		t.Fatal("missing original problem activity")
	}
	// A different local alias for the same remote identity has one history.
	observation.RepositoryID = domain.NewID()
	set = collectProblemFixture(t, s, set.Revision, observation)
	if got := prActivityRows(t, s); len(got) != len(before) {
		t.Fatal("alias duplicated original activity")
	}
	problems := readProblemFixture(t, s, set.ID)
	p, _ := Decode[domain.PRProblem](problems[0])
	request := domain.NewID()
	mutate := func(tx *Tx) (any, error) {
		return tx.DismissPRProblem(problems[0].ID, problems[0].Revision, p.ContentVersion)
	}
	if _, err := s.Mutate(notificationOwner(), request, "fixture.activity-dismiss", nil, mutate); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.Mutate(notificationOwner(), request, "fixture.activity-dismiss", nil, mutate); err != nil || !replay.Replayed {
		t.Fatal("dismiss receipt replay", err)
	}
	rows := prActivityRows(t, s)
	if len(rows) != 3 {
		t.Fatal("dismiss retry duplicated activity")
	}
	dismissal, err := Decode[domain.PRActivity](rows[0])
	if err != nil || dismissal.Action != domain.PRActivityDismissed || dismissal.SourceID != problems[0].ID || dismissal.SourceRevision != problems[0].Revision+1 || dismissal.Actor.RequestID != request || dismissal.Problems[0].ContentVersion != p.ContentVersion {
		t.Fatal("lost original dismissal provenance", err)
	}
	for _, row := range rows {
		if strings.Contains(string(row.Data), "activity-private-feedback-sentinel") || strings.Contains(string(row.Data), "Original title") {
			t.Fatal("content entered activity")
		}
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.activity-rollback", nil, func(tx *Tx) (any, error) {
		if _, err := tx.DismissPRProblem(problems[1].ID, problems[1].Revision, func() string { v, _ := Decode[domain.PRProblem](problems[1]); return v.ContentVersion }()); err != nil {
			return nil, err
		}
		return nil, errors.New("rollback fixture")
	})
	if err == nil || len(prActivityRows(t, s)) != 3 {
		t.Fatal("activity escaped source rollback")
	}
	// Force equal timestamps to verify strict UUID tie breaking across every page.
	if _, err := s.db.Exec(`UPDATE entities SET created_at=1000 WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-activity'`); err != nil {
		t.Fatal(err)
	}
	seen := map[domain.ID]bool{}
	f := ActivityFilter{Limit: 1}
	for {
		page, more, epoch, err := activityPage(t, s, f)
		if err != nil || len(page) != 1 || seen[page[0].ID] {
			t.Fatal("equal-time pagination lost or duplicated a source", err)
		}
		seen[page[0].ID] = true
		if !more {
			break
		}
		f.After, f.Epoch = page[0].ID, epoch
	}
	if len(seen) != 3 {
		t.Fatal("equal-time pagination omitted activity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(prActivityRows(t, s)) != 3 {
		t.Fatal("restart lost retained activity")
	}
}

func TestPRActivityAttemptTransitionsDoNotInferHandlingAndSessionDeletion(t *testing.T) {
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
		t.Fatal(err)
	}
	a = f.finish(t, a, false)
	a = f.finish(t, a, false)
	// A repeated uncertain observation does not create another transition.
	if len(prActivityRows(t, s)) != 5 {
		t.Fatal("duplicate uncertainty or attempt receipt")
	}
	a = f.finish(t, a, true)
	finished, _ := Decode[domain.PRRemediationAttempt](a)
	states := map[domain.PRRemediationAttemptState]int{}
	for _, row := range prActivityRows(t, s) {
		v, err := Decode[domain.PRActivity](row)
		if err != nil || v.Validate() != nil {
			t.Fatal("invalid retained activity", err)
		}
		if v.Action == domain.PRActivityAttempt {
			states[v.AttemptState]++
			if v.SourceID != a.ID || v.Problems[0] != f.problems[0] {
				t.Fatal("lost original attempt/version reference")
			}
		}
	}
	for _, state := range []domain.PRRemediationAttemptState{domain.PRRemediationReserved, domain.PRRemediationBound, domain.PRRemediationRunning, domain.PRRemediationUncertain, domain.PRRemediationFinished} {
		if states[state] != 1 {
			t.Fatal("missing distinct attempt transition", state, states)
		}
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.delete-activity-session", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.SessionKind, finished.SessionID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(r.Kind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := prActivityRows(t, s); len(rows) != 1 {
		t.Fatal("session deletion retained owned activity or erased shared PR evidence", len(rows))
	}
	var retained int
	if err := s.db.QueryRow(`SELECT count(*) FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-activity' AND json_extract(body,'$.source_id')=?`, a.ID).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("hidden session activity was not removed", retained, err)
	}
	// No native or provider success can silently turn the original problem into
	// handled evidence; this boundary has only observed, dismissed and attempts.
	r, err := s.Get(context.Background(), domain.ProblemKind, f.problems[0].ID)
	var problem domain.PRProblem
	if err != nil || json.Unmarshal(r.Data, &problem) != nil || problem.State != domain.PRProblemUnhandled {
		t.Fatal("attempt success fabricated handling", err)
	}
}

func TestPRActivityFailedAndStoppedAttemptsRetainExplicitOutcomes(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			s, _ := openTest(t)
			defer s.Close()
			f := newRemediationStoreFixture(t, s)
			f.outcome = outcome
			a, err := f.reserve(t, domain.PRRemediationAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			a = f.bind(t, a)
			a, _, err = f.start(t, a, domain.NewID(), f.observation)
			if err != nil {
				t.Fatal(err)
			}
			f.finish(t, a, true)
			v, _ := Decode[domain.PRActivity](prActivityRows(t, s)[0])
			if v.AttemptState != domain.PRRemediationFinished || v.Outcome != outcome || v.VerificationID != "" {
				t.Fatal("failed/stopped execution lost its distinct outcome")
			}
		})
	}
}

func TestPRActivityDoesNotReconstructHistoricalProblems(t *testing.T) {
	s, root := openTest(t)
	set := collectProblemFixture(t, s, 0, problemObservationFixture())
	// Model pre-feature records using the unchanged historical source layout.
	// Their original evidence remains retained, but no activity was published.
	if _, err := s.db.Exec(`DELETE FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-activity'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(readProblemFixture(t, s, set.ID)) != 2 || len(prActivityRows(t, s)) != 0 {
		t.Fatal("reopen rewrote historical evidence or inferred retrospective activity")
	}
}
