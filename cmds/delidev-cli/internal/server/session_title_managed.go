// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// managedTitlePhase selects how much current session state a managed title
// assignment must still prove. Credential custody never depends on a session
// remaining visible, so Finish validates only immutable identity evidence.
type managedTitlePhase string

const (
	// managedTitleAdmission runs before the auxiliary lane claims a queued job.
	managedTitleAdmission managedTitlePhase = "admission"
	// managedTitleTake runs before protected credentials are delivered.
	managedTitleTake managedTitlePhase = "take"
	// managedTitleCustody runs after the lease commits in Take and during
	// protected Finish, before vault staging and receipt publication. It checks
	// structural lineage only: a rename, Stop, Archive or Worker capability
	// change that races inference must still allow the refreshed bundle to be
	// staged, and is applied or discarded at publication instead.
	managedTitleCustody managedTitlePhase = "custody"
)

func managedTitleDenied() *domain.Error {
	return domain.Fail(domain.PermissionDenied, "The managed automatic title no longer matches its original authority.", "Keep the placeholder; no account, model, Worker or credential generation may be substituted.")
}

func managedTitleCapabilityAbsent() *domain.Error {
	return domain.Fail(domain.Unsupported, "The original Worker no longer proves the managed title profile.", "Keep the title unsupported; no fallback harness or authentication is allowed.")
}

// managedTitleAuthority is the validated, decoded evidence for one managed
// title job. It is derived only from retained server state, never from a Worker
// report.
type managedTitleAuthority struct {
	Input         domain.AuxiliaryTitleInput
	Original      domain.ExecutionJobInput
	Session       domain.Session
	SessionRecord store.Record
	Account       domain.Account
	AccountRecord store.Record
}

// managedTitleProfileAvailable reports whether a successful subscription
// execution can receive a managed title assignment. A non-ChatGPT service or an
// execution without direct startup evidence is an unsupported profile; only a
// Worker lacking the negotiated adapter reports a missing capability.
func managedTitleProfileAvailable(tx *store.Tx, input domain.ExecutionJobInput) (bool, domain.SessionTitleReason, error) {
	configuration := input.Configuration
	if configuration.Harness != domain.Codex || !configuration.Subscription || configuration.SubscriptionService != domain.SubscriptionChatGPT || input.Version != 4 || input.Startup == nil {
		return false, domain.TitleReasonUnsupportedAgent, nil
	}
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return false, domain.TitleReasonNone, err
	}
	if !domain.ManagedTitlesSupported(machine.WorkerCapabilities) {
		return false, domain.TitleReasonCapabilityAbsent, nil
	}
	return true, domain.TitleReasonNone, nil
}

// settledManagedTitleAccount returns the account only when the first turn's
// protected Finish has settled: no lease, no pending operation and no recovery
// fence. Titles never queue against uncertain ownership.
func settledManagedTitleAccount(tx *store.Tx, input domain.ExecutionJobInput) (store.Record, domain.Account, bool, error) {
	record, account, err := subscriptionAccount(tx, input.AccountID, 0)
	if err != nil {
		return record, account, false, nil
	}
	state := account.Subscription
	settled := state != nil && state.Lease == nil && !state.RecoveryRequired && state.Pending == nil && state.Generation != "" && state.IdentityCommitment != "" &&
		account.Enabled && account.Health == domain.AccountReady && account.Removal == nil && !account.ConfirmedExhausted && account.Connection != nil && account.Connection.ID == input.ConnectionID
	return record, account, settled, nil
}

// managedTitleActorValid proves the session creator is still authorized. The
// local owner has no device row and cannot be revoked as a device.
func managedTitleActorValid(tx *store.Tx, actorDevice domain.ID) error {
	if actorDevice == "" {
		return nil
	}
	record, err := tx.Get(domain.DeviceKind, actorDevice)
	if err != nil {
		return managedTitleDenied()
	}
	device, err := store.Decode[domain.Device](record)
	if err != nil || device.Revoked || device.Type == domain.WorkerDevice {
		return managedTitleDenied()
	}
	return nil
}

// validateManagedTitle verifies one immutable managed title assignment against
// current server state. Every stricter-than-custody check is skipped for the
// custody phase. Lookup failures that mean missing state collapse into a typed
// denial; storage and budget failures keep their own codes.
func (s *Service) validateManagedTitle(tx *store.Tx, record store.Record, job domain.Job, phase managedTitlePhase) (managedTitleAuthority, error) {
	var result managedTitleAuthority
	collapse := func(err error) error {
		if err == nil {
			return nil
		}
		if code := domain.SafeError(err).Code; code == domain.NotFound || code == domain.PermissionDenied || code == domain.Conflict || code == domain.InvalidArgument {
			return managedTitleDenied()
		}
		return err
	}
	var input domain.AuxiliaryTitleInput
	if job.Type != domain.GenerateSessionTitleJob || domain.Decode(job.Input, &input) != nil || !input.IsManaged() || input.Validate() != nil || input.SessionID != record.SessionID || input.ProjectID != record.ProjectID || input.MachineID != job.MachineID || input.OriginalJobID != job.ParentID {
		return result, managedTitleDenied()
	}
	result.Input = input
	if phase == managedTitleAdmission && job.State != domain.JobQueued || phase != managedTitleAdmission && job.State != domain.JobClaimed {
		return result, managedTitleDenied()
	}
	if phase != managedTitleAdmission && (job.InstanceID != input.OriginalInstanceID || job.AssignedDeviceID != input.OriginalDeviceID) {
		return result, managedTitleDenied()
	}
	parentRecord, err := tx.Get(domain.JobKind, job.ParentID)
	if err != nil || parentRecord.SessionID != record.SessionID {
		return result, managedTitleDenied()
	}
	parent, err := store.Decode[domain.Job](parentRecord)
	var original domain.ExecutionJobInput
	if err != nil || parent.Type != domain.ExecuteSessionJob || parent.State != domain.JobSucceeded || parent.MachineID != input.MachineID || parent.InstanceID != input.OriginalInstanceID || parent.AssignedDeviceID != input.OriginalDeviceID || domain.Decode(parent.Input, &original) != nil || original.Validate() != nil {
		return result, managedTitleDenied()
	}
	configuration := original.Configuration
	if original.Version != 4 || original.Startup == nil || original.ExecutionID != input.OriginalExecutionID || configuration.AgentID != input.AgentID || configuration.Harness != input.Harness || original.AccountID != input.AccountID || original.ConnectionID != input.ConnectionID || configuration.ModelID != input.ModelID || configuration.NativeModel != input.NativeModel || configuration.Effort != input.Effort || configuration.Options.ServiceTier != input.ServiceTier || !configuration.Subscription || configuration.SubscriptionService != domain.SubscriptionChatGPT || configuration.ProviderID != "" {
		return result, managedTitleDenied()
	}
	ready := parent.Startup
	if ready == nil || ready.Ready == nil || ready.Failure != nil || ready.Ready.Validate() != nil || input.Startup == nil || input.Startup.ExecutableSHA256 != ready.Ready.ExecutableSHA256 || input.Startup.ExplicitPath != original.Startup.ExplicitPath || input.NativeVersion != ready.Ready.NativeVersion {
		return result, managedTitleDenied()
	}
	var completion domain.ExecutionCompletion
	if domain.Decode(parent.Output, &completion) != nil || completion.ValidateForHarness(domain.Codex) != nil || completion.Outcome != domain.ExecutionSucceeded || completion.ExecutionID != original.ExecutionID {
		return result, managedTitleDenied()
	}
	result.Original = original
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return result, collapse(err)
	}
	// The current capability set gates only new authority: admission and Take.
	// Once the exact lease exists, the Worker may reattach with a different
	// capability set, and custody must still publish its confirmed cleanup and
	// refreshed bundle from the immutable lease lineage.
	if phase != managedTitleCustody && !domain.ManagedTitlesSupported(machine.WorkerCapabilities) {
		return result, managedTitleCapabilityAbsent()
	}
	accountRecord, account, err := subscriptionAccount(tx, input.AccountID, 0)
	if err != nil {
		return result, managedTitleDenied()
	}
	state := account.Subscription
	// A recovery fence on the exact frozen generation and identity is
	// reconcilable, so admission keeps the title queued behind it instead of
	// retiring it. Take and custody still refuse a fenced account.
	fenceAdmitted := phase == managedTitleAdmission && state != nil && state.RecoveryRequired
	if state == nil || state.RecoveryRequired && !fenceAdmitted || account.Connection == nil || account.Connection.ID != input.ConnectionID || state.Generation != input.Managed.Generation || state.IdentityCommitment != input.Managed.IdentityCommitment {
		return result, managedTitleDenied()
	}
	result.Account, result.AccountRecord = account, accountRecord
	if phase == managedTitleCustody {
		return result, nil
	}
	// Recording the fence marks the account failed; that health is the fence
	// itself, not a separate loss of authority.
	if !account.Enabled || account.Health != domain.AccountReady && !(fenceAdmitted && account.Health == domain.AccountFailed) || account.Removal != nil || account.ConfirmedExhausted {
		return result, managedTitleDenied()
	}
	if s.executionAuthority == nil {
		return result, managedTitleDenied()
	}
	originalGrant, err := tx.ExecutionGrantForJob(job.ParentID)
	if err != nil || originalGrant.MachineID != input.MachineID || originalGrant.InstanceID != input.OriginalInstanceID || originalGrant.DeviceID != input.OriginalDeviceID || originalGrant.ServerEpoch != s.executionAuthority.epoch {
		return result, managedTitleDenied()
	}
	sessionRecord, session, err := sessionRecord(tx, input.SessionID)
	if err != nil {
		return result, collapse(err)
	}
	expectedState := domain.TitleRunning
	if phase == managedTitleAdmission {
		expectedState = domain.TitleQueued
	}
	if session.NameMode != domain.AutomaticSessionName || session.NameOwner != domain.AutomaticNameOwner || session.NameGeneration != input.NameGeneration || session.TitleOperationID != input.OperationID || session.TitleJobID != record.ID || session.TitleState != expectedState || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || !matchesInitialTitleExecution(session, original) {
		return result, managedTitleDenied()
	}
	result.Session, result.SessionRecord = session, sessionRecord
	firstRecord, err := tx.Get(domain.QueueKind, session.InitialExecution.InputID)
	if err != nil || firstRecord.SessionID != sessionRecord.ID {
		return result, managedTitleDenied()
	}
	firstInput, err := store.Decode[domain.QueuedInput](firstRecord)
	if err != nil || firstInput.Prompt != input.Prompt {
		return result, managedTitleDenied()
	}
	if err := tx.RequireSessionBudget(sessionRecord.ID, session.EstimatedCostBudget); err != nil {
		return result, err
	}
	if err := tx.RequireExecutionAgent(session, input.AgentID); err != nil {
		return result, managedTitleDenied()
	}
	if session.ProjectID != "" {
		project, err := tx.ExecutionProjectPolicy(session)
		if err != nil || !project.Agents.Allows(input.AgentID) || !project.Accounts.Allows(input.AccountID) {
			return result, managedTitleDenied()
		}
	}
	if err := managedTitleActorValid(tx, input.Managed.ActorDeviceID); err != nil {
		return result, err
	}
	if phase == managedTitleTake {
		canceled, err := tx.JobCancellationRequested(record.ID)
		if err != nil || canceled {
			return result, managedTitleDenied()
		}
		instance, seen, err := tx.WorkerInstance(input.MachineID)
		if err != nil || instance != input.OriginalInstanceID || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
			return result, managedTitleDenied()
		}
	}
	return result, nil
}

// managedTitleLease reloads the exact claimed title job named by an EXECUTE
// lease and revalidates it. A lease and its title job revision are inseparable.
func (s *Service) managedTitleLease(tx *store.Tx, lease domain.SubscriptionLease, phase managedTitlePhase) (managedTitleAuthority, store.Record, error) {
	record, err := tx.Get(domain.JobKind, lease.OperationID)
	if err != nil {
		return managedTitleAuthority{}, record, err
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil || lease.TitleRevision == 0 || record.Revision != lease.TitleRevision || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
		return managedTitleAuthority{}, record, managedTitleDenied()
	}
	authority, err := s.validateManagedTitle(tx, record, job, phase)
	if err == nil && authority.Input.Managed.Generation != lease.Generation {
		err = managedTitleDenied()
	}
	return authority, record, err
}

// accountHasPrimaryWork reports whether any conversation, Fork, compaction or
// review job on the account is queued or claimed. Such eligible primary work has
// admission priority, and a claimed sibling title keeps the account serialized.
func accountHasPrimaryWork(tx *store.Tx, account domain.ID, except domain.ID) (bool, error) {
	for _, state := range []domain.JobState{domain.JobQueued, domain.JobClaimed} {
		var after domain.ID
		for {
			records, err := tx.Jobs("", "", state, after, store.MaxPage)
			if err != nil {
				return false, err
			}
			for _, record := range records {
				after = record.ID
				if record.ID == except {
					continue
				}
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return false, err
				}
				var owner domain.ID
				switch job.Type {
				case domain.ExecuteSessionJob:
					var execution domain.ExecutionJobInput
					if domain.Decode(job.Input, &execution) == nil {
						owner = execution.AccountID
					}
				case domain.ForkSessionJob:
					var fork domain.ForkJobInput
					if domain.Decode(job.Input, &fork) == nil {
						owner = fork.SourceAssignment.AccountID
					}
				case domain.CompactSessionJob:
					var compact domain.SessionCompactionInput
					if domain.DecodeCompactionInput(job.Input, &compact) == nil {
						owner = compact.Assignment.AccountID
					}
				case domain.NativeCodeReviewJob:
					var review domain.NativeCodeReviewInput
					if domain.DecodeNativeCodeReviewInput(job.Input, &review) == nil {
						owner = review.Source.AccountID
					}
				case domain.GenerateSessionTitleJob:
					// Only a claimed managed sibling occupies the account; queued titles
					// compete on equal footing through serialized admission.
					var title domain.AuxiliaryTitleInput
					if state == domain.JobClaimed && domain.Decode(job.Input, &title) == nil && title.IsManaged() {
						owner = title.AccountID
					}
				}
				if owner == account {
					return true, nil
				}
			}
			if len(records) < store.MaxPage {
				break
			}
		}
	}
	return false, nil
}

// managedTitleAccountBusy reports a transient reason the title must stay queued,
// including a reconcilable recovery fence.
func managedTitleAccountBusy(tx *store.Tx, accountID domain.ID, account domain.Account, title store.Record) (bool, error) {
	state := account.Subscription
	if state == nil || state.Lease != nil || state.Pending != nil || state.RecoveryRequired || state.ExclusiveServerObservationActive() || state.ServerOperation != nil && state.ServerOperation.NativeStarted || state.Observation != nil && state.Observation.Active() {
		return true, nil
	}
	return accountHasPrimaryWork(tx, accountID, title.ID)
}

// managedTitleReservation reports whether a claimed managed title still holds
// the custody window of an account: its auxiliary admission marked it running,
// but its own TakeSubscription has not yet committed the one-time inference
// claim together with the account lease. The account has no lease in that
// window, so every other account owner (primary Take, refresh, quota or
// reset-credit observation) must treat it as exclusively reserved; otherwise a
// successful Finish could advance the frozen credential generation and turn an
// already running title into skipped/authority-lost.
//
// The reservation lapses as soon as the title can no longer Take: a committed
// inference claim (the lease or the immutable receipt carries custody from
// there), a requested cancellation, or a Worker process that is no longer the
// live claiming instance. Revocation and instance replacement also settle the
// job itself, so a dead Worker never stalls the account.
func managedTitleReservation(tx *store.Tx, account domain.ID, except domain.ID) (bool, error) {
	var after domain.ID
	for {
		records, err := tx.ClaimedManagedTitleJobs(account, after, store.MaxPage)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			after = record.ID
			if record.ID == except {
				continue
			}
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return false, err
			}
			var input domain.AuxiliaryTitleInput
			if job.Type != domain.GenerateSessionTitleJob || job.State != domain.JobClaimed || domain.Decode(job.Input, &input) != nil || !input.IsManaged() || input.AccountID != account {
				continue
			}
			if claimed, err := tx.TitleInferenceClaimed(record.ID); err != nil || claimed {
				if err != nil {
					return false, err
				}
				continue
			}
			if canceled, err := tx.JobCancellationRequested(record.ID); err != nil || canceled {
				if err != nil {
					return false, err
				}
				continue
			}
			instance, seen, err := tx.WorkerInstance(job.MachineID)
			if err != nil {
				return false, err
			}
			if instance == "" || instance != job.InstanceID || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
				continue
			}
			return true, nil
		}
		if len(records) < store.MaxPage {
			return false, nil
		}
	}
}

// managedTitleAccountReserved is the typed transient refusal returned to an
// account owner that raced a claimed managed title. It is a definite no-effect
// busy result: the Worker waits on the same exact account and retries.
func managedTitleAccountReserved() *domain.Error {
	return domain.Fail(domain.ResourceExhausted, "The selected account is reserved by a running automatic title.", "Wait for the running title to take and finish its original custody; a running title is never preempted and no account is switched.")
}

// queueManagedSessionTitle freezes the version-3 assignment for a protected
// ChatGPT subscription session. It runs in the verified terminal transaction of
// the first successful turn, after that turn's protected Finish settled the
// account. It selects no account, model, Worker or executable, creates no
// conversation input and never claims the conversation workspace lease.
func queueManagedSessionTitle(tx *store.Tx, sr store.Record, session *domain.Session, sourceRecord store.Record, sourceJob domain.Job, input domain.ExecutionJobInput, firstInput domain.QueuedInput) error {
	_, account, settled, err := settledManagedTitleAccount(tx, input)
	if err != nil {
		return err
	}
	if !settled || account.Type != domain.SubscriptionAccount || account.SubscriptionService != domain.SubscriptionChatGPT || account.ProviderID != "" {
		session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonAuthorityLost
		return nil
	}
	if session.Startup == nil || session.Startup.JobID != sourceRecord.ID || session.Startup.Ready == nil || input.Startup == nil {
		session.TitleState, session.TitleReason = domain.TitleUnsupported, domain.TitleReasonUnsupportedAgent
		return nil
	}
	if err := managedTitleActorValid(tx, session.CreatedBy); err != nil {
		session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonAuthorityLost
		return nil
	}
	titleInput := domain.AuxiliaryTitleInput{
		Version: 3, SessionID: sr.ID, OperationID: session.TitleOperationID, NameGeneration: session.NameGeneration,
		OriginalJobID: sourceRecord.ID, OriginalExecutionID: input.ExecutionID, MachineID: input.MachineID,
		OriginalDeviceID: sourceJob.AssignedDeviceID, OriginalInstanceID: sourceJob.InstanceID,
		ProjectID: session.ProjectID,
		AgentID:   input.Configuration.AgentID, Harness: input.Configuration.Harness, NativeVersion: session.Startup.Ready.NativeVersion,
		AccountID: input.AccountID, ConnectionID: input.ConnectionID, ModelID: input.Configuration.ModelID,
		NativeModel: input.Configuration.NativeModel, Effort: input.Configuration.Effort,
		ServiceTier: input.Configuration.Options.ServiceTier, Prompt: firstInput.Prompt,
		Startup: &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: input.Startup.ExplicitPath, ExecutableSHA256: session.Startup.Ready.ExecutableSHA256},
		Managed: &domain.ManagedTitleAuthority{ActorDeviceID: session.CreatedBy, SubscriptionService: domain.SubscriptionChatGPT, Generation: account.Subscription.Generation, IdentityCommitment: account.Subscription.IdentityCommitment},
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

// managedTitleResult validates a managed success report against the protected
// Finish receipt. It returns a typed problem for the caller to settle instead of
// an error, so an unconfirmed receipt records an uncertain title without
// applying text. A valid result stays available for usage attribution even when
// publication is discarded because the title operation was canceled.
func managedTitleResult(tx *store.Tx, record store.Record, job domain.Job, input domain.AuxiliaryTitleInput, output json.RawMessage, reported *domain.Error) (domain.AuxiliaryTitleResult, *domain.Error) {
	var result domain.AuxiliaryTitleResult
	if reported != nil {
		return result, reported
	}
	unconfirmed := domain.Fail(domain.RecoveryRequired, "The protected title authentication finish is unconfirmed.", "Retain the original title lease and cleanup evidence; do not repeat title inference.")
	if domain.Decode(output, &result) != nil || result.Validate(input) != nil {
		return domain.AuxiliaryTitleResult{}, domain.Fail(domain.InvalidArgument, "The Worker title result failed its bounded output and usage checks.", "Keep the placeholder; do not repair, truncate, or replay the native inference.")
	}
	claimed, err := tx.TitleInferenceClaimed(record.ID)
	if err != nil || !claimed {
		return domain.AuxiliaryTitleResult{}, unconfirmed
	}
	proof, err := tx.ManagedTitleFinish(result.ManagedFinish)
	if err != nil || proof.Job != record.ID || proof.JobRevision != record.Revision || proof.Account != input.AccountID || proof.Machine != job.MachineID || proof.Instance != job.InstanceID || proof.Device != job.AssignedDeviceID || proof.Generation != input.Managed.Generation {
		return domain.AuxiliaryTitleResult{}, unconfirmed
	}
	// The receipt is immutable and bound to the exact job revision, Worker
	// instance, device and settled generation, so it already proves the Finish.
	// The account is checked only for its frozen connection: after the Finish
	// releases it, a queued primary job may legitimately lease it or advance its
	// generation before this report arrives, and that must not turn a proven
	// title into permanent uncertainty.
	_, account, err := subscriptionAccount(tx, proof.Account, 0)
	if err != nil || account.Subscription == nil || account.Connection == nil || account.Connection.ID != input.ConnectionID {
		return domain.AuxiliaryTitleResult{}, unconfirmed
	}
	if canceled, err := tx.JobCancellationRequested(record.ID); err != nil || canceled {
		return result, domain.Fail(domain.Canceled, "The automatic title was canceled before publication.", "Keep the current session title; the credential finish is already settled.")
	}
	// Revoking a client device does not cancel title jobs, so the frozen creator
	// must still be authorized at publication. The valid result keeps its usage
	// attribution, but its text is discarded.
	if managedTitleActorValid(tx, input.Managed.ActorDeviceID) != nil {
		return result, domain.Fail(domain.PermissionDenied, "The session creator lost authority before the automatic title was published.", "Keep the current session title; the credential finish is already settled.")
	}
	return result, nil
}
