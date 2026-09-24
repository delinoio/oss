package server

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func controlNativeSession(tx *store.Tx, sr store.Record, session *domain.Session, action domain.SessionAction) error {
	if action == domain.RestoreSession {
		if session.Archive != domain.Archived {
			return domain.Fail(domain.Conflict, "The session is not archived.", "Inspect its current native cleanup and visibility state.")
		}
		session.Archive, session.Dispatch, session.NextExecutionIntent = domain.NotArchived, domain.DispatchPaused, ""
		return nil
	}
	if action != domain.StopSession && action != domain.ArchiveSession {
		return domain.SessionExecutionUnavailable()
	}
	r, err := tx.SessionExecutionJob(sr.ID, session.ExecutionSelection().ID)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		return err
	}
	progress := session.Execution
	if progress != nil && (progress.JobID != r.ID || progress.ExecutionID != session.ExecutionSelection().ID || progress.InputID != session.ExecutionSelection().InputID) {
		return executionEventConflict()
	}
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	if action == domain.ArchiveSession {
		session.Archive = domain.ArchivePending
	}
	if session.ActiveExecutionID == "" && session.Recovery == domain.NoRecovery && progress != nil && progress.CleanupVerified && job.State.Terminal() {
		if action == domain.ArchiveSession {
			session.Archive = domain.Archived
		}
		return nil
	}
	input, _, _, err := nativeExecutionScope(tx, r, job)
	if err != nil {
		return err
	}
	if err := tx.RequestJobCancellation(r.ID); err != nil {
		return err
	}
	if job.State == domain.JobQueued {
		now := time.Now().UTC()
		job.State, job.FinishedAt = domain.JobCanceled, &now
		job.Problem = domain.Fail(domain.Canceled, "The native job was canceled before Worker dispatch.", "The immutable first claim remains paused for explicit recovery; no input was sent by this job.")
		if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job); err != nil {
			return err
		}
		session.Outcome, session.Problem = domain.ExecutionStopped, job.Problem
	}
	if job.State != domain.JobClaimed {
		return retainNativeUncertainty(tx, input, sr, session)
	}
	return nil
}

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
			session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
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
