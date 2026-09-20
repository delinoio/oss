package core

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

// Every exact-commit gate linearizes at the same metadata revalidation point.
// Hash evidence outside the SQLite writer lock, then reject stale snapshots.
func (s *Service) selectLatestAttempt(ctx context.Context, plan Run, evaluate func(Run) Gate, submit bool) (Gate, error) {
	var candidate *Run
	var verified Gate
	for {
		if err := ctx.Err(); err != nil {
			return Gate{}, err
		}
		var gate Gate
		created, verify := false, false
		err := s.Store.Transaction(func(tx *sql.Tx) error {
			// Only metadata selection/revalidation and insertion own the writer
			// lock. Potentially unbounded evidence hashing happens after release.
			var id string
			err := tx.QueryRow("SELECT id FROM runs WHERE repo=? AND commit_oid=? AND fingerprint=? ORDER BY seq DESC LIMIT 1", plan.RepositoryID, plan.Commit, plan.Fingerprint).Scan(&id)
			if err == nil {
				r, err := loadRun(tx, id)
				if err != nil {
					return err
				}
				if !r.State.Terminal() {
					gate = s.GateRun(r) // Nonterminal states never inspect evidence.
					return nil
				}
				if candidate == nil || !bytes.Equal(Encode(r), Encode(*candidate)) {
					candidate, verify = &r, true
					return nil
				}
				gate = verified
				if gate.Passed || !submit {
					return nil
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if !submit {
				gate = Gate{Commit: plan.Commit, State: Queued, Reason: "no compatible execution; run ach run --commit " + plan.Commit}
				return nil
			}
			if err = insertRun(tx, &plan, ""); err != nil {
				return err
			}
			gate = s.GateRun(plan)
			created = true
			return nil
		})
		if err != nil {
			if submit {
				return Gate{}, Wrap("submission-failed", err)
			}
			return Gate{}, err
		}
		if verify {
			verified = evaluate(*candidate)
			// A concurrent submission or retention mutation invalidates this
			// snapshot. The next short transaction reselects before using it.
			continue
		}
		if created {
			s.Log.Info("run.accepted", "run_id", plan.ID, "commit", plan.Commit)
		}
		return gate, nil
	}
}
