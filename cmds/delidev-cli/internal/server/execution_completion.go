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
	if !session.OwnsExecution(input) || session.ActiveExecutionID != input.ExecutionID || (session.Execution != nil && session.Execution.JobID != record.ID) {
		return input, sr, session, executionEventConflict()
	}
	return input, sr, session, nil
}

func finishNativeExecution(tx *store.Tx, record store.Record, job domain.Job, expectedRevision uint64, raw json.RawMessage, reported *domain.Error) (store.Record, error) {
	input, sr, session, err := nativeExecutionScope(tx, record, job)
	if err != nil {
		return store.Record{}, err
	}
	job.Startup = session.Startup
	if input.Version == 4 && session.Startup != nil && session.Startup.Ready != nil {
		input.Installation.Version = session.Startup.Ready.NativeVersion
	}
	if input.Version == 4 && reported != nil && session.Startup != nil && session.Startup.JobID == record.ID && session.Startup.Failure != nil && session.Startup.Failure.State == domain.StartupFailed {
		return settleExecutionStartupFailure(tx, record, job, expectedRevision, input, sr, session)
	}
	var completion domain.ExecutionCompletion
	progress := session.Execution
	verified := reported == nil && domain.Decode(raw, &completion) == nil && completion.ValidateForHarness(input.Configuration.Harness) == nil && completion.ExecutionID == input.ExecutionID && completion.InputID == input.InputID && progress != nil && progress.JobID == record.ID && progress.ExecutionID == input.ExecutionID && progress.InputID == input.InputID && progress.NativeThreadID == string(completion.NativeThreadID) && progress.NativeTurnID == string(completion.NativeTurnID) && progress.LastSequence == completion.LastSequence && progress.Outcome == completion.Outcome && !progress.CleanupVerified && progress.Subagents.Closed()
	if input.Configuration.Harness == domain.GrokBuild && verified {
		ordinary := completion.Outcome == domain.ExecutionSucceeded && progress.GrokStop == nil && progress.GrokTerminal != nil && progress.GrokTerminal.Validate(progress.NativeThreadID) == nil && progress.GrokTerminal.Model == input.Configuration.NativeModel
		stopped := progress.GrokTerminal == nil && progress.GrokStop != nil && progress.GrokStop.Validate(progress.NativeThreadID) == nil && progress.GrokStop.Outcome() == completion.Outcome && progress.GrokStop.Model == input.Configuration.NativeModel && progress.GrokStop.InputID == input.InputID && progress.GrokStop.InputRequestID == input.TurnRequestID
		if stopped {
			stopped, err = tx.JobCancellationRequested(record.ID)
			if err != nil {
				return store.Record{}, err
			}
		}
		tools := progress.GrokToolsTerminal != nil && progress.GrokTerminal == nil && progress.GrokStop == nil && progress.GrokToolsTerminal.Validate(progress.NativeThreadID) == nil && progress.GrokToolsTerminal.Outcome() == completion.Outcome && progress.GrokToolsTerminal.Model == input.Configuration.NativeModel
		verified = completion.Version == 1 && (tools || (ordinary || stopped) && input.Input.Mode == domain.ExecuteMode)
	}
	if input.Configuration.Harness == domain.ClaudeCode {
		// A digest alone cannot grant continuation after a changed permission,
		// interrupted input or an uncomposed tool/callback history profile.
		if verified {
			ordinary := progress.ClaudeTerminal != nil && progress.ClaudeStop == nil && progress.ClaudeDenial == nil && progress.ClaudeTerminal.Validate() == nil && progress.ClaudeTerminal.InputID == input.InputID && progress.ClaudeTerminal.Outcome() == completion.Outcome
			stopped := progress.ClaudeStop != nil && progress.ClaudeTerminal == nil && progress.ClaudeDenial == nil && progress.ClaudeStop.Validate() == nil && progress.ClaudeStop.InputID == input.InputID && completion.Outcome == domain.ExecutionStopped
			denied := progress.ClaudeDenial != nil && progress.ClaudeTerminal == nil && progress.ClaudeStop == nil && progress.ClaudeDenial.Validate() == nil && progress.ClaudeDenial.InputID == input.InputID && completion.Outcome == domain.ExecutionStopped
			verified = completion.Version == 1 && (ordinary || stopped || denied)
			if completion.Version == 2 && ordinary && progress.ClaudeContinuationBoundary(input.InputID) {
				verified, err = tx.ClaudeRootContentContinuation(input.ExecutionID)
				if err != nil {
					return store.Record{}, err
				}
			}
		}
	}
	if verified && len(progress.Subagents) != 0 && completion.Version != 1 {
		verified = false
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	previousDispatch := session.Dispatch
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	if verified {
		progress.CleanupVerified = true
		if input.Configuration.Harness == domain.GrokBuild && progress.GrokStop == nil && session.Outcome == domain.ExecutionSucceeded {
			// Stop/Archive can commit after ordinary native success but before
			// this cleanup report. Retain native success and cleanup independently;
			// only an uncanceled successful product input enters accounting.
			canceled, err := tx.JobCancellationRequested(record.ID)
			if err != nil {
				return store.Record{}, err
			}
			if !canceled {
				if err := tx.PutGrokAccounting(record.ID, sr.ProjectID, input, *progress, completion); err != nil {
					return store.Record{}, err
				}
			}
		}
		if completion.Version == 2 && completion.Outcome == domain.ExecutionSucceeded && session.Outcome == domain.ExecutionSucceeded && previousDispatch == domain.DispatchClaimed && session.Archive == domain.NotArchived && session.Recovery == domain.NoRecovery && progress.Waiting == (domain.NativeWaiting{}) && progress.UnconfirmedResponses == 0 {
			session.Dispatch, session.NextExecutionIntent = domain.DispatchReady, domain.ContinueAutomatically
		}
		if session.Recovery == domain.NoRecovery {
			session.ActiveExecutionID = ""
			// Agent cleanup proves only its own process graph. Store.Put
			// separately waits for original terminal and forward cleanup.
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
	if input.SidechatRetry != nil {
		for i := range session.SidechatRetries {
			g := &session.SidechatRetries[i]
			if g.ID == input.SidechatRetry.GenerationID && g.ExecutionID == input.ExecutionID && g.ExecutionJobID == record.ID {
				if verified {
					canceled, err := tx.JobCancellationRequested(record.ID)
					if err != nil {
						return store.Record{}, err
					}
					if completion.Outcome == domain.ExecutionSucceeded && !canceled && session.Archive == domain.NotArchived && session.Recovery == domain.NoRecovery {
						g.Completed = true
						session.SidechatCurrentAnswer = input.ExecutionID
					}
					session.SidechatActiveRetry = ""
				}
			}
		}
	}
	if verified && completion.Outcome == domain.ExecutionSucceeded {
		if err := queueAutomaticSessionTitle(tx, sr, &session, record, input, false); err != nil {
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
	if err := retireSteer(tx, sr, session, true); err != nil {
		return err
	}
	if err := invalidateQuestionResponses(tx, input); err != nil {
		return err
	}
	session.Recovery, session.Dispatch, session.NextExecutionIntent = domain.NeedsRecovery, domain.DispatchPaused, ""
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
	if job.Type == domain.CompactSessionJob {
		return loseSessionCompaction(tx, record, job)
	}
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
