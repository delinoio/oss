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
	return domain.Fail(domain.Conflict, "The session is not ready for another turn.", "Check workspace availability, the selected account and queued input; explicitly resume paused sessions.")
}

func continuationDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Callers must roll back on every error. Advancing ownership never rewrites the
// initial snapshot/route; the successor retains the exact preceding progress.
// There is no native, credential or filesystem operation inside this transaction.
func queueContinuation(tx *store.Tx, sr store.Record, session domain.Session, explicit bool) (store.Record, error) {
	if session.Recovery != domain.NoRecovery || session.Execution == nil || !session.Execution.CleanupVerified {
		return queueUnconfirmedSuccessor(tx, sr, session, explicit)
	}
	if session.CompactionJobID != "" {
		return store.Record{}, domain.CompactionUncertain()
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return store.Record{}, err
	}
	if !session.WorkspaceAvailable() || session.InitialExecution == nil || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.Execution == nil || (session.Dispatch != domain.DispatchReady && session.Dispatch != domain.DispatchBlocked && !(explicit && session.Dispatch == domain.DispatchPaused)) {
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
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		return store.Record{}, err
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
	assignment, completion, assignmentDigest, err := checkedContinuationPredecessor(tx, sr, session)
	if err != nil {
		return store.Record{}, err
	}
	// Recheck current authority against the immutable selection even when Resume
	// has no input yet. The Worker rechecks native history before the later send.
	account, connection := session.ContinuationAccount()
	candidate := continuationAssignment(session, assignment, completion, assignmentDigest, intent, account, connection)
	if session.Compaction != nil && session.Compaction.ExecutionID == assignment.ExecutionID {
		candidate.Continuation.Compaction = session.Compaction
	}
	input, err := checkedExecutionAssignment(tx, sr, session, machine, candidate)
	if err != nil {
		return store.Record{}, err
	}
	if !bytes.Equal(input.Preparation, assignment.Preparation) || !bytes.Equal(input.Manifest, assignment.Manifest) {
		return store.Record{}, nativeCompletionUncertain()
	}
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
	prior, err := tx.Get(domain.QueueKind, assignment.InputID)
	if err != nil {
		return store.Record{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](prior)
	if err != nil {
		return store.Record{}, err
	}
	if ir.SessionID != sr.ID || next.Delivery != domain.InputQueued || next.ExecutionID != "" || next.NativeRequestID != "" || next.Sequence <= queued.Sequence || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(next.Prompt)) {
		return store.Record{}, continuationConflict()
	}
	input.InputID, input.Input = ir.ID, domain.SessionInput{Prompt: next.Prompt, Mode: next.Mode}
	if input.Configuration.Harness == domain.OpenCode {
		if _, err := input.Configuration.OpenCodePrimaryForInput(input.Input.Mode); err != nil {
			return store.Record{}, err
		}
	}
	if err := input.Validate(); err != nil {
		return store.Record{}, err
	}
	next.Delivery, next.ExecutionID, next.NativeRequestID = domain.InputClaimed, input.ExecutionID, input.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, next); err != nil {
		return store.Record{}, err
	}
	session.CurrentExecution = &domain.ExecutionSelection{ID: input.ExecutionID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID}
	session.ActiveExecutionID, session.Outcome, session.Dispatch = input.ExecutionID, domain.ExecutionNotStarted, domain.DispatchClaimed
	session.ExecutionRecoveryJobID = ""
	session.Execution, session.Problem, session.NextExecutionIntent = nil, nil, ""
	session.CurrentNativeHistory = ""
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}

func checkContinuationInputs(tx *store.Tx, sessionID domain.ID, assignment domain.ExecutionJobInput, progress domain.ExecutionProgress) error {
	bindings, err := domain.CheckedExecutionInputs(assignment.InputID, continuationDigest([]byte(assignment.Input.Prompt)), progress.AcceptedInputs)
	if err != nil {
		return err
	}
	requests := make(map[domain.ID]bool, len(bindings))
	for _, binding := range bindings {
		record, err := tx.Get(domain.QueueKind, binding.InputID)
		if err != nil {
			return err
		}
		input, err := store.Decode[domain.QueuedInput](record)
		if err != nil {
			return err
		}
		if record.SessionID != sessionID || input.Delivery != domain.InputAccepted || input.ExecutionID != assignment.ExecutionID || input.Mode != assignment.Input.Mode || input.NativeRequestID.Validate() != nil || requests[input.NativeRequestID] || domain.BindExecutionInput(record.ID, input.Prompt) != binding {
			return nativeCompletionUncertain()
		}
		requests[input.NativeRequestID] = true
	}
	return nil
}

func checkedContinuationPredecessor(tx *store.Tx, sr store.Record, session domain.Session) (domain.ExecutionJobInput, domain.ExecutionCompletion, string, error) {
	if session.InitialExecution == nil || session.Execution == nil || session.ActiveExecutionID != "" || session.PendingSteerID != "" {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", continuationConflict()
	}
	if (session.Outcome != domain.ExecutionSucceeded && session.Outcome != domain.ExecutionFailed && session.Outcome != domain.ExecutionStopped) || session.Execution.Outcome != session.Outcome {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", continuationConflict()
	}
	selected := session.ExecutionSelection()
	previous, err := tx.SessionExecutionJob(sr.ID, selected.ID)
	if err != nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", err
	}
	job, err := store.Decode[domain.Job](previous)
	if err != nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", err
	}
	var assignment domain.ExecutionJobInput
	var completion domain.ExecutionCompletion
	if domain.Decode(job.Input, &assignment) != nil || assignment.Validate() != nil || !session.OwnsExecution(assignment) || assignment.SessionID != sr.ID ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", job.MachineID != session.MachineID) ||
		domain.Decode(job.Output, &completion) != nil || completion.Version != 2 || completion.ValidateForHarness(assignment.Configuration.Harness) != nil || session.Execution.JobID != previous.ID {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", nativeCompletionUncertain()
	}
	terminalState := map[domain.ExecutionOutcome]domain.JobState{domain.ExecutionSucceeded: domain.JobSucceeded, domain.ExecutionFailed: domain.JobFailed, domain.ExecutionStopped: domain.JobCanceled}[completion.Outcome]
	if job.State != terminalState || job.FinishedAt == nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", nativeCompletionUncertain()
	}
	priorInput, err := tx.Get(domain.QueueKind, assignment.InputID)
	if err != nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", err
	}
	queued, err := store.Decode[domain.QueuedInput](priorInput)
	if err != nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", err
	}
	if priorInput.SessionID != sr.ID || queued.Delivery != domain.InputAccepted || queued.ExecutionID != assignment.ExecutionID || queued.NativeRequestID != assignment.TurnRequestID || queued.Prompt != assignment.Input.Prompt || queued.Mode != assignment.Input.Mode {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", nativeCompletionUncertain()
	}
	if err := checkContinuationInputs(tx, sr.ID, assignment, *session.Execution); err != nil {
		return domain.ExecutionJobInput{}, domain.ExecutionCompletion{}, "", err
	}

	return assignment, completion, continuationDigest(job.Input), nil
}

func continuationAssignment(session domain.Session, assignment domain.ExecutionJobInput, completion domain.ExecutionCompletion, digest string, intent domain.ExecutionIntent, account, connection domain.ID) domain.ExecutionJobInput {
	input := assignment
	// A successor must not inherit the preceding one-shot PR Git authority.
	input.Remediation, input.Retry = nil, nil
	input.Version, input.ExecutionID, input.InputID = 2, domain.NewID(), domain.NewID()
	// The first child turn imports the fork checkpoint. Every later turn uses
	// its own verified completion on that history, never the creation boundary.
	input.Fork = nil
	input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID()
	input.AccountID, input.ConnectionID = account, connection
	input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: session.NativeExecutionRoot(), HistoryRequestID: domain.NewID(), Previous: *session.Execution, Completion: completion, AssignmentInputDigest: digest, InputMode: assignment.Input.Mode, PromptDigest: continuationDigest([]byte(assignment.Input.Prompt)), Intent: intent}
	// A switch back may select the same account after its connection rotated.
	// The checkpoint still belongs to the complete original account/connection.
	if account != assignment.AccountID || connection != assignment.ConnectionID {
		input.Continuation.PreviousAccountID, input.Continuation.PreviousConnectionID = assignment.AccountID, assignment.ConnectionID
	}
	return input
}

// An unconfirmed predecessor remains historical. A fresh attempt consumes only a
// new queued input; it never replays a claimed input or manufactures a checkpoint.
func queueUnconfirmedSuccessor(tx *store.Tx, sr store.Record, session domain.Session, explicit bool) (store.Record, error) {
	if session.InitialExecution == nil || !session.WorkspaceAvailable() || session.Archive != domain.NotArchived || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || (session.Dispatch == domain.DispatchPaused && !explicit) {
		return store.Record{}, continuationConflict()
	}
	domain.ObserveOwnership(domain.OwnershipCleanup, session.ExecutionSelection().ID)
	ir, err := tx.OldestQueuedInput(sr.ID)
	if err != nil {
		if explicit && domain.SafeError(err).Code == domain.MissingInput {
			session.Dispatch, session.NextExecutionIntent = domain.DispatchReady, domain.ContinueExplicitly
			_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
			return store.Record{}, err
		}
		return store.Record{}, err
	}
	next, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return store.Record{}, err
	}
	if next.Delivery != domain.InputQueued || next.ExecutionID != "" || next.NativeRequestID != "" || session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(next.Prompt)) {
		return store.Record{}, continuationConflict()
	}
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		return store.Record{}, err
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
		return store.Record{}, domain.Fail(domain.Unavailable, "The selected Worker is not connected.", "Reconnect the selected Worker.")
	}
	prior, err := tx.SessionExecutionJob(sr.ID, session.ExecutionSelection().ID)
	if err != nil {
		return store.Record{}, err
	}
	old, err := store.Decode[domain.Job](prior)
	if err != nil {
		return store.Record{}, err
	}
	var input domain.ExecutionJobInput
	if err := domain.Decode(old.Input, &input); err != nil {
		return store.Record{}, err
	}
	if err := input.Validate(); err != nil {
		return store.Record{}, err
	}
	input.ExecutionID, input.InputID = domain.NewID(), ir.ID
	input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID()
	input.MachineID = session.MachineID
	input.AccountID, input.ConnectionID = session.ContinuationAccount()
	input.Input = domain.SessionInput{Prompt: next.Prompt, Mode: next.Mode}
	input.Continuation, input.Fork, input.Retry, input.Remediation = nil, nil, nil, nil
	input, err = checkedExecutionAssignment(tx, sr, session, machine, input)
	if err != nil {
		return store.Record{}, err
	}
	next.Delivery, next.ExecutionID, next.NativeRequestID = domain.InputClaimed, input.ExecutionID, input.TurnRequestID
	if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, next); err != nil {
		return store.Record{}, err
	}
	session.CurrentExecution = &domain.ExecutionSelection{ID: input.ExecutionID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID}
	session.NativeExecutionRootID, session.ActiveExecutionID = input.ExecutionID, input.ExecutionID
	session.Outcome, session.Dispatch, session.Recovery = domain.ExecutionNotStarted, domain.DispatchClaimed, domain.NoRecovery
	session.Execution, session.Startup, session.Problem = nil, nil, nil
	session.ExecutionRecoveryJobID, session.CurrentNativeHistory, session.NextExecutionIntent, session.CompactionJobID = "", "", "", ""
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}
