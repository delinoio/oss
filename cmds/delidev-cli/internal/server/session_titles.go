package server

import (
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func cancelSessionTitleJob(tx *store.Tx, jobID domain.ID) (bool, error) {
	if jobID == "" {
		return false, nil
	}
	record, err := tx.Get(domain.JobKind, jobID)
	if err != nil {
		return false, err
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil || job.Type != domain.GenerateSessionTitleJob {
		return false, domain.Fail(domain.RecoveryRequired, "The retained title job does not match its session ownership.", "Preserve the session and inspect the original title operation before changing visibility.")
	}
	switch job.State {
	case domain.JobQueued:
		now := time.Now().UTC()
		job.State, job.FinishedAt = domain.JobCanceled, &now
		job.Problem = domain.Fail(domain.Canceled, "The automatic title was canceled before Worker assignment.", "Keep the current session title; no inference was sent.")
		_, err = tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job)
		return false, err
	case domain.JobClaimed:
		return true, tx.RequestJobCancellation(jobID)
	case domain.JobUncertain:
		return true, nil
	case domain.JobSucceeded, domain.JobFailed, domain.JobCanceled:
		return false, nil
	default:
		return false, domain.Fail(domain.RecoveryRequired, "The retained title job has an unknown state.", "Preserve its original ownership and inspect the session before another operation.")
	}
}

func titleProfileAvailable(tx *store.Tx, input domain.ExecutionJobInput) (domain.Provider, bool, error) {
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return domain.Provider{}, false, err
	}
	if input.Configuration.Harness != domain.Codex || input.Installation.Version != domain.CodexProtocolVersion || !input.Installation.ProtocolVerified || input.Installation.Protocol == nil || input.Installation.Protocol.State != domain.ProtocolVerified || input.Installation.Protocol.Protocol != domain.CodexAppServer || !hasTitleCapability(machine.WorkerCapabilities) {
		return domain.Provider{}, false, nil
	}
	record, err := tx.Get(domain.ProviderKind, input.Configuration.ProviderID)
	if err != nil {
		return domain.Provider{}, false, nil
	}
	provider, err := store.Decode[domain.Provider](record)
	if err != nil || provider.Protocol != domain.OpenAIResponses {
		return domain.Provider{}, false, nil
	}
	return provider, true, nil
}

func hasTitleCapability(values []domain.WorkerCapability) bool {
	for _, value := range values {
		if value == domain.AutomaticTitlesCodexV1 {
			return true
		}
	}
	return false
}

// queueAutomaticSessionTitle runs inside the verified terminal transaction.
// The durable operation identity is retained even when the frozen profile is
// unsupported, so reconnects and later configuration changes cannot reroute it.
func queueAutomaticSessionTitle(tx *store.Tx, sr store.Record, session *domain.Session, sourceRecord store.Record, input domain.ExecutionJobInput) error {
	if session.NameOwner != domain.AutomaticNameOwner || session.NameMode != domain.AutomaticSessionName || session.TitleState != domain.TitleWaiting || session.TitleOperationID != "" || session.InitialExecution == nil {
		return nil
	}
	session.TitleOperationID = domain.NewID()
	sourceJob, err := store.Decode[domain.Job](sourceRecord)
	if err != nil || sourceJob.Type != domain.ExecuteSessionJob || sourceJob.State != domain.JobClaimed || sourceJob.MachineID != input.MachineID || sourceJob.AssignedDeviceID.Validate() != nil || sourceJob.InstanceID.Validate() != nil {
		session.TitleState, session.TitleReason = domain.TitleFailed, domain.TitleReasonInvalidOutput
		return nil
	}
	firstRecord, err := tx.Get(domain.QueueKind, session.InitialExecution.InputID)
	if err != nil || firstRecord.SessionID != sr.ID {
		session.TitleState, session.TitleReason = domain.TitleFailed, domain.TitleReasonInvalidOutput
		return nil
	}
	firstInput, err := store.Decode[domain.QueuedInput](firstRecord)
	if err != nil || firstInput.Sequence == 0 || domain.Text(firstInput.Prompt, "first session input", domain.MaxPromptBytes, true) != nil {
		session.TitleState, session.TitleReason = domain.TitleFailed, domain.TitleReasonInvalidOutput
		return nil
	}
	provider, available, err := titleProfileAvailable(tx, input)
	if err != nil {
		return err
	}
	if !available {
		session.TitleState = domain.TitleUnsupported
		session.TitleReason = domain.TitleReasonUnsupportedAgent
		if input.Configuration.Harness == domain.Codex && input.Installation.Version == domain.CodexProtocolVersion {
			session.TitleReason = domain.TitleReasonCapabilityAbsent
		}
		return nil
	}
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		if domain.SafeError(err).Code == domain.ResourceExhausted {
			session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonBudgetReached
			return nil
		}
		return err
	}
	if session.ProjectID != "" {
		project, err := tx.ExecutionProjectPolicy(*session)
		if err != nil || !project.Agents.Allows(input.Configuration.AgentID) || !project.Accounts.Allows(input.AccountID) {
			session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonAuthorityLost
			return nil
		}
	}
	_, account, err := accountFromTx(tx, input.AccountID, 0)
	if err != nil || !account.Enabled || account.Type != domain.APIAccount || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.ProviderID != input.Configuration.ProviderID || account.Connection.Authentication != provider.Authentication {
		session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonAuthorityLost
		return nil
	}
	if domain.Text(input.Installation.ResolvedPath, "native executable", 4096, true) != nil {
		session.TitleState, session.TitleReason = domain.TitleUnsupported, domain.TitleReasonUnsupportedAgent
		return nil
	}
	titleInput := domain.AuxiliaryTitleInput{
		Version: 1, SessionID: sr.ID, OperationID: session.TitleOperationID, NameGeneration: session.NameGeneration,
		OriginalJobID: sourceRecord.ID, OriginalExecutionID: input.ExecutionID, MachineID: input.MachineID,
		OriginalDeviceID: sourceJob.AssignedDeviceID, OriginalInstanceID: sourceJob.InstanceID,
		ProjectID: session.ProjectID,
		AgentID:   input.Configuration.AgentID, Harness: input.Configuration.Harness, NativeVersion: input.Installation.Version,
		Executable: input.Installation.ResolvedPath, AccountID: input.AccountID, ConnectionID: input.ConnectionID,
		ProviderID: input.Configuration.ProviderID, ProviderProtocol: provider.Protocol, ModelID: input.Configuration.ModelID,
		NativeModel: input.Configuration.NativeModel, Effort: input.Configuration.Effort,
		ServiceTier: input.Configuration.Options.ServiceTier, Prompt: firstInput.Prompt,
	}
	if err := titleInput.Validate(); err != nil {
		session.TitleState, session.TitleReason = domain.TitleUnsupported, domain.TitleReasonUnsupportedAgent
		return nil
	}
	raw, err := json.Marshal(titleInput)
	if err != nil || len(raw) > 1<<20 {
		return domain.Fail(domain.ResourceExhausted, "The immutable title assignment exceeds its storage bound.", "Retain the placeholder and the original conversation result.")
	}
	jobID := domain.NewID()
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobQueued, MachineID: input.MachineID, ParentID: sourceRecord.ID, Input: raw, AcceptedAt: time.Now().UTC()}
	if _, err := tx.PutJob(jobID, 0, sr.ID, sr.ProjectID, job); err != nil {
		return err
	}
	session.TitleJobID, session.TitleState, session.TitleReason = jobID, domain.TitleQueued, domain.TitleReasonNone
	return nil
}

func titleFailureReason(err *domain.Error) domain.SessionTitleReason {
	if err == nil {
		return domain.TitleReasonInferenceFailed
	}
	switch err.Code {
	case domain.Canceled:
		return domain.TitleReasonCanceled
	case domain.Unsupported:
		return domain.TitleReasonUnsupportedAgent
	case domain.Unauthenticated, domain.PermissionDenied:
		return domain.TitleReasonAuthorityLost
	case domain.RecoveryRequired:
		return domain.TitleReasonCleanupUncertain
	case domain.InvalidArgument:
		return domain.TitleReasonInvalidOutput
	default:
		return domain.TitleReasonInferenceFailed
	}
}

func requireSessionTitleRelayProof(tx *store.Tx, jobID domain.ID) error {
	sendClaimed, err := tx.TitleInferenceClaimed(jobID)
	if err != nil {
		return err
	}
	requestClaimed, err := tx.TitleHTTPRequestClaimed(jobID)
	if err != nil {
		return err
	}
	if !sendClaimed || !requestClaimed {
		return domain.Fail(domain.PermissionDenied, "The successful title report has no durable native relay proof.", "Accept title output only after the assigned Worker registered and sent one relayed request.")
	}
	return nil
}

func titleFailureOutcome(err *domain.Error) (domain.SessionTitleState, domain.SessionTitleReason) {
	state, reason := domain.TitleFailed, titleFailureReason(err)
	if err == nil {
		return state, reason
	}
	switch err.Code {
	case domain.Canceled, domain.Unauthenticated, domain.PermissionDenied:
		state = domain.TitleSkipped
	case domain.Unsupported:
		state = domain.TitleUnsupported
	case domain.RecoveryRequired:
		state = domain.TitleUncertain
	}
	return state, reason
}

func validateSessionTitleOutput(output json.RawMessage, input domain.AuxiliaryTitleInput, projectID domain.ID) (domain.AuxiliaryTitleResult, error) {
	var result domain.AuxiliaryTitleResult
	if domain.Decode(output, &result) != nil || result.Validate(input) != nil || (result.UsageRecord != nil && result.UsageRecord.ProjectID != projectID) {
		return domain.AuxiliaryTitleResult{}, domain.Fail(domain.InvalidArgument, "The Worker title result failed its bounded output and usage checks.", "Keep the placeholder; do not repair, truncate, or replay the native inference.")
	}
	return result, nil
}

func finishLostSessionTitle(tx *store.Tx, record store.Record) error {
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return err
	}
	if session.TitleJobID != record.ID || session.TitleOperationID == "" {
		return nil
	}
	if session.TitleState == domain.TitleRunning || session.TitleState == domain.TitleQueued {
		session.TitleState, session.TitleReason = domain.TitleUncertain, domain.TitleReasonCleanupUncertain
	}
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}

func finishSessionTitle(tx *store.Tx, record store.Record, job domain.Job, expectedRevision uint64, output json.RawMessage, reported *domain.Error) (store.Record, error) {
	var input domain.AuxiliaryTitleInput
	if job.Type != domain.GenerateSessionTitleJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.SessionID != record.SessionID || input.MachineID != job.MachineID || input.OriginalJobID != job.ParentID {
		return store.Record{}, domain.Fail(domain.RecoveryRequired, "The auxiliary title assignment does not match its original execution.", "Preserve the job and inspect its immutable parent before retrying.")
	}
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return store.Record{}, err
	}
	if session.TitleJobID != record.ID || session.TitleOperationID != input.OperationID {
		return store.Record{}, domain.Fail(domain.RecoveryRequired, "The title operation no longer owns this session job.", "Retain the result for reconciliation without changing the current session name.")
	}
	if reported == nil {
		if err := requireSessionTitleRelayProof(tx, record.ID); err != nil {
			return store.Record{}, err
		}
	}
	var result domain.AuxiliaryTitleResult
	if reported == nil {
		if result, err = validateSessionTitleOutput(output, input, session.ProjectID); err != nil {
			reported = domain.SafeError(err)
		}
	}
	if reported == nil && result.UsageRecord != nil {
		if _, _, err := tx.PutResponseUsage(domain.NewID(), *result.UsageRecord); err != nil {
			return store.Record{}, err
		}
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	if reported != nil {
		job.Problem = reported
		job.Output = nil
		switch reported.Code {
		case domain.Canceled:
			job.State = domain.JobCanceled
		case domain.RecoveryRequired:
			job.State = domain.JobUncertain
		default:
			job.State = domain.JobFailed
		}
		if session.NameOwner == domain.AutomaticNameOwner && session.NameGeneration == input.NameGeneration {
			session.TitleState, session.TitleReason = titleFailureOutcome(reported)
		}
	} else {
		job.State, job.Output, job.Problem = domain.JobSucceeded, output, nil
		if session.NameOwner == domain.AutomaticNameOwner && session.NameGeneration == input.NameGeneration && session.NameMode == domain.AutomaticSessionName {
			session.Name, session.TitleState, session.TitleReason = result.Title, domain.TitleSucceeded, domain.TitleReasonNone
		} else if session.TitleState != domain.TitleSkipped {
			session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonManualRename
		}
	}
	saved, err := tx.PutJob(record.ID, expectedRevision, record.SessionID, record.ProjectID, job)
	if err != nil {
		return store.Record{}, err
	}
	if session.Archive == domain.ArchivePending && session.ActiveExecutionID == "" && session.Recovery == domain.NoRecovery {
		stopped, stopErr := stopSessionWorkspace(tx, &session)
		if stopErr != nil {
			return store.Record{}, stopErr
		}
		if stopped && session.Recovery == domain.NoRecovery {
			session.Archive = domain.Archived
		}
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	return saved, nil
}
