package server

import (
	"context"
	"crypto/sha256"
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
	if err != nil || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.InstanceID != grant.InstanceID || job.MachineID != grant.MachineID {
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
	initial := session.InitialExecution
	if session.MachineID != grant.MachineID || session.AgentID != input.Configuration.AgentID || initial.ID != grant.ExecutionID || initial.ConfigurationDigest != input.ConfigurationDigest || initial.InitialAccountID != input.AccountID || initial.ConnectionID != input.ConnectionID || initial.InputID != input.InputID {
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
		r, err := tx.Get(domain.ProjectKind, session.ProjectID)
		if err != nil {
			return empty, executionDenied()
		}
		project, err := store.Decode[domain.Project](r)
		if err != nil || !project.Agents.Allows(session.AgentID) || !project.Accounts.Allows(input.AccountID) {
			return empty, executionDenied()
		}
	}
	_, account, err := accountFromTx(tx, input.AccountID, 0)
	if err != nil || !account.Enabled || account.Type != domain.APIAccount || account.Health != domain.AccountReady || account.Removal != nil || account.ConfirmedExhausted || account.Connection == nil || account.Connection.ID != input.ConnectionID || account.ProviderID != input.Configuration.ProviderID {
		return empty, executionDenied()
	}
	r, err := tx.Get(domain.ProviderKind, input.Configuration.ProviderID)
	if err != nil {
		return empty, executionDenied()
	}
	provider, err := store.Decode[domain.Provider](r)
	if err != nil || provider.Authentication != account.Connection.Authentication || provider.Protocol != domain.OpenAIResponses || input.Configuration.Harness != domain.Codex || input.Installation.Version != domain.CodexProtocolVersion {
		return empty, executionDenied()
	}
	r, err = tx.Get(domain.ModelKind, input.Configuration.ModelID)
	if err != nil {
		return empty, executionDenied()
	}
	model, err := store.Decode[domain.Model](r)
	if err != nil || model.ProviderID != input.Configuration.ProviderID || !slices.Contains(model.Harnesses, input.Configuration.Harness) {
		return empty, executionDenied()
	}
	scope := apiproxy.Scope{ExecutionID: grant.ExecutionID, SessionID: input.SessionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, NativeModel: input.Configuration.NativeModel, Provider: provider, Operations: []apiproxy.Operation{apiproxy.ResponseCreate, apiproxy.ResponseCompact}}
	if err := scope.Validate(); err != nil {
		return empty, executionDenied()
	}
	return scope, nil
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
		var input domain.ExecutionJobInput
		if err != nil || domain.Decode(job.Input, &input) != nil {
			return nil, executionDenied()
		}
		grant.ExecutionID = input.ExecutionID
		if _, err := s.executionAuthority.scope(tx, grant); err != nil {
			return nil, err
		}
		return struct{ JobID domain.ID }{identity.Job}, tx.PutExecutionGrant(grant)
	})
	if err == nil {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			stored, err := tx.ExecutionGrant(identity.Digest)
			if err != nil || stored.JobID != grant.JobID {
				return executionDenied()
			}
			_, err = s.executionAuthority.scope(tx, stored)
			return err
		})
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("execution_credential_registered", "job_id", identity.Job, "machine_id", identity.Machine, "instance_id", identity.Instance, "request_id", meta.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.RegisterExecutionResponse{ProxyPath: apiproxy.Prefix, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
