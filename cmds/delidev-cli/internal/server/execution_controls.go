package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Account disconnection revokes relay access and durably cancels the owning
// native jobs in the same acceptance transaction. Transport/native cleanup is
// separate; this function never treats a cancellation request as proof of exit.
func cancelAccountExecutions(tx *store.Tx, account domain.ID) error {
	var after domain.ID
	for {
		records, err := tx.AccountExecutionJobs(account, after, store.MaxPage)
		if err != nil {
			return err
		}
		for _, r := range records {
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				return err
			}
			input, sr, session, err := nativeExecutionScope(tx, r, job)
			if err != nil {
				return err
			}
			if input.AccountID != account {
				return executionEventConflict()
			}
			if err := tx.RequestJobCancellation(r.ID); err != nil {
				return err
			}
			session.Dispatch = domain.DispatchPaused
			if session.Problem == nil {
				session.Problem = domain.Fail(domain.Unauthenticated, "The selected account was disconnected; native execution cancellation was requested.", "Retain the original execution until its cleanup and input acceptance are reconciled.")
			}
			if job.State == domain.JobQueued {
				// An undispatched job cannot start after disconnect. Its immutable
				// first claim remains available for explicit native-state recovery;
				// deleting it would allow later configuration to replace the claim.
				now := time.Now().UTC()
				job.State, job.FinishedAt, job.Problem = domain.JobCanceled, &now, session.Problem
				if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job); err != nil {
					return err
				}
			}
			if job.State != domain.JobClaimed {
				if err := retainNativeUncertainty(tx, input, sr, &session); err != nil {
					return err
				}
			}
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return err
			}
			after = r.ID
		}
		if len(records) < store.MaxPage {
			return nil
		}
	}
}
