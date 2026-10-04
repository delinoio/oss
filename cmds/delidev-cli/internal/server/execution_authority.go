package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Mutate creates a durable receipt even for a successful no-op. Roll back only
// raced, unchanged history observations using this private signal; remove it if
// the store gains a conditional observation transaction without request receipts.
var errHistoryObservationUnchanged = domain.Fail(domain.Conflict, "The native history mode is already recorded.", "Preserve the original observation.")

type executionAuthority struct {
	service *Service
	epoch   domain.ID
	mu      sync.Mutex
	closed  bool
	active  map[domain.ID]executionLease
	wait    sync.WaitGroup
}

type executionLease struct {
	account domain.ID
	cancel  context.CancelFunc
	done    chan struct{}
}

func newExecutionAuthority(service *Service) *executionAuthority {
	return &executionAuthority{service: service, epoch: domain.NewID(), active: map[domain.ID]executionLease{}}
}

func executionDenied() *domain.Error {
	return domain.Fail(domain.PermissionDenied, "This execution no longer has native API authority.", "Reconcile the original session, Worker and account connection; do not substitute another account or replay input.")
}

// Resolve every authorization decision from current durable ownership. A stored
// token digest or native account connection alone never grants inference.
func (a *executionAuthority) scope(tx *store.Tx, grant store.ExecutionGrant) (apiproxy.Scope, error) {
	var empty apiproxy.Scope
	if grant.ServerEpoch != a.epoch {
		return empty, executionDenied()
	}
	jobRecord, err := tx.Get(domain.JobKind, grant.JobID)
	if err != nil {
		return empty, executionDenied()
	}
	job, err := store.Decode[domain.Job](jobRecord)
	if err != nil {
		return empty, executionDenied()
	}
	if job.Type == domain.CompactSessionJob {
		return a.compactionScope(tx, grant, jobRecord, job)
	}
	if job.Type == domain.GenerateSessionTitleJob {
		return a.titleScope(tx, grant, jobRecord, job)
	}
	if job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.InstanceID != grant.InstanceID || job.MachineID != grant.MachineID {
		return empty, executionDenied()
	}
	canceled, err := tx.JobCancellationRequested(grant.JobID)
	if err != nil || canceled {
		return empty, executionDenied()
	}
	var input domain.ExecutionJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.ExecutionID != grant.ExecutionID || input.MachineID != grant.MachineID || input.SessionID != jobRecord.SessionID {
		return empty, executionDenied()
	}
	instance, seen, err := tx.WorkerInstance(grant.MachineID)
	if err != nil || instance != grant.InstanceID || seen.After(time.Now().UTC().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
		return empty, executionDenied()
	}
	deviceRecord, err := tx.Get(domain.DeviceKind, grant.DeviceID)
	if err != nil {
		return empty, executionDenied()
	}
	device, err := store.Decode[domain.Device](deviceRecord)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != grant.MachineID {
		return empty, executionDenied()
	}
	if _, _, err := activeMachine(tx, grant.MachineID); err != nil {
		return empty, executionDenied()
	}
	_, session, err := sessionRecord(tx, input.SessionID)
	if err != nil || session.InitialExecution == nil || session.ActiveExecutionID != grant.ExecutionID || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchClaimed || (session.Outcome != domain.ExecutionNotStarted && session.Outcome != domain.ExecutionRunning) {
		return empty, executionDenied()
	}
	if !session.OwnsExecution(input) {
		return empty, executionDenied()
	}
	if err := tx.RequireExecutionAgent(session, input.Configuration.AgentID); err != nil {
		return empty, executionDenied()
	}
	ir, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil || ir.SessionID != input.SessionID {
		return empty, executionDenied()
	}
	queued, err := store.Decode[domain.QueuedInput](ir)
	if err != nil || (queued.Delivery != domain.InputClaimed && queued.Delivery != domain.InputAccepted) || queued.ExecutionID != input.ExecutionID || queued.NativeRequestID != input.TurnRequestID || queued.Mode != input.Input.Mode || queued.Prompt != input.Input.Prompt {
		return empty, executionDenied()
	}
	if session.ProjectID != "" {
		project, err := tx.ExecutionProjectPolicy(session)
		if err != nil || !project.Agents.Allows(session.AgentID) || !project.Accounts.Allows(input.AccountID) {
			return empty, executionDenied()
		}
	}
	_, account, err := accountFromTx(tx, input.AccountID, 0)
	if err != nil || !account.Enabled || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.ProviderID != input.Configuration.ProviderID {
		return empty, executionDenied()
	}
	managed := input.Configuration.Subscription && input.Configuration.SubscriptionService == domain.SubscriptionChatGPT && account.Type == domain.SubscriptionAccount && account.SubscriptionService == input.Configuration.SubscriptionService && account.ProviderID == "" && input.Configuration.Harness == domain.Codex
	var provider domain.Provider
	var operations []apiproxy.Operation
	if !managed {
		r, err := tx.Get(domain.ProviderKind, input.Configuration.ProviderID)
		if err != nil {
			return empty, executionDenied()
		}
		provider, err = store.Decode[domain.Provider](r)
		if err != nil || provider.Authentication != account.Connection.Authentication {
			return empty, executionDenied()
		}
		operations = executionAPIOperations(input, provider.Protocol)
	}
	if managed {
		state := account.Subscription
		if state == nil || state.RecoveryRequired || state.Lease == nil {
			return empty, executionDenied()
		}
		lease := state.Lease
		if lease.Action != domain.SubscriptionExecute || lease.OperationID != grant.JobID || lease.MachineID != grant.MachineID || lease.InstanceID != grant.InstanceID || lease.DeviceID != grant.DeviceID || lease.Epoch != a.service.subscriptionServerEpoch() || lease.Generation != state.Generation {
			return empty, executionDenied()
		}
	}
	if (!managed && account.Type != domain.APIAccount) || (len(operations) == 0 && !managed) {
		return empty, executionDenied()
	}
	r, err := tx.Get(domain.ModelKind, input.Configuration.ModelID)
	if err != nil {
		return empty, executionDenied()
	}
	model, err := store.Decode[domain.Model](r)
	if err != nil || model.ProviderID != input.Configuration.ProviderID || model.SubscriptionService != input.Configuration.SubscriptionService || !model.MatchesAccount(account, input.Configuration.Harness) || !slices.Contains(model.Harnesses, input.Configuration.Harness) {
		return empty, executionDenied()
	}
	scope := apiproxy.Scope{ExecutionID: grant.ExecutionID, SessionID: input.SessionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, SubscriptionService: input.Configuration.SubscriptionService, ModelID: input.Configuration.ModelID, NativeModel: input.Configuration.NativeModel, Harness: input.Configuration.Harness, Provider: provider, Operations: operations}
	if !managed {
		if err := scope.Validate(); err != nil {
			return empty, executionDenied()
		}
	}
	return scope, nil
}

// Relay compatibility is narrower than native protocol discovery. In particular,
// OpenCode continuation requires its exact accepted predecessor profile; no
// different SDK protocol or silently omitted native options is authorized.
func executionAPIOperations(input domain.ExecutionJobInput, protocol domain.APIProtocol) []apiproxy.Operation {
	switch input.Configuration.Harness {
	case domain.Codex:
		if input.Installation.Version == domain.CodexProtocolVersion && protocol == domain.OpenAIResponses {
			return []apiproxy.Operation{apiproxy.ResponseCreate, apiproxy.ResponseCompact}
		}
	case domain.ClaudeCode:
		if input.Validate() != nil || input.Installation.Version != domain.ClaudeProtocolVersion || protocol != domain.AnthropicMessages {
			return nil
		}
		if _, err := input.Configuration.ClaudeAPIInputPermission(input.Input.Mode); err == nil {
			// Resumed assignments retain the exact checked predecessor. Token
			// counting and other protocols require separate evidence.
			return []apiproxy.Operation{apiproxy.MessageCreate}
		}
	case domain.GrokBuild:
		if input.Validate() != nil || input.Version != 1 || input.Continuation != nil || input.Installation.Version != domain.GrokProtocolVersion || protocol != domain.OpenAIChat {
			return nil
		}
		if _, err := input.Configuration.GrokModeForInput(input.Input.Mode); err == nil {
			return []apiproxy.Operation{apiproxy.ChatCompletion}
		}
	case domain.OpenCode:
		o := input.Configuration.Options
		validGeneration := input.Version == 1 && input.Continuation == nil || input.Version == 2 && input.Continuation != nil && input.Validate() == nil
		if !validGeneration || input.Installation.Version != domain.OpenCodeProtocolVersion || protocol != domain.OpenAIChat || input.Configuration.Effort != "" || o.SubagentModel != "" || o.SubagentEffort != "" || o.MaxConcurrency != 0 || o.ApprovalReviewModel != "" || o.ServiceTier != "" {
			return nil
		}
		if _, err := o.OpenCodePrimaryForInput(input.Input.Mode); err == nil {
			return []apiproxy.Operation{apiproxy.ChatCompletion}
		}
	}
	return nil
}

func (a *executionAuthority) resolve(ctx context.Context, grant store.ExecutionGrant) (apiproxy.Scope, error) {
	var scope apiproxy.Scope
	err := a.service.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		scope, err = a.scope(tx, grant)
		return err
	})
	return scope, err
}

func (a *executionAuthority) Acquire(ctx context.Context, token string) (*apiproxy.Lease, error) {
	digest := sha256.Sum256([]byte(token))
	var grant store.ExecutionGrant
	var scope apiproxy.Scope
	err := a.service.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		grant, err = tx.ExecutionGrant(digest[:])
		if err == nil {
			scope, err = a.scope(tx, grant)
		}
		return err
	})
	if err != nil {
		return nil, executionDenied()
	}
	// Managed subscription registrations bind publication only. They never
	// authorize the API relay or expose a subscription bundle through it.
	if scope.SubscriptionService != "" {
		return nil, executionDenied()
	}
	leaseContext, cancel := context.WithCancel(ctx)
	id := domain.NewID()
	released := make(chan struct{})
	a.mu.Lock()
	if a.closed || len(a.active) >= 32 {
		a.mu.Unlock()
		cancel()
		return nil, domain.Fail(domain.Unavailable, "Execution API authority is unavailable.", "Wait for owned requests to finish or reconcile the execution.")
	}
	a.active[id] = executionLease{account: scope.AccountID, cancel: cancel, done: released}
	a.wait.Add(1)
	a.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			changed := a.service.Store.Changed()
			if _, err := a.resolve(leaseContext, grant); err != nil {
				return
			}
			select {
			case <-leaseContext.Done():
				return
			case <-changed:
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	lease := &apiproxy.Lease{Scope: scope, Context: leaseContext}
	// Begin/HTTP-send claims require live authority. Completion only updates an
	// existing exact original observation, even after Stop or account revocation.
	// It grants no inference and is joined before lease/storage closure.
	lease.PublishDiagnostic = func(ctx context.Context, value domain.RequestDiagnostic) error {
		if value.SessionID != scope.SessionID || value.ExecutionID != scope.ExecutionID || value.AccountID != scope.AccountID || value.ConnectionID != scope.ConnectionID || value.ProviderID != scope.ProviderID || value.ModelID != scope.ModelID || value.Source != domain.DiagnosticProxyHTTP {
			return executionDenied()
		}
		publication := domain.NewID()
		value.PublicationRequestID = publication
		_, err := a.service.Store.Mutate(ctx, publication, "request-diagnostic.publish", value, func(tx *store.Tx) (any, error) {
			if value.State == domain.DiagnosticInProgress {
				if leaseContext.Err() != nil {
					return nil, executionDenied()
				}
				if _, err := a.scope(tx, grant); err != nil {
					return nil, err
				}
				if scope.Purpose == domain.SessionTitleUsage && value.HTTPAttempted != nil && *value.HTTPAttempted {
					if value.Operation != domain.DiagnosticResponse {
						return nil, executionDenied()
					}
					claimed, err := tx.ClaimTitleHTTPRequest(grant.JobID)
					if err != nil {
						return nil, err
					}
					if !claimed {
						return nil, executionDenied()
					}
				}
			} else if value.Revision == 0 {
				return nil, executionDenied()
			}
			if err := tx.PutRequestDiagnostic(value, value.Revision); err != nil {
				return nil, err
			}
			return struct {
				ID       domain.ID
				Revision uint64
			}{value.ID, value.Revision + 1}, nil
		})
		return err
	}

	// Title HTTP ownership and diagnostic send publication commit together.
	// No separate BeforeSubmit mutation can consume title authority if metadata
	// persistence fails before the upstream request is sent.
	lease.Release = func() {
		once.Do(func() {
			cancel()
			<-done
			a.mu.Lock()
			delete(a.active, id)
			close(released)
			a.mu.Unlock()
			a.wait.Done()
		})
	}
	lease.Key = func(ctx context.Context) ([]byte, error) {
		if leaseContext.Err() != nil {
			return nil, executionDenied()
		}
		unlock, err := a.service.lockAccounts(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		if leaseContext.Err() != nil {
			return nil, executionDenied()
		}
		if _, err := a.resolve(ctx, grant); err != nil {
			return nil, err
		}
		vault, err := a.service.secrets()
		if err != nil {
			return nil, err
		}
		return vault.Get(ctx, credentials.Ref{Owner: scope.AccountID, ID: scope.ConnectionID, Purpose: credentials.AccountAPI})
	}
	reference := func(kind apiproxy.ReferenceKind, nativeID string) store.ExecutionReference {
		return store.ExecutionReference{SessionID: scope.SessionID, AccountID: scope.AccountID, ConnectionID: scope.ConnectionID, ModelID: scope.ModelID, Kind: kind, NativeID: nativeID}
	}
	lease.AuthorizeReference = func(ctx context.Context, kind apiproxy.ReferenceKind, nativeID string) error {
		if leaseContext.Err() != nil {
			return executionDenied()
		}
		return a.service.Store.Read(ctx, func(tx *store.Tx) error {
			if _, err := a.scope(tx, grant); err != nil {
				return err
			}
			_, session, err := sessionRecord(tx, scope.SessionID)
			if err != nil {
				return err
			}
			if len(session.AccountChanges) != 0 {
				return domain.Fail(domain.PermissionDenied, "Account-switched execution requires full native history.", "Provider conversation and previous-response references cannot cross account selections.")
			}
			exists, err := tx.HasExecutionReference(reference(kind, nativeID))
			if err != nil {
				return err
			}
			if !exists {
				return executionDenied()
			}
			return nil
		})
	}
	if scope.Purpose != domain.SessionTitleUsage && scope.Provider.Protocol == domain.OpenAIResponses {
		lease.ObserveHistory = func(ctx context.Context, accountBound bool) error {
			if leaseContext.Err() != nil {
				return executionDenied()
			}
			// Most provider requests retain the already known mode. Revalidate
			// authority without creating a receipt or waking store watchers.
			pending := false
			err := a.service.Store.Read(ctx, func(tx *store.Tx) error {
				_, _, mode, err := a.historyObservation(tx, grant, accountBound)
				pending = mode != ""
				return err
			})
			if err != nil || !pending {
				return err
			}
			_, err = a.service.Store.Mutate(ctx, domain.NewID(), "execution.observe-history-mode", struct {
				Execution    domain.ID
				AccountBound bool
			}{scope.ExecutionID, accountBound}, func(tx *store.Tx) (any, error) {
				// The read above grants no write authority. Another request or
				// revocation may have changed ownership or sticky history.
				sr, session, mode, err := a.historyObservation(tx, grant, accountBound)
				if err != nil {
					return nil, err
				}
				if mode == "" {
					return nil, errHistoryObservationUnchanged
				}
				session.CurrentNativeHistory = mode
				if session.Execution != nil {
					session.Execution.NativeHistory = mode
				}
				_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
				return struct{}{}, err
			})
			if errors.Is(err, errHistoryObservationUnchanged) {
				return nil
			}
			return err
		}
	}
	lease.ObserveReference = func(ctx context.Context, kind apiproxy.ReferenceKind, nativeID string) error {
		if leaseContext.Err() != nil {
			return executionDenied()
		}
		_, err := a.service.Store.Mutate(ctx, domain.NewID(), "execution.observe-reference", reference(kind, nativeID), func(tx *store.Tx) (any, error) {
			if _, err := a.scope(tx, grant); err != nil {
				return nil, err
			}
			return struct{}{}, tx.ObserveExecutionReference(reference(kind, nativeID))
		})
		return err
	}
	// Recheck after registering cancellation. Revocation between the initial
	// read and watcher startup cannot authorize a new native request.
	if _, err := a.resolve(ctx, grant); err != nil {
		lease.Release()
		return nil, err
	}
	return lease, nil
}

// historyObservation returns a pending mode only after fresh durable authority
// checks. An empty mode means the existing sticky observation is unchanged.
func (a *executionAuthority) historyObservation(tx *store.Tx, grant store.ExecutionGrant, accountBound bool) (store.Record, domain.Session, domain.NativeHistoryMode, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Session{}, "", err
	}
	scope, err := a.scope(tx, grant)
	if err != nil {
		return store.Record{}, domain.Session{}, "", err
	}
	sr, session, err := sessionRecord(tx, scope.SessionID)
	if err != nil {
		return store.Record{}, domain.Session{}, "", err
	}
	if session.Execution != nil && session.Execution.ExecutionID != scope.ExecutionID {
		return store.Record{}, domain.Session{}, "", executionDenied()
	}
	mode := domain.FullNativeHistory
	if accountBound {
		if len(session.AccountChanges) != 0 {
			return store.Record{}, domain.Session{}, "", executionDenied()
		}
		mode = domain.AccountBoundHistory
	}
	if session.CurrentNativeHistory == domain.AccountBoundHistory || session.CurrentNativeHistory == mode {
		return sr, session, "", nil
	}
	return sr, session, mode, nil
}

func (a *executionAuthority) cancel() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	for _, lease := range a.active {
		lease.cancel()
	}
	a.mu.Unlock()
}

func (a *executionAuthority) stopAccount(ctx context.Context, account domain.ID) error {
	if a == nil {
		return nil
	}
	var waiting []<-chan struct{}
	a.mu.Lock()
	for _, lease := range a.active {
		if lease.account == account {
			lease.cancel()
			waiting = append(waiting, lease.done)
		}
	}
	a.mu.Unlock()
	for _, done := range waiting {
		select {
		case <-done:
		case <-ctx.Done():
			return domain.Fail(domain.RecoveryRequired, "Execution requests have not finished account revocation cleanup.", "Retry the original disconnect after owned requests stop; the account stays disconnected.")
		}
	}
	return nil
}

func (a *executionAuthority) close() {
	if a != nil {
		a.cancel()
		a.wait.Wait()
	}
}

func (s *Service) RegisterExecution(ctx context.Context, req *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 || len(req.Msg.CredentialDigest) != 32 || s.executionAuthority == nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A claimed execution and private credential digest are required.", "Register the exact current Worker assignment."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Job, Machine, Instance, Device domain.ID
		Revision                       uint64
		Digest                         []byte
	}{domain.ID(meta.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision, req.Msg.CredentialDigest}
	grant := store.ExecutionGrant{JobID: identity.Job, MachineID: identity.Machine, InstanceID: identity.Instance, DeviceID: identity.Device, ServerEpoch: s.executionAuthority.epoch, Digest: identity.Digest}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "execution.register", identity, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return nil, err
		}
		if r.Revision != identity.Revision {
			return nil, executionDenied()
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, executionDenied()
		}
		if job.Type == domain.CompactSessionJob {
			var input domain.SessionCompactionInput
			if domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
				return nil, executionDenied()
			}
			grant.ExecutionID = input.ActionID
		} else if job.Type == domain.GenerateSessionTitleJob {
			var input domain.AuxiliaryTitleInput
			if domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
				return nil, executionDenied()
			}
			grant.ExecutionID = input.OriginalExecutionID
			claimed, err := tx.ClaimTitleInference(identity.Job)
			if err != nil {
				return nil, err
			}
			if !claimed {
				return nil, executionDenied()
			}
		} else {
			var input domain.ExecutionJobInput
			if domain.Decode(job.Input, &input) != nil {
				return nil, executionDenied()
			}
			grant.ExecutionID = input.ExecutionID
		}
		scope, err := s.executionAuthority.scope(tx, grant)
		if err != nil {
			return nil, err
		}
		if !scope.Provider.EnabledValue() {
			return nil, providerDisabled()
		}
		return struct{ JobID domain.ID }{identity.Job}, tx.PutExecutionGrant(grant)
	})
	var registeredScope apiproxy.Scope
	if err == nil {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			stored, err := tx.ExecutionGrant(identity.Digest)
			if err != nil || stored.JobID != grant.JobID {
				return executionDenied()
			}
			registeredScope, err = s.executionAuthority.scope(tx, stored)
			return err
		})
	}
	if err != nil {
		if domain.SafeError(err).Code == domain.ProviderDisabled {
			s.logger.InfoContext(ctx, "execution_grant_denied", "operation", "register_execution", "job_id", identity.Job, "reason", domain.ProviderDisabled, "enabled", false)
		}
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("execution_credential_registered", "job_id", identity.Job, "machine_id", identity.Machine, "instance_id", identity.Instance, "request_id", meta.RequestId, "api_protocol", registeredScope.Provider.Protocol, "subscription_service", registeredScope.SubscriptionService, "replayed", result.Replayed)
	proxyPath := apiproxy.Prefix
	if registeredScope.SubscriptionService != "" {
		proxyPath = ""
	}
	response := connect.NewResponse(&pb.RegisterExecutionResponse{ProxyPath: proxyPath, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
