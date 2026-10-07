package server

import (
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func matchesInitialTitleExecution(session domain.Session, original domain.ExecutionJobInput) bool {
	initial := session.InitialExecution
	return initial != nil && original.Continuation == nil && session.MachineID == original.MachineID && session.AgentID == original.Configuration.AgentID && session.NativeExecutionRoot() == original.ExecutionID && (initial.InputID == original.InputID || original.Retry != nil) && initial.InitialAccountID == original.AccountID && initial.ConnectionID == original.ConnectionID && initial.ConfigurationDigest == original.ConfigurationDigest
}

func (a *executionAuthority) titleScope(tx *store.Tx, grant store.ExecutionGrant, record store.Record, job domain.Job) (apiproxy.Scope, error) {
	var empty apiproxy.Scope
	denied := func() (apiproxy.Scope, error) { return empty, executionDenied() }
	var input domain.AuxiliaryTitleInput
	if job.State != domain.JobClaimed || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.SessionID != record.SessionID || input.ProjectID != record.ProjectID || input.MachineID != grant.MachineID || input.OriginalDeviceID != grant.DeviceID || input.OriginalJobID != job.ParentID || input.OriginalExecutionID != grant.ExecutionID {
		return denied()
	}
	claimed, err := tx.TitleInferenceClaimed(record.ID)
	if err != nil || !claimed {
		return denied()
	}
	canceled, err := tx.JobCancellationRequested(record.ID)
	if err != nil || canceled {
		return denied()
	}
	instance, seen, err := tx.WorkerInstance(grant.MachineID)
	if err != nil || instance != grant.InstanceID || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
		return denied()
	}
	deviceRecord, err := tx.Get(domain.DeviceKind, grant.DeviceID)
	if err != nil {
		return denied()
	}
	device, err := store.Decode[domain.Device](deviceRecord)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != grant.MachineID {
		return denied()
	}
	_, machine, err := activeMachine(tx, grant.MachineID)
	if err != nil || !hasTitleCapability(machine.WorkerCapabilities) {
		return denied()
	}
	parentRecord, err := tx.Get(domain.JobKind, job.ParentID)
	if err != nil || parentRecord.SessionID != record.SessionID {
		return denied()
	}
	parent, err := store.Decode[domain.Job](parentRecord)
	var original domain.ExecutionJobInput
	if err != nil || parent.Type != domain.ExecuteSessionJob || parent.State != domain.JobSucceeded || parent.MachineID != grant.MachineID || parent.InstanceID != input.OriginalInstanceID || parent.AssignedDeviceID != input.OriginalDeviceID || domain.Decode(parent.Input, &original) != nil || original.Validate() != nil || original.ExecutionID != input.OriginalExecutionID || original.Configuration.AgentID != input.AgentID || original.Configuration.Harness != input.Harness || (input.Version == 1 && (original.Installation.Version != input.NativeVersion || original.Installation.ResolvedPath != input.Executable)) || original.AccountID != input.AccountID || original.ConnectionID != input.ConnectionID || original.Configuration.ProviderID != input.ProviderID || original.Configuration.ModelID != input.ModelID || original.Configuration.NativeModel != input.NativeModel {
		return denied()
	}
	if input.Version == 2 && (original.Version != 4 || original.Startup == nil || parent.Startup == nil || parent.Startup.Ready == nil || parent.Startup.Failure != nil || parent.Startup.Ready.Validate() != nil || input.Startup == nil || input.Startup.ExecutableSHA256 != parent.Startup.Ready.ExecutableSHA256 || input.Startup.ExplicitPath != original.Startup.ExplicitPath || input.NativeVersion != parent.Startup.Ready.NativeVersion) {
		return denied()
	}
	originalGrant, err := tx.ExecutionGrantForJob(job.ParentID)
	if err != nil || originalGrant.MachineID != grant.MachineID || originalGrant.InstanceID != input.OriginalInstanceID || originalGrant.DeviceID != input.OriginalDeviceID || originalGrant.ServerEpoch != grant.ServerEpoch || grant.InstanceID != input.OriginalInstanceID {
		return denied()
	}
	var completion domain.ExecutionCompletion
	if domain.Decode(parent.Output, &completion) != nil || completion.ValidateForHarness(domain.Codex) != nil || completion.Outcome != domain.ExecutionSucceeded || completion.ExecutionID != original.ExecutionID {
		return denied()
	}
	sessionRecord, session, err := sessionRecord(tx, input.SessionID)
	if err != nil || session.NameMode != domain.AutomaticSessionName || session.NameOwner != domain.AutomaticNameOwner || session.NameGeneration != input.NameGeneration || session.TitleOperationID != input.OperationID || session.TitleJobID != record.ID || session.TitleState != domain.TitleRunning || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || !matchesInitialTitleExecution(session, original) {
		return denied()
	}
	firstRecord, err := tx.Get(domain.QueueKind, session.InitialExecution.InputID)
	if err != nil || firstRecord.SessionID != sessionRecord.ID {
		return denied()
	}
	firstInput, err := store.Decode[domain.QueuedInput](firstRecord)
	if err != nil || firstInput.Prompt != input.Prompt {
		return denied()
	}
	if tx.RequireSessionBudget(sessionRecord.ID, session.EstimatedCostBudget) != nil {
		return denied()
	}
	if err := tx.RequireExecutionAgent(session, input.AgentID); err != nil {
		return denied()
	}
	if session.ProjectID != "" {
		project, err := tx.ExecutionProjectPolicy(session)
		if err != nil || !project.Agents.Allows(input.AgentID) || !project.Accounts.Allows(input.AccountID) {
			return denied()
		}
	}
	_, account, err := accountFromTx(tx, input.AccountID, 0)
	if err != nil || !account.Enabled || account.Type != domain.APIAccount || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.ProviderID != input.ProviderID {
		return denied()
	}
	providerRecord, err := tx.Get(domain.ProviderKind, input.ProviderID)
	if err != nil {
		return denied()
	}
	provider, err := store.Decode[domain.Provider](providerRecord)
	if err == nil {
		provider, err = providers.ResolveAccountProfile(provider, account)
	}
	if err != nil || provider.Protocol != domain.OpenAIResponses || provider.Authentication != account.Connection.Authentication {
		return denied()
	}
	modelRecord, err := tx.Get(domain.ModelKind, input.ModelID)
	if err != nil {
		return denied()
	}
	model, err := store.Decode[domain.Model](modelRecord)
	if err != nil || model.ProviderID != input.ProviderID || model.NativeID != input.NativeModel || !slices.Contains(model.Harnesses, domain.Codex) {
		return denied()
	}
	scope := apiproxy.Scope{ExecutionID: input.OriginalExecutionID, SessionID: input.SessionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.ProviderID, ModelID: input.ModelID, NativeModel: input.NativeModel, Harness: domain.Codex, Purpose: domain.SessionTitleUsage, TitlePrompt: input.Prompt, Effort: input.Effort, ServiceTier: input.ServiceTier, Provider: provider, Operations: []apiproxy.Operation{apiproxy.ResponseCreate}}
	if err := scope.Validate(); err != nil {
		return denied()
	}
	return scope, nil
}
