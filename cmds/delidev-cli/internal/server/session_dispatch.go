package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func firstDispatchConflict() error {
	return domain.Fail(domain.Conflict, "The session is not eligible for its first execution.", "Wait for ready workspace ownership, retain queue order and explicitly resume a paused session.")
}

// All callers must propagate failure out of their Store.Mutate callback. The
// transient ready state, snapshot/routing claim and job are one transaction;
// validating the selected configuration after the claim cannot partially commit.
func queueInitialExecution(tx *store.Tx, sr store.Record, session domain.Session, explicitResume bool) (store.Record, error) {
	if session.InitialExecution != nil || session.ActiveExecutionID != "" || session.Outcome != domain.ExecutionNotStarted || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || (session.Dispatch != domain.DispatchBlocked && session.Dispatch != domain.DispatchReady && !(explicitResume && session.Dispatch == domain.DispatchPaused)) {
		return store.Record{}, firstDispatchConflict()
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
	var empty domain.ExecutionJobInput
	c := claim.Configuration
	if c.Harness != domain.Codex {
		return empty, domain.Fail(domain.Unsupported, "This harness has no integrated execution profile yet.", "Select a supported installed Codex profile; no harness fallback is performed.")
	}
	var installation *domain.Installation
	for i := range machine.Installations {
		if machine.Installations[i].Harness == c.Harness {
			if installation != nil {
				return empty, domain.Fail(domain.RecoveryRequired, "The native installation selection is ambiguous.", "Refresh the selected Worker's discovery before execution.")
			}
			installation = &machine.Installations[i]
		}
	}
	if installation == nil || installation.State != domain.InstallationDetected || installation.Version != codex.SupportedVersion || !installation.ProtocolVerified || installation.Protocol == nil || installation.Protocol.Protocol != domain.CodexAppServer || installation.Protocol.State != domain.ProtocolVerified || installation.Protocol.Problem != nil || installation.Problem != nil || installation.ResolvedPath == "" || installation.ObservedAt == nil || installation.ObservedAt.IsZero() || installation.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return empty, domain.Fail(domain.Unsupported, "The selected Worker has no verified installation for this execution profile.", "Run machine discovery with native protocol verification for the selected Codex installation.")
	}
	_, account, err := accountFromTx(tx, claim.InitialAccountID, 0)
	if err != nil {
		return empty, err
	}
	if account.Type != domain.APIAccount || account.Connection == nil || account.Connection.ID != claim.ConnectionID || account.Health != domain.AccountReady || account.Validation == nil || account.Validation.ConnectionID != claim.ConnectionID || account.Validation.State != domain.Observed || account.Validation.Problem != nil || account.Validation.ObservedAt.IsZero() || account.Validation.ObservedAt.Before(account.Connection.ConnectedAt) || account.Validation.ObservedAt.After(time.Now().UTC().Add(time.Second)) || (account.Validation.Authentication != domain.CredentialAccepted && account.Validation.Authentication != domain.KeylessEndpoint) {
		return empty, domain.Fail(domain.Unsupported, "The selected account lacks verified API execution authority.", "Connect and validate the selected API account; stored credentials or a model catalog alone do not authorize inference.")
	}
	pr, err := tx.Get(domain.ProviderKind, c.ProviderID)
	if err != nil {
		return empty, err
	}
	provider, err := store.Decode[domain.Provider](pr)
	if err != nil {
		return empty, err
	}
	if provider.Protocol != domain.OpenAIResponses || (provider.Authentication == domain.KeylessAuth) != (account.Validation.Authentication == domain.KeylessEndpoint) || provider.Authentication != account.Connection.Authentication || account.ProviderID != c.ProviderID {
		return empty, domain.Fail(domain.Unsupported, "The selected provider protocol is incompatible with this native profile.", "Select an OpenAI Responses provider for Codex; no protocol translation is performed.")
	}
	prepared, err := tx.Get(domain.JobKind, session.Preparation.JobID)
	if err != nil {
		return empty, err
	}
	job, err := store.Decode[domain.Job](prepared)
	if err != nil {
		return empty, err
	}
	var request workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(job.Input, &request) != nil || domain.Decode(job.Output, &manifest) != nil || request.SessionID != sr.ID || request.MachineID != session.MachineID || workspace.ValidateResult(request, manifest, machine.OS) != nil {
		return empty, workspace.ResultUncertain()
	}
	if request.Type == domain.Local || len(request.Repositories) > 1 {
		return empty, domain.Fail(domain.Unsupported, "This workspace requires an additional native execution adapter.", "Retain its prepared files; Local origin and multiple native permission roots must be validated before execution.")
	}
	if err := codex.ValidateSelection(codex.ThreadSettings{Model: c.NativeModel, Provider: codex.APIProvider, Cwd: manifest.PrimaryPath, Effort: c.Effort, Instructions: c.Instructions, Options: c.Options}); err != nil {
		return empty, err
	}
	ir, err := tx.Get(domain.QueueKind, claim.InputID)
	if err != nil {
		return empty, err
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil {
		return empty, err
	}
	input := domain.ExecutionJobInput{Version: 1, SessionID: sr.ID, MachineID: session.MachineID, ExecutionID: claim.ID, InputID: claim.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: queued.NativeRequestID, Configuration: c, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID, Input: domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}, Installation: *installation, Preparation: job.Input, Manifest: job.Output}
	return input, input.Validate()
}

func (s *Service) dispatchInitial(ctx context.Context, record store.Record) error {
	identity := struct {
		Session  domain.ID
		Revision uint64
	}{record.ID, record.Revision}
	result, err := s.Store.Mutate(ctx, domain.NewID(), "session.dispatch.initial", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, record.ID)
		if err != nil {
			return nil, err
		}
		if sr.Revision != record.Revision {
			return nil, firstDispatchConflict()
		}
		_, err = queueInitialExecution(tx, sr, session, false)
		return sessionReceipt{SessionID: sr.ID}, err
	})
	if err == nil {
		s.logger.InfoContext(ctx, "initial_execution_queued", "session_id", record.ID, "request_id", result.RequestID)
		return nil
	}
	problem := domain.SafeError(err)
	if ctx.Err() != nil || problem.Code == domain.Conflict || problem.Code == domain.Internal || problem.Cause != "" {
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
		if sr.Revision != record.Revision || session.InitialExecution != nil || session.Dispatch == domain.DispatchPaused || session.Recovery != domain.NoRecovery || session.Archive != domain.NotArchived || (session.Problem != nil && *session.Problem == *problem) {
			return nil, firstDispatchConflict()
		}
		session.Dispatch, session.Problem = domain.DispatchBlocked, problem
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID}, nil
	})
	if updateErr == nil {
		s.logger.InfoContext(ctx, "initial_execution_blocked", "session_id", record.ID, "code", problem.Code)
	} else if domain.SafeError(updateErr).Code != domain.Conflict {
		return updateErr
	}
	return err
}

// No provider/native work runs here. Each bounded claim transaction commits an
// immutable job for the outbound Worker stream, which revalidates real native
// settings and filesystem/process ownership before it can send the first input.
func (s *Service) runInitialExecutionDispatch(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var after domain.ID
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		page, more, err := s.Store.InitialExecutionCandidates(ctx, after, 50)
		if err != nil {
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "initial_execution_scan_failed", "code", domain.SafeError(err).Code)
			}
			continue
		}
		for _, record := range page {
			if ctx.Err() != nil {
				return
			}
			after = record.ID
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.dispatchInitial(bounded, record)
			cancel()
			if err != nil && (domain.SafeError(err).Cause != "" || domain.SafeError(err).Code == domain.Internal) && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "initial_execution_storage_failed", "session_id", record.ID, "code", domain.SafeError(err).Code)
			}
		}
		if !more {
			after = ""
		}
	}
}
