package server

import (
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func queueForkInitialExecution(tx *store.Tx, sr store.Record, session domain.Session, explicit bool) (store.Record, error) {
	if session.InitialExecution != nil || session.CurrentExecution != nil || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.Dispatch == domain.DispatchPaused && !explicit {
		return store.Record{}, firstDispatchConflict()
	}
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		return store.Record{}, err
	}
	f := session.Fork
	row, err := tx.Get(domain.JobKind, f.JobID)
	if err != nil {
		return store.Record{}, err
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil {
		return store.Record{}, err
	}
	var result domain.ForkJobResult
	var accepted domain.ForkJobInput
	if job.Type != domain.ForkSessionJob || job.State != domain.JobSucceeded || domain.Decode(job.Input, &accepted) != nil || accepted.Validate() != nil || domain.Decode(job.Output, &result) != nil || result.ChildSessionID != sr.ID || result.RuntimeID != f.RuntimeID || result.CheckpointDigest != f.CheckpointDigest || result.NativeThreadID != f.NativeThreadID || result.NativeTurnID != f.SourceTurnID || accepted.Snapshot.ConfigurationDigest != f.Snapshot.ConfigurationDigest || accepted.SourceSessionID != f.SourceSessionID {
		return store.Record{}, forkConflict()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return store.Record{}, err
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil || instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout {
		return store.Record{}, forkConflict()
	}
	// Check current account/project/installation eligibility even when explicit
	// Resume has no queued input. Neither empty Resume nor fork advances routing.
	input := domain.ExecutionJobInput{Version: 3, SessionID: sr.ID, MachineID: session.MachineID, ExecutionID: domain.NewID(), InputID: domain.NewID(), ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: f.Snapshot.Configuration, ConfigurationDigest: f.Snapshot.ConfigurationDigest, AccountID: f.Snapshot.InitialAccountID, ConnectionID: f.Snapshot.ConnectionID, Fork: &domain.ForkExecution{JobID: f.JobID, RuntimeID: f.RuntimeID, NativeThreadID: f.NativeThreadID, NativeTurnID: f.SourceTurnID, CheckpointDigest: f.CheckpointDigest, HistoryRequestID: domain.NewID()}, Input: domain.SessionInput{Prompt: "Fork eligibility check", Mode: domain.ExecuteMode}}
	if _, err := checkedExecutionSelection(tx, session, machine, input); err != nil {
		return store.Record{}, err
	}
	ir, err := tx.OldestQueuedInput(sr.ID)
	if err != nil {
		if !explicit || domain.SafeError(err).Code != domain.MissingInput || session.PendingInputs != 0 {
			return store.Record{}, err
		}
		session.Dispatch, session.NextExecutionIntent = domain.DispatchReady, domain.ContinueExplicitly
		_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
		return store.Record{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return store.Record{}, err
	}
	if queued.Delivery != domain.InputQueued || queued.ExecutionID != "" || queued.NativeRequestID != "" || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(queued.Prompt)) {
		return store.Record{}, forkConflict()
	}
	input.InputID, input.Input = ir.ID, domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}
	input, err = checkedExecutionAssignment(tx, sr, session, machine, input)
	if err != nil {
		return store.Record{}, err
	}
	initial := f.Snapshot
	initial.ID, initial.InputID, initial.AcceptedAt = f.RuntimeID, ir.ID, time.Now().UTC()
	session.InitialExecution = &initial
	session.CurrentExecution = &domain.ExecutionSelection{ID: input.ExecutionID, InputID: ir.ID, AccountID: input.AccountID, ConnectionID: input.ConnectionID}
	session.ActiveExecutionID, session.Dispatch, session.Problem, session.NextExecutionIntent = input.ExecutionID, domain.DispatchClaimed, nil, ""
	queued.Delivery, queued.ExecutionID, queued.NativeRequestID = domain.InputClaimed, input.ExecutionID, input.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, queued); err != nil {
		return store.Record{}, err
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}
