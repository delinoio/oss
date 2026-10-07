package server

import (
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func settleExecutionStartupFailure(tx *store.Tx, record store.Record, job domain.Job, revision uint64, input domain.ExecutionJobInput, sr store.Record, session domain.Session) (store.Record, error) {
	o := session.Startup.Failure
	if o.Validate() != nil || o.State != domain.StartupFailed || session.Execution != nil && session.Execution.NativeTurnID != "" {
		return store.Record{}, domain.StartupRejectionUncertain()
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return store.Record{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil || queued.Delivery != domain.InputClaimed || queued.ExecutionID != input.ExecutionID || queued.NativeRequestID != input.TurnRequestID || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(queued.Prompt)) {
		return store.Record{}, domain.StartupRejectionUncertain()
	}
	queued.Delivery = domain.InputRejected
	session.PendingInputs--
	session.PendingInputBytes -= uint64(len(queued.Prompt))
	session.ActiveExecutionID, session.NextExecutionIntent = "", ""
	session.Dispatch, session.Outcome = domain.DispatchPaused, domain.ExecutionFailed
	session.Problem = startupFailureProblem(*o)
	// A startup thread without input cannot become a native predecessor.
	session.Execution = nil
	if input.Continuation != nil {
		previous := input.Continuation.Previous
		session.Execution = &previous
	}
	if session.Archive == domain.ArchivePending {
		session.Archive = domain.Archived
	}
	if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, queued); err != nil {
		return store.Record{}, err
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	now := time.Now().UTC()
	job.Startup, job.State, job.Problem, job.FinishedAt = session.Startup, domain.JobFailed, session.Problem, &now
	if o.ProblemCode == domain.Canceled {
		job.State = domain.JobCanceled
	}
	saved, err := tx.PutJob(record.ID, revision, record.SessionID, record.ProjectID, job)
	return store.Record{ID: saved.ID}, err
}

// Resume creates a distinct attempt only from positively settled no-send proof.
// It never reroutes, consumes another input, or rewrites the failed assignment.
func queueExecutionStartupRetry(tx *store.Tx, sr store.Record, session domain.Session) (store.Record, error) {
	if session.Startup == nil || session.Startup.Failure == nil || session.Startup.Failure.Validate() != nil || session.Startup.Failure.State != domain.StartupFailed || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.PendingInputs != 0 || session.PendingInputBytes != 0 || !session.WorkspaceAvailable() || session.CompactionJobID != "" {
		return store.Record{}, domain.StartupRejectionUncertain()
	}
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		return store.Record{}, err
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return store.Record{}, err
	}
	previous, err := tx.Get(domain.JobKind, session.Startup.JobID)
	if err != nil {
		return store.Record{}, err
	}
	job, err := store.Decode[domain.Job](previous)
	var input domain.ExecutionJobInput
	if err != nil || !job.State.Terminal() || job.Type != domain.ExecuteSessionJob || job.Startup == nil || job.Startup.Failure == nil || job.Startup.Failure.State != domain.StartupFailed || previous.SessionID != sr.ID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", job.MachineID != session.MachineID) ||
		domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.ExecutionID != session.Startup.ExecutionID || input.Remediation != nil || !session.OwnsExecution(input) {
		return store.Record{}, domain.StartupRejectionUncertain()
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return store.Record{}, err
	}
	old, err := store.Decode[domain.QueuedInput](ir)
	if err != nil || old.Delivery != domain.InputRejected || old.ExecutionID != input.ExecutionID || old.NativeRequestID != input.TurnRequestID || old.Prompt != input.Input.Prompt || old.Mode != input.Input.Mode {
		return store.Record{}, domain.StartupRejectionUncertain()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return store.Record{}, err
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil || instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return store.Record{}, domain.Fail(domain.Unavailable, "The original Runner Device is disconnected.", "Reconnect it before retrying this attempt.")
	}
	input.Retry = &domain.ExecutionStartupRetry{JobID: previous.ID, ExecutionID: input.ExecutionID, InputID: input.InputID}
	input.ExecutionID, input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID(), domain.NewID()
	input.InputID, err = appendSessionInput(tx, sr.ID, &session, input.Input)
	if err != nil {
		return store.Record{}, err
	}
	input, err = checkedExecutionAssignment(tx, sr, session, machine, input)
	if err != nil {
		return store.Record{}, err
	}
	if input.Continuation == nil && input.Fork == nil {
		session.NativeExecutionRootID = input.ExecutionID
	}
	queuedRecord, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return store.Record{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](queuedRecord)
	if err != nil {
		return store.Record{}, err
	}
	queued.Delivery, queued.ExecutionID, queued.NativeRequestID = domain.InputClaimed, input.ExecutionID, input.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, queuedRecord.ID, queuedRecord.Revision, sr.ID, sr.ProjectID, queued); err != nil {
		return store.Record{}, err
	}
	session.CurrentExecution = &domain.ExecutionSelection{ID: input.ExecutionID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID}
	session.ActiveExecutionID, session.Dispatch, session.Outcome, session.Execution, session.Startup, session.Problem = input.ExecutionID, domain.DispatchClaimed, domain.ExecutionNotStarted, nil, nil, nil
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}
