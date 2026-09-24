package server

import (
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func nativeExecutionScope(tx *store.Tx, record store.Record, job domain.Job) (domain.ExecutionJobInput, store.Record, domain.Session, error) {
	var input domain.ExecutionJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.SessionID != record.SessionID || input.MachineID != job.MachineID {
		return input, store.Record{}, domain.Session{}, executionEventConflict()
	}
	sr, session, err := sessionRecord(tx, input.SessionID)
	if err != nil {
		return input, sr, session, err
	}
	initial := session.InitialExecution
	if initial == nil || initial.ID != input.ExecutionID || initial.ConfigurationDigest != input.ConfigurationDigest || initial.InputID != input.InputID || initial.InitialAccountID != input.AccountID || initial.ConnectionID != input.ConnectionID || session.ActiveExecutionID != input.ExecutionID || (session.Execution != nil && session.Execution.JobID != record.ID) {
		return input, sr, session, executionEventConflict()
	}
	return input, sr, session, nil
}

func finishNativeExecution(tx *store.Tx, record store.Record, job domain.Job, expectedRevision uint64, raw json.RawMessage, reported *domain.Error) (store.Record, error) {
	input, sr, session, err := nativeExecutionScope(tx, record, job)
	if err != nil {
		return store.Record{}, err
	}
	var completion domain.ExecutionCompletion
	progress := session.Execution
	verified := reported == nil && domain.Decode(raw, &completion) == nil && completion.Validate() == nil && completion.ExecutionID == input.ExecutionID && completion.InputID == input.InputID && progress != nil && progress.JobID == record.ID && progress.ExecutionID == input.ExecutionID && progress.InputID == input.InputID && progress.NativeThreadID == string(completion.NativeThreadID) && progress.NativeTurnID == string(completion.NativeTurnID) && progress.LastSequence == completion.LastSequence && progress.Outcome == completion.Outcome && !progress.CleanupVerified
	now := time.Now().UTC()
	job.FinishedAt = &now
	session.Dispatch = domain.DispatchPaused
	if verified {
		progress.CleanupVerified = true
		if session.Recovery == domain.NoRecovery {
			session.ActiveExecutionID = ""
			// This first-execution profile currently owns only the agent process
			// graph. Future terminals/forwards must join this completion gate
			// before they can become session-owned public resources.
			if session.Archive == domain.ArchivePending {
				session.Archive = domain.Archived
			}
		}
		job.State = domain.JobSucceeded
		job.Problem = nil
		job.Output, err = json.Marshal(completion)
		if err != nil {
			return store.Record{}, err
		}
		if completion.Outcome != domain.ExecutionSucceeded {
			job.State = domain.JobFailed
			if completion.Outcome == domain.ExecutionStopped {
				job.State = domain.JobCanceled
			}
			job.Problem = session.Problem
		}
	} else {
		// A terminal native event without the exact owned-cleanup report is not
		// sufficient. Keep the execution/input claim reachable for recovery;
		// neither an error nor an empty journal can authorize another send.
		job.State, job.Problem, job.Output = domain.JobUncertain, nativeCompletionUncertain(), nil
		if err := retainNativeUncertainty(tx, input, sr, &session); err != nil {
			return store.Record{}, err
		}
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	saved, err := tx.PutJob(record.ID, expectedRevision, record.SessionID, record.ProjectID, job)
	if err != nil {
		return store.Record{}, err
	}
	// The native completion receipt is a reference, not another prompt-bearing
	// copy of the immutable assignment. ReportWork expands it after commit.
	return store.Record{ID: saved.ID}, nil
}

func nativeCompletionUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native execution completion or owned cleanup requires reconciliation.", "Preserve the Worker runtime, event outbox and process journals; never replay the initial input.")
}

func retainNativeUncertainty(tx *store.Tx, input domain.ExecutionJobInput, sr store.Record, session *domain.Session) error {
	session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
	if session.Problem == nil {
		session.Problem = nativeCompletionUncertain()
	}
	if session.Outcome == domain.ExecutionRunning || session.Outcome == domain.ExecutionNotStarted {
		session.Outcome = domain.ExecutionFailed
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return err
	}
	if ir.SessionID != sr.ID || queued.ExecutionID != input.ExecutionID {
		return executionEventConflict()
	}
	if queued.Delivery == domain.InputClaimed {
		queued.Delivery = domain.InputUncertain
		_, err = tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, queued)
	}
	return err
}

// Worker replacement/revocation cannot leave a session apparently running or
// make its claimed input editable. Native terminal facts remain independent of
// cleanup: even a published success still needs its original owned journals.
func finishLostNativeExecution(tx *store.Tx, record store.Record, job domain.Job) error {
	if job.Type != domain.ExecuteSessionJob {
		return nil
	}
	input, sr, session, err := nativeExecutionScope(tx, record, job)
	if err != nil {
		return err
	}
	if err := retainNativeUncertainty(tx, input, sr, &session); err != nil {
		return err
	}
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}
