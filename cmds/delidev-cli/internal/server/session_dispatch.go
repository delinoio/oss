package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func firstDispatchConflict() error {
	return domain.Fail(domain.Conflict, "The session is not eligible for its first execution.", "Wait for ready workspace ownership, retain queue order and explicitly resume a paused session.")
}

func requireSessionProviderEnabled(tx *store.Tx, session domain.Session) (domain.ID, error) {
	var providerID domain.ID
	var subscriptionService domain.SubscriptionService
	if session.InitialExecution != nil {
		providerID = session.InitialExecution.Configuration.ProviderID
		subscriptionService = session.InitialExecution.Configuration.SubscriptionService
	} else if session.Fork != nil {
		providerID = session.Fork.Snapshot.Configuration.ProviderID
		subscriptionService = session.Fork.Snapshot.Configuration.SubscriptionService
	} else {
		agentRecord, err := tx.Get(domain.AgentKind, session.AgentID)
		if err != nil {
			return "", err
		}
		agent, err := store.Decode[domain.Agent](agentRecord)
		if err != nil {
			return "", err
		}
		if len(agent.Routes) > 0 {
			var project *domain.Project
			if session.ProjectID != "" {
				record, err := tx.Get(domain.ProjectKind, session.ProjectID)
				if err != nil {
					return "", err
				}
				value, err := store.Decode[domain.Project](record)
				if err != nil {
					return "", err
				}
				project = &value
			}
			// The first-execution transaction resolves again with the real server policy.
			// This read guards provider authority without consuming routing state.
			policy, err := tx.DefaultRoutingPolicy()
			if err != nil {
				return "", err
			}
			preview, err := tx.PreviewSourceRouting(session.AgentID, agent, project, policy)
			if err != nil {
				return "", err
			}
			providerID, subscriptionService = preview.Model.ProviderID, preview.Model.SubscriptionService
		} else {
			modelRecord, err := tx.Get(domain.ModelKind, agent.ModelID)
			if err != nil {
				return "", err
			}
			model, err := store.Decode[domain.Model](modelRecord)
			if err != nil {
				return "", err
			}
			providerID, subscriptionService = model.ProviderID, model.SubscriptionService
		}
	}
	if subscriptionService.Valid() && providerID == "" {
		return "", nil
	}
	providerRecord, err := tx.Get(domain.ProviderKind, providerID)
	if err != nil {
		return providerID, err
	}
	provider, err := store.Decode[domain.Provider](providerRecord)
	if err != nil {
		return providerID, err
	}
	if provider.Protocol != domain.NativeSubscription && !provider.EnabledValue() {
		return providerID, providerDisabled()
	}
	return providerID, nil
}

// All callers must propagate failure out of their Store.Mutate callback. The
// transient ready state, snapshot/routing claim and job are one transaction;
// validating the selected configuration after the claim cannot partially commit.
func queueInitialExecution(tx *store.Tx, sr store.Record, session domain.Session, explicitResume bool) (store.Record, error) {
	if session.Fork != nil {
		return queueForkInitialExecution(tx, sr, session, explicitResume)
	}
	if session.InitialExecution != nil || session.CurrentExecution != nil || session.NextExecutionIntent != "" || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || (session.Dispatch != domain.DispatchBlocked && session.Dispatch != domain.DispatchReady && !(explicitResume && session.Dispatch == domain.DispatchPaused)) {
		return store.Record{}, firstDispatchConflict()
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
		return store.Record{}, domain.Fail(domain.Unavailable, "The selected Worker is not currently connected.", "Reconnect the original execution machine; the accepted input remains queued.")
	}
	ir, err := tx.OldestQueuedInput(sr.ID)
	if err != nil {
		return store.Record{}, err
	}
	session.Dispatch = domain.DispatchReady
	ready, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	if err != nil {
		return store.Record{}, err
	}
	claim, err := tx.ClaimInitialExecution(sr.ID, ready.Revision, ir.ID, ir.Revision)
	if err != nil {
		return store.Record{}, err
	}
	input, err := initialExecutionAssignment(tx, sr, session, machine, claim)
	if err != nil {
		return store.Record{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: session.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}

func initialExecutionAssignment(tx *store.Tx, sr store.Record, session domain.Session, machine domain.Machine, claim domain.InitialExecution) (domain.ExecutionJobInput, error) {
	ir, err := tx.Get(domain.QueueKind, claim.InputID)
	if err != nil {
		return domain.ExecutionJobInput{}, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return domain.ExecutionJobInput{}, err
	}
	return checkedExecutionAssignment(tx, sr, session, machine, domain.ExecutionJobInput{Version: 1, SessionID: sr.ID, MachineID: session.MachineID, ExecutionID: claim.ID, InputID: claim.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: queued.NativeRequestID, Input: domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}, Configuration: claim.Configuration, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID})
}

func checkedExecutionSelection(tx *store.Tx, session domain.Session, machine domain.Machine, input domain.ExecutionJobInput) (domain.Installation, error) {
	if !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "This Runner Device needs an update before execution.", "Update and reconnect the selected Runner Device to start the actual process directly.")
	}
	return checkedExecutionConfiguration(tx, session, machine, input)
}

func checkedExecutionConfiguration(tx *store.Tx, session domain.Session, machine domain.Machine, input domain.ExecutionJobInput) (domain.Installation, error) {
	var empty domain.Installation
	c := input.Configuration
	if err := c.ValidateNativeOptions(); err != nil {
		return empty, err
	}
	if err := requireSidechatParent(tx, session); err != nil {
		return empty, err
	}
	if c.SidechatPolicy != "" && (!session.IsSidechat() || !slices.Contains(machine.WorkerCapabilities, domain.CodexReadOnlySidechatWorkerV1)) {
		return empty, domain.SidechatUnavailable()
	}
	protocol := domain.OpenAIResponses
	switch c.Harness {
	case domain.Codex:
		if (c.Options.SubagentModel != "" || c.Options.SubagentEffort != "" || c.Options.MaxConcurrency != 0) && !slices.Contains(machine.WorkerCapabilities, domain.CodexSubagentConfigurationV1) {
			return empty, domain.Fail(domain.Unsupported, "The original Runner Device lacks Codex child configuration support.", "Update and reconnect that Runner Device; the saved input and native defaults remain unchanged.")
		}
	case domain.ClaudeCode:
		validGeneration := input.Fork == nil && (input.Continuation == nil || input.Continuation.Validate(input) == nil)
		if !validGeneration {
			return empty, domain.Fail(domain.Unsupported, "Claude continuation requires separately verified native history.", "Preserve the original input; no replacement execution is authorized.")
		}
		if _, err := c.ClaudeAPIInputPermission(input.Input.Mode); err != nil {
			return empty, err
		}
		protocol = domain.AnthropicMessages
	case domain.OpenCode:
		if input.Fork != nil && !slices.Contains(machine.WorkerCapabilities, domain.OpenCodeGeneralChatForkV1) {
			return empty, domain.Fail(domain.Unsupported, "The original Runner Device lacks OpenCode Fork support.", "Update and reconnect the original Unix Runner Device; the child stays paused.")
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.OpenCodeForegroundSubagentsV1) {
			return empty, domain.Fail(domain.Unsupported, "The original Runner Device lacks foreground child observation support.", "Update and reconnect that Runner Device before OpenCode execution.")
		}
		if _, err := c.OpenCodePrimaryForInput(input.Input.Mode); err != nil {
			return empty, err
		}
		protocol = domain.OpenAIChat
	case domain.GrokBuild:
		if input.Continuation != nil || input.Fork != nil {
			return empty, domain.Fail(domain.Unsupported, "Grok continuation requires separately verified native history.", "Preserve the original completed input without creating a replacement session.")
		}
		if _, err := c.GrokFirstInputContext(input.Input.Mode); err != nil {
			return empty, err
		}
		protocol = domain.OpenAIChat
	default:
		return empty, domain.Fail(domain.Unsupported, "This harness has no integrated execution profile yet.", "Select a supported installed profile; no harness fallback is performed.")
	}
	installation := domain.Installation{Harness: c.Harness}
	if input.Startup != nil {
		installation.ExplicitPath = input.Startup.ExplicitPath
	} else if input.Continuation != nil || input.Fork != nil {
		// Historical native ownership cannot adopt a later PATH selection.
		installation.ExplicitPath = input.Installation.ResolvedPath
	} else {
		for _, selected := range machine.Installations {
			if selected.Harness == c.Harness {
				if installation.ExplicitPath != "" {
					return empty, domain.Fail(domain.RecoveryRequired, "The executable selection is ambiguous.", "Correct the selected Runner Device's executable path.")
				}
				installation.ExplicitPath = selected.ExplicitPath
			}
		}
	}
	_, account, err := executionAccountFromTx(tx, input.AccountID, input.ConnectionID)
	if err != nil {
		return empty, err
	}
	managed := c.Subscription && c.SubscriptionService == domain.SubscriptionChatGPT && account.SubscriptionService == c.SubscriptionService && account.ProviderID == "" && c.ProviderID == "" && account.Type == domain.SubscriptionAccount && c.Harness == domain.Codex && account.Subscription != nil && account.Subscription.Generation != "" && !account.Subscription.RecoveryRequired && (account.Subscription.Pending == nil || account.Subscription.Pending.Action == domain.SubscriptionRefresh) && account.Connection != nil && account.Connection.Authentication == domain.SubscriptionAuth
	if managed && !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
		return empty, domain.Fail(domain.Unsupported, "The selected Runner Device has no managed Codex capability.", "Connect a Runner Device with a verified managed authentication profile before dispatching this account.")
	}
	apiReady := account.Type == domain.APIAccount && account.Validation != nil && account.Validation.ConnectionID == input.ConnectionID && account.Validation.State == domain.Observed && account.Validation.Problem == nil && !account.Validation.ObservedAt.IsZero() && account.Connection != nil && !account.Validation.ObservedAt.Before(account.Connection.ConnectedAt) && !account.Validation.ObservedAt.After(time.Now().UTC().Add(time.Second)) && (account.Validation.Authentication == domain.CredentialAccepted || account.Validation.Authentication == domain.KeylessEndpoint)
	if !account.Enabled || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.Health != domain.AccountReady || (!managed && !apiReady) {
		return empty, domain.Fail(domain.Unsupported, "The selected account lacks verified API execution authority.", "Connect and validate the selected API account; stored credentials or a model catalog alone do not authorize inference.")
	}
	if !managed {
		pr, err := tx.Get(domain.ProviderKind, c.ProviderID)
		if err != nil {
			return empty, err
		}
		provider, err := store.Decode[domain.Provider](pr)
		if err == nil {
			provider, err = providers.ResolveAccountProfile(provider, account)
		}
		if err != nil {
			return empty, err
		}
		if !provider.EnabledValue() {
			return empty, providerDisabled()
		}
		if provider.Protocol != protocol || (provider.Authentication == domain.KeylessAuth) != (account.Validation.Authentication == domain.KeylessEndpoint) || provider.Authentication != account.Connection.Authentication || account.ProviderID != c.ProviderID {
			return empty, domain.Fail(domain.Unsupported, "The selected provider protocol is incompatible with this native profile.", "Select the matching API protocol; no translation or subscription fallback is performed.")
		}
	}
	// Recheck current restrictions without resolving changed Agent/templates or
	// rerunning routing. Only the immutable selected account may continue.
	if err := tx.RequireExecutionAgent(session, c.AgentID); err != nil {
		return empty, err
	}
	mr, err := tx.Get(domain.ModelKind, c.ModelID)
	if err != nil {
		return empty, err
	}
	model, err := store.Decode[domain.Model](mr)
	if err != nil {
		return empty, err
	}
	if model.ProviderID != c.ProviderID || model.SubscriptionService != c.SubscriptionService || !model.MatchesAccount(account, c.Harness) || !slices.Contains(model.Harnesses, c.Harness) {
		return empty, domain.Fail(domain.Unsupported, "The selected model no longer supports this execution profile.", "Restore compatibility without replacing the original session snapshot.")
	}
	if err := tx.RequireCodexSubagentModel(c, account); err != nil {
		return empty, err
	}
	if session.ProjectID != "" {
		project, err := tx.ExecutionProjectPolicy(session)
		if err != nil {
			return empty, err
		}
		if !project.Agents.Allows(c.AgentID) || !project.Accounts.Allows(input.AccountID) {
			return empty, domain.Fail(domain.PermissionDenied, "Current project restrictions exclude the selected execution.", "Restore the original Agent and account permissions before resuming.")
		}
	}
	return installation, nil
}

func checkedExecutionAssignment(tx *store.Tx, sr store.Record, session domain.Session, machine domain.Machine, input domain.ExecutionJobInput) (domain.ExecutionJobInput, error) {
	var empty domain.ExecutionJobInput
	installation, err := checkedExecutionSelection(tx, session, machine, input)
	if err != nil {
		return empty, err
	}
	job, err := checkedExecutionWorkspace(tx, sr, session, machine, input.Configuration)
	if err != nil {
		return empty, err
	}
	selection := &domain.ExecutionStartupSelection{Harness: input.Configuration.Harness, ExplicitPath: installation.ExplicitPath}
	if input.Continuation != nil || input.Fork != nil {
		selection.ExecutableSHA256 = input.Installation.ExecutableSHA256
		if input.Startup != nil {
			selection.ExecutableSHA256 = input.Startup.ExecutableSHA256
		}
		if session.Startup != nil && session.Startup.Ready != nil {
			selection.ExecutableSHA256 = session.Startup.Ready.ExecutableSHA256
		}
	}
	input.Version, input.Startup, input.Installation = 4, selection, domain.Installation{}
	input.Preparation, input.Manifest = job.Input, job.Output
	return input, input.Validate()
}

// Source operations recheck current policy and workspace ownership without
// converting or replacing the original native assignment. Legacy operations
// retain their existing capability contract; only new executions require v4.
func checkedExecutionSource(tx *store.Tx, sr store.Record, session domain.Session, machine domain.Machine, input domain.ExecutionJobInput) error {
	if input.Validate() != nil || !session.OwnsExecution(input) {
		return continuationConflict()
	}
	candidate := input
	if input.Version != 4 {
		candidate.Startup = &domain.ExecutionStartupSelection{Harness: input.Configuration.Harness, ExplicitPath: input.Installation.ResolvedPath, ExecutableSHA256: input.Installation.ExecutableSHA256}
	}
	var err error
	if input.Version == 4 {
		_, err = checkedExecutionSelection(tx, session, machine, candidate)
	} else {
		_, err = checkedExecutionConfiguration(tx, session, machine, candidate)
	}
	if err != nil {
		return err
	}
	job, err := checkedExecutionWorkspace(tx, sr, session, machine, input.Configuration)
	if err != nil {
		return err
	}
	if !bytes.Equal(job.Input, input.Preparation) || !bytes.Equal(job.Output, input.Manifest) {
		return workspace.ResultUncertain()
	}
	return nil
}

func checkedExecutionWorkspace(tx *store.Tx, sr store.Record, session domain.Session, machine domain.Machine, c domain.ExecutionConfiguration) (domain.Job, error) {
	var empty domain.Job
	prepared, err := tx.Get(domain.JobKind, session.Preparation.JobID)
	if err != nil {
		return empty, err
	}
	job, err := store.Decode[domain.Job](prepared)
	if err != nil {
		return empty, err
	}
	if prepared.SessionID != sr.ID || job.Type != domain.PrepareWorkspaceJob || job.State != domain.JobSucceeded || job.MachineID != session.MachineID {
		return empty, workspace.ResultUncertain()
	}
	var request workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(job.Input, &request) != nil || domain.Decode(job.Output, &manifest) != nil || request.SessionID != sr.ID || request.MachineID != session.MachineID || request.Type != session.Workspace || workspace.ValidateResult(request, manifest, machine.OS) != nil {
		return empty, workspace.ResultUncertain()
	}
	if err := validateLocalOrigin(tx, session); err != nil {
		return empty, err
	}
	if request.Type == domain.Local && (session.LocalOrigin == nil || request.OriginMachineID != session.LocalOrigin.MachineID) {
		return empty, workspace.ResultUncertain()
	}
	if c.Harness == domain.GrokBuild {
		if request.Type != domain.GeneralChat || len(manifest.Repositories) != 0 {
			return empty, domain.Fail(domain.Unsupported, "This Grok runner requires an owned General Chat workspace.", "Retain repository workspaces for their separately verified native profile.")
		}
	} else if c.Harness == domain.Codex {
		settings := codex.ThreadSettings{Model: c.NativeModel, Provider: codex.APIProvider, Cwd: manifest.PrimaryPath, Effort: c.Effort, Instructions: c.Instructions, Options: c.Options}
		if len(manifest.Repositories) > 1 {
			settings.WorkspaceRoots = manifest.WorkspaceRoots()
		}
		if err := codex.ValidateSelection(settings); err != nil {
			return empty, err
		}
	}
	return job, nil
}

func (s *Service) dispatchExecution(ctx context.Context, record store.Record) error {
	// This private server coordinator owns dispatch, including its retained PR
	// history read. Establish its own owner context instead of depending on the
	// ticker caller; public RPC and Worker authorization remain independent.
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	attempt, observations, prepareErr := s.preparePRFixDispatch(ctx, record)
	if prepareErr != nil && domain.SafeError(prepareErr).Code == domain.Conflict {
		// A changed prerequisite cannot leave a never-started automatic input
		// holding the PR indefinitely and vetoing another independent kind.
		// The ordinary removal transaction refuses claimed/native work.
		if err := s.cancelAutomaticPRPreflight(ctx, attempt); err != nil {
			return err
		}
	}
	identity := struct {
		Session  domain.ID
		Revision uint64
	}{record.ID, record.Revision}
	var result store.Result
	err := prepareErr
	if err == nil {
		result, err = s.Store.Mutate(ctx, domain.NewID(), "session.dispatch.execution", identity, func(tx *store.Tx) (any, error) {
			sr, session, err := sessionRecord(tx, record.ID)
			if err != nil {
				return nil, err
			}
			if sr.Revision != record.Revision {
				return nil, firstDispatchConflict()
			}
			var job store.Record
			if session.InitialExecution == nil {
				job, err = queueInitialExecution(tx, sr, session, false)
			} else {
				job, err = queueContinuation(tx, sr, session, false)
			}
			if err == nil {
				err = bindPRFixAssignment(tx, attempt, job, observations)
			}
			return sessionReceipt{SessionID: sr.ID}, err
		})
	}
	if err == nil {
		s.logger.InfoContext(ctx, "session_execution_queued", "session_id", record.ID, "request_id", result.RequestID)
		return nil
	}
	problem := domain.SafeError(err)
	if problem.Code == domain.ProviderDisabled {
		s.logger.InfoContext(ctx, "session_dispatch_denied", "operation", "new_execution", "session_id", record.ID, "reason", problem.Code, "enabled", false)
	}
	if ctx.Err() != nil || (problem.Code == domain.Conflict && prepareErr == nil) || problem.Code == domain.Internal || problem.Cause != "" {
		return err
	}
	// A failed native/configuration check rolls back the complete claim first.
	// Publish a stable blocking reason separately without creating a snapshot,
	// consuming routing/input, or erasing a concurrent Stop/Archive/recovery.
	_, updateErr := s.Store.Mutate(ctx, domain.NewID(), "session.dispatch.blocked", struct {
		Identity any
		Problem  *domain.Error
	}{identity, problem}, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, record.ID)
		if err != nil {
			return nil, err
		}
		if sr.Revision != record.Revision || session.Dispatch == domain.DispatchPaused || session.Recovery != domain.NoRecovery || session.Archive != domain.NotArchived || (session.Problem != nil && *session.Problem == *problem) {
			return nil, firstDispatchConflict()
		}
		session.Dispatch, session.Problem = domain.DispatchBlocked, problem
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID}, nil
	})
	if updateErr == nil {
		s.logger.InfoContext(ctx, "session_execution_blocked", "session_id", record.ID, "code", problem.Code)
	} else if domain.SafeError(updateErr).Code != domain.Conflict {
		return updateErr
	}
	return err
}

// Manual PR fixes perform bounded provider reads before their claim transaction.
// Each claim commits an immutable job for the outbound Worker stream; only that
// Worker revalidates native settings and filesystem/process ownership, executes
// the harness and verifies its push. No native publication runs on the server.
func (s *Service) runExecutionDispatch(parent context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.runExecutionDispatchTicks(parent, ticker.C)
}

func (s *Service) runExecutionDispatchTicks(parent context.Context, ticks <-chan time.Time) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	var after domain.ID
	var fixAfter domain.ID
	// Slow provider reads occupy a separate bounded lane, never the ordinary
	// serial queue scan. Keep each original session unique and join on shutdown.
	active := map[domain.ID]bool{}
	completed := make(chan struct {
		record store.Record
		failed bool
	}, 4)
	retries := prFixDispatchRetries{}
	var fixes sync.WaitGroup
	defer fixes.Wait()
	for {
		var now time.Time
		select {
		case <-ctx.Done():
			return
		case now = <-ticks:
		}
		for len(completed) > 0 {
			result := <-completed
			delete(active, result.record.ID)
			if result.failed {
				delay := retries.failed(result.record, now)
				s.logger.InfoContext(ctx, "manual_pr_fix_retry_scheduled", "session_id", result.record.ID, "delay_ms", delay.Milliseconds())
			} else {
				delete(retries, result.record.ID)
			}
		}
		retries.expire(now)
		fixAfter = s.reconcilePRFixes(ctx, fixAfter)
		page, more, err := s.Store.ExecutionCandidates(ctx, after, 50)
		if err != nil {
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "session_execution_scan_failed", "code", domain.SafeError(err).Code)
			}
			continue
		}
		for _, record := range page {
			if ctx.Err() != nil {
				return
			}
			after = record.ID
			if active[record.ID] {
				continue
			}
			manual, err := s.isPRFixCandidate(ctx, record.ID)
			if err != nil {
				s.logger.WarnContext(ctx, "manual_pr_fix_candidate_failed", "session_id", record.ID, "code", domain.SafeError(err).Code)
				continue
			}
			if manual {
				if len(active) >= cap(completed) || !retries.ready(record, now) {
					continue
				}
				active[record.ID] = true
				fixes.Add(1)
				go func() {
					defer fixes.Done()
					bounded, cancel := context.WithTimeout(ctx, 75*time.Second)
					defer cancel()
					err := s.dispatchExecution(bounded, record)
					if err != nil && ctx.Err() == nil {
						s.logger.WarnContext(ctx, "manual_pr_fix_dispatch_failed", "session_id", record.ID, "code", domain.SafeError(err).Code)
					}
					completed <- struct {
						record store.Record
						failed bool
					}{record, err != nil}
				}()
				continue
			}
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = s.dispatchExecution(bounded, record)
			cancel()
			if err != nil && (domain.SafeError(err).Cause != "" || domain.SafeError(err).Code == domain.Internal) && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "session_execution_storage_failed", "session_id", record.ID, "code", domain.SafeError(err).Code)
			}
		}
		if !more {
			after = ""
		}
	}
}
