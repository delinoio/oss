package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func continuationConflict() error {
	return domain.Fail(domain.Conflict, "The session is not ready for another turn.", "Keep input queued until the preceding execution, native history and owned cleanup are verified; explicitly resume paused sessions.")
}

func continuationDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Callers must roll back on every error. Advancing ownership never rewrites the
// initial snapshot/route; the successor retains the exact preceding progress.
// There is no native, credential or filesystem operation inside this transaction.
func queueContinuation(tx *store.Tx, sr store.Record, session domain.Session, explicit bool) (store.Record, error) {
	if session.InitialExecution == nil || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.Execution == nil || !session.Execution.CleanupVerified || (session.Dispatch != domain.DispatchReady && session.Dispatch != domain.DispatchBlocked && !(explicit && session.Dispatch == domain.DispatchPaused)) {
		return store.Record{}, continuationConflict()
	}
	if session.Outcome != domain.ExecutionSucceeded && session.Outcome != domain.ExecutionFailed && session.Outcome != domain.ExecutionStopped {
		return store.Record{}, continuationConflict()
	}
	intent := session.NextExecutionIntent
	if explicit {
		intent = domain.ContinueExplicitly
	}
	if intent != domain.ContinueExplicitly && (intent != domain.ContinueAutomatically || session.Outcome != domain.ExecutionSucceeded) {
		return store.Record{}, continuationConflict()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return store.Record{}, err
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil {
		return store.Record{}, err
	}
	if instance.Validate() != nil || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
		return store.Record{}, domain.Fail(domain.Unavailable, "The original Worker is not currently connected.", "Reconnect the owning machine; the retained input and account selection remain unchanged.")
	}
	selected := session.ExecutionSelection()
	previous, err := tx.SessionExecutionJob(sr.ID, selected.ID)
	if err != nil {
		return store.Record{}, err
	}
	job, err := store.Decode[domain.Job](previous)
	if err != nil {
		return store.Record{}, err
	}
	var assignment domain.ExecutionJobInput
	var completion domain.ExecutionCompletion
	if domain.Decode(job.Input, &assignment) != nil || assignment.Validate() != nil || !session.OwnsExecution(assignment) || assignment.SessionID != sr.ID || job.MachineID != session.MachineID || domain.Decode(job.Output, &completion) != nil || completion.Version != 2 || completion.Validate() != nil || session.Execution.JobID != previous.ID {
		return store.Record{}, nativeCompletionUncertain()
	}
	terminalState := map[domain.ExecutionOutcome]domain.JobState{domain.ExecutionSucceeded: domain.JobSucceeded, domain.ExecutionFailed: domain.JobFailed, domain.ExecutionStopped: domain.JobCanceled}[completion.Outcome]
	if job.State != terminalState || job.FinishedAt == nil {
		return store.Record{}, nativeCompletionUncertain()
	}
	priorInput, err := tx.Get(domain.QueueKind, assignment.InputID)
	if err != nil {
		return store.Record{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](priorInput)
	if err != nil {
		return store.Record{}, err
	}
	if priorInput.SessionID != sr.ID || queued.Delivery != domain.InputAccepted || queued.ExecutionID != assignment.ExecutionID || queued.NativeRequestID != assignment.TurnRequestID || queued.Prompt != assignment.Input.Prompt || queued.Mode != assignment.Input.Mode {
		return store.Record{}, nativeCompletionUncertain()
	}
	// Recheck current authority against the immutable selection even when Resume
	// has no input yet. The Worker rechecks native history before the later send.
	input, err := checkedExecutionAssignment(tx, sr, session, machine, assignment)
	if err != nil {
		return store.Record{}, err
	}
	if !bytes.Equal(input.Preparation, assignment.Preparation) || !bytes.Equal(input.Manifest, assignment.Manifest) {
		return store.Record{}, nativeCompletionUncertain()
	}
	input.Version, input.ExecutionID, input.InputID = 2, domain.NewID(), domain.NewID()
	input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID()
	input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: session.InitialExecution.ID, HistoryRequestID: domain.NewID(), Previous: *session.Execution, Completion: completion, AssignmentInputDigest: continuationDigest(job.Input), InputMode: assignment.Input.Mode, PromptDigest: continuationDigest([]byte(assignment.Input.Prompt)), Intent: intent}
	if err := input.Validate(); err != nil {
		return store.Record{}, err
	}
	ir, err := tx.OldestQueuedInput(sr.ID)
	if err != nil {
		if !explicit || domain.SafeError(err).Code != domain.MissingInput || session.PendingInputs != 0 || session.PendingInputBytes != 0 {
			return store.Record{}, err
		}
		session.Dispatch, session.NextExecutionIntent, session.Problem = domain.DispatchReady, intent, nil
		_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
		return store.Record{}, err
	}
	next, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return store.Record{}, err
	}
	if ir.SessionID != sr.ID || next.Delivery != domain.InputQueued || next.ExecutionID != "" || next.NativeRequestID != "" || next.Sequence <= queued.Sequence || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(next.Prompt)) {
		return store.Record{}, continuationConflict()
	}
	input.InputID, input.Input = ir.ID, domain.SessionInput{Prompt: next.Prompt, Mode: next.Mode}
	if err := input.Validate(); err != nil {
		return store.Record{}, err
	}
	next.Delivery, next.ExecutionID, next.NativeRequestID = domain.InputClaimed, input.ExecutionID, input.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, next); err != nil {
		return store.Record{}, err
	}
	session.CurrentExecution = &domain.ExecutionSelection{ID: input.ExecutionID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID}
	session.ActiveExecutionID, session.Outcome, session.Dispatch = input.ExecutionID, domain.ExecutionNotStarted, domain.DispatchClaimed
	session.Execution, session.Problem, session.NextExecutionIntent = nil, nil, ""
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}
