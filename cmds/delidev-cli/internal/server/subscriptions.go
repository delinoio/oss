package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type subscriptionProgress struct {
	URL, UserCode, Name string
	Generation          domain.ID
	Until               time.Time
}

func (s *Service) subscriptionServerEpoch() domain.ID {
	s.subscriptionOnce.Do(func() { s.subscriptionEpoch = domain.NewID() })
	return s.subscriptionEpoch
}

func subscriptionAction(value pb.SubscriptionAction) domain.SubscriptionAction {
	switch value {
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN:
		return domain.SubscriptionLogin
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH:
		return domain.SubscriptionRefresh
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT:
		return domain.SubscriptionLogout
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_QUOTA:
		return domain.SubscriptionQuota
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_RESET_CREDIT:
		return domain.SubscriptionResetCredit
	case pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE:
		return domain.SubscriptionExecute
	default:
		return ""
	}
}

func subscriptionDenied() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The managed account cannot grant authentication authority.", "Reconcile its original lease, credential generation and confirmed native cleanup; never redistribute an older bundle.")
}

func subscriptionActorValid(tx *store.Tx, actor domain.Principal) error {
	if actor.Type == domain.OwnerDevice {
		return nil
	}
	r, err := tx.Get(domain.DeviceKind, actor.DeviceID)
	if err != nil {
		return subscriptionDenied()
	}
	d, err := store.Decode[domain.Device](r)
	if err != nil || d.Revoked || d.Type != actor.Type || d.MachineID != actor.MachineID {
		return subscriptionDenied()
	}
	return nil
}

// Queued lifecycle requests have not granted native authority. Settle them in
// the same transaction as client revocation, without releasing independent or
// claimed leases and without undoing logout's accepted execution revocation.
func cancelQueuedSubscriptionInitiator(tx *store.Tx, device domain.ID) error {
	accounts, err := all(tx, domain.AccountKind)
	if err != nil {
		return err
	}
	for _, record := range accounts {
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			return err
		}
		state := account.Subscription
		if state == nil || state.Pending == nil || state.Pending.Actor.DeviceID != device || state.Pending.Phase != domain.SubscriptionQueued {
			continue
		}
		if o := state.ServerOperation; o != nil && o.ID == state.Pending.ID {
			o.State = domain.SubscriptionCanceled
		}
		if o := state.NativeOperation; o != nil && o.ID == state.Pending.ID {
			o.State = domain.SubscriptionCanceled
			if state.Pending.Action == domain.SubscriptionLogin && state.Lease == nil && state.Generation == "" {
				state.NativeProfileID = ""
				state.OwnerMachineID = ""
			}
		}
		state.Pending = nil
		if _, err := tx.Put(domain.AccountKind, record.ID, record.Revision, "", "", account); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionInstallation(tx *store.Tx, machineID domain.ID) (domain.Installation, error) {
	_, machine, err := activeMachine(tx, machineID)
	if err != nil {
		return domain.Installation{}, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "The selected Runner Device has no managed Codex capability.", "Connect a current Runner Device with a verified installed Codex profile.")
	}
	var selected *domain.Installation
	for i := range machine.Installations {
		if machine.Installations[i].Harness == domain.Codex {
			if selected != nil {
				return domain.Installation{}, subscriptionDenied()
			}
			selected = &machine.Installations[i]
		}
	}
	if selected != nil && domain.CodexVersionAllowed(selected.Version) && selected.State == domain.InstallationDetected && selected.ProtocolVerified && selected.ResolvedPath != "" && selected.Problem == nil && selected.Protocol != nil && selected.Protocol.Protocol == domain.ProtocolFor(domain.Codex) && selected.Protocol.State == domain.ProtocolVerified && selected.Protocol.Problem == nil && selected.ObservedAt != nil && !selected.ObservedAt.IsZero() && !selected.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return *selected, nil
	}
	return domain.Installation{}, domain.Fail(domain.Unsupported, "Managed subscriptions require installed Codex 0.151.0.", "Discover and verify that exact native installation on the explicitly selected Runner Device.")
}

func subscriptionAccount(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err != nil {
		return r, a, err
	}
	if a.Type != domain.SubscriptionAccount {
		return r, a, domain.Fail(domain.Unsupported, "This operation requires a Codex subscription account.", "Preserve API accounts through their existing connection operations.")
	}
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionChatGPT || a.ProviderID != "" {
		return r, a, domain.Fail(domain.Unsupported, "This service has no managed subscription profile.", "Select a ChatGPT service account for the verified Codex lifecycle.")
	}
	return r, a, nil
}

func (s *Service) RequestSubscription(ctx context.Context, req *connect.Request[pb.RequestSubscriptionRequest]) (*connect.Response[pb.RequestSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.requestClaudeSubscription(ctx, req)
	}
	if req.Msg.MachineId == "" {
		return s.requestServerSubscription(ctx, req)
	}
	action := subscriptionAction(req.Msg.Action)
	if action == "" || (action != domain.SubscriptionLogin && action != domain.SubscriptionRefresh && action != domain.SubscriptionLogout) || (req.Msg.DeviceCode && action != domain.SubscriptionLogin) || domain.ID(req.Msg.MachineId).Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose a supported subscription operation and Runner Device.", "Use login, refresh or logout with an explicit machine."), c)
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type == domain.WorkerDevice {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		ID, Machine domain.ID
		Revision    uint64
		Action      domain.SubscriptionAction
		DeviceCode  bool
		Actor       domain.Principal
	}{domain.ID(m.Id), domain.ID(req.Msg.MachineId), m.ExpectedRevision, action, req.Msg.DeviceCode, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.request", input, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, input.ID, input.Revision)
		if err != nil {
			return nil, err
		}
		if _, err = subscriptionInstallation(tx, input.Machine); err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			a.Subscription = &domain.SubscriptionState{}
		}
		state := a.Subscription
		if state.ServerQuotaActive() || state.Pending != nil || state.RecoveryRequired || a.Removal != nil || state.Observation != nil && state.Observation.Active() && action != domain.SubscriptionLogout {
			return nil, subscriptionDenied()
		}
		if action == domain.SubscriptionLogin && (a.Connection != nil || state.Generation != "" || state.Lease != nil) {
			return nil, domain.Fail(domain.Conflict, "The account already owns a login or lease.", "Complete its original logout and cleanup before a new login.")
		}
		if action != domain.SubscriptionLogin && (a.Connection == nil || state.Generation == "") {
			return nil, subscriptionDenied()
		}
		if state.ServerOperation != nil && state.ServerOperation.CleanupPhase == domain.SubscriptionCredentialCleanupConfirmed {
			// A fresh explicit Worker login supersedes the settled server attempt.
			// Its cleanup checkpoint cannot describe the new pending/native owner.
			state.ServerOperation = nil
		}
		state.Pending = &domain.SubscriptionOperation{ID: domain.ID(m.RequestId), Action: action, MachineID: input.Machine, Actor: actor, DeviceCode: input.DeviceCode, Phase: domain.SubscriptionQueued}
		if action == domain.SubscriptionLogout {
			// Deny new executions at acceptance. Existing ownership remains leased
			// until its original Worker reports native/file cleanup.
			a.Health = domain.AccountRevoked
			if state.Observation != nil && state.Observation.Phase == domain.SubscriptionObservationQueued {
				state.Observation.Phase = domain.SubscriptionObservationFailed
				state.Observation.ErrorCode = domain.Canceled
			}
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_operation_accepted", "account_id", input.ID, "operation_id", m.RequestId, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RequestSubscriptionResponse{Account: rpc.Resource(r), OperationId: m.RequestId, Replayed: result.Replayed}), nil
}

func (s *Service) CancelSubscription(ctx context.Context, req *connect.Request[pb.CancelSubscriptionRequest]) (*connect.Response[pb.CancelSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.cancelClaudeSubscription(ctx, req)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.cancel", disconnectAccountInput{domain.ID(m.Id), m.ExpectedRevision}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, domain.ID(m.Id), m.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.Pending == nil {
			return nil, domain.Fail(domain.Conflict, "No subscription operation is pending.", "Read current account status; cancellation never starts another operation.")
		}
		if a.Subscription.Pending.Action != domain.SubscriptionLogin {
			return nil, domain.Fail(domain.Unsupported, "Only an active login can be canceled.", "Let refresh or logout finish under exclusive ownership.")
		}
		if a.Subscription.Pending.Phase == domain.SubscriptionQueued {
			if o := a.Subscription.ServerOperation; o != nil && o.ID == a.Subscription.Pending.ID {
				o.State = domain.SubscriptionCanceled
			}
			a.Subscription.Pending = nil
		} else {
			a.Subscription.Pending.Canceled = true
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.CancelSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

func (s *Service) GetSubscriptionProgress(ctx context.Context, req *connect.Request[pb.GetSubscriptionProgressRequest]) (*connect.Response[pb.GetSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if matched, err := s.isClaudeSubscription(ctx, req.Msg.AccountId); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.getClaudeSubscriptionProgress(ctx, req)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if response, matched, err := s.serverSubscriptionProgress(ctx, req.Msg); matched || err != nil {
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(response), nil
	}
	var op *domain.SubscriptionOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, domain.ID(req.Msg.AccountId), 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.Pending == nil || string(a.Subscription.Pending.ID) != req.Msg.OperationId {
			return domain.Fail(domain.NotFound, "The original login presentation is unavailable.", "Read account status without starting another login.")
		}
		op = a.Subscription.Pending
		if a.Subscription.RecoveryRequired || (a.Subscription.Lease != nil && a.Subscription.Lease.Epoch != s.subscriptionServerEpoch()) {
			return subscriptionDenied()
		}
		return subscriptionActorValid(tx, op.Actor)
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	if actor != op.Actor {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	p := s.subscriptionProgress[op.ID]
	return connect.NewResponse(&pb.GetSubscriptionProgressResponse{Url: p.URL, UserCode: p.UserCode, Canceled: op.Canceled}), nil
}

func (s *Service) WatchSubscription(ctx context.Context, req *connect.Request[pb.WatchSubscriptionRequest], stream *connect.ServerStream[pb.WatchSubscriptionResponse]) error {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, c)
	}
	defer func() {
		if err := s.retainLostSubscriptionLeases(domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), false); err != nil {
			s.logger.Warn("subscription_owner_loss_unconfirmed", "code", domain.SafeError(err).Code)
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var records []store.Record
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			if err := currentInstance(tx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)); err != nil {
				return err
			}
			_, machine, err := activeMachine(tx, domain.ID(req.Msg.MachineId))
			if err != nil {
				return err
			}
			if !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
				return subscriptionDenied()
			}
			// A current adapter may wait for explicitly accepted lifecycle work
			// without an installed harness. Acceptance/Take still own authority.
			if !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
				if _, err := subscriptionInstallation(tx, domain.ID(req.Msg.MachineId)); err != nil {
					return err
				}
			}
			accounts, err := all(tx, domain.AccountKind)
			if err != nil {
				return err
			}
			for _, r := range accounts {
				a, err := store.Decode[domain.Account](r)
				if err != nil {
					return err
				}
				if a.SubscriptionService == domain.SubscriptionClaude && !slices.Contains(machine.WorkerCapabilities, domain.NativeClaudeSubscriptionsV1) || a.SubscriptionService == domain.SubscriptionChatGPT && !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) {
					continue
				}
				state := a.Subscription
				if state != nil && a.SubscriptionService == domain.SubscriptionClaude && state.RecoveryRequired && state.Lease != nil && state.Lease.MachineID == domain.ID(req.Msg.MachineId) {
					records = append(records, r)
					if len(records) == 4 {
						break
					}
					continue
				}
				if state != nil && state.Observation != nil && state.Observation.Phase == domain.SubscriptionObservationQueued && state.Observation.MachineID == domain.ID(req.Msg.MachineId) && !state.RecoveryRequired && (state.Lease == nil || state.Lease.Action == domain.SubscriptionExecute && state.Lease.InstanceID == domain.ID(req.Msg.InstanceId)) {
					records = append(records, r)
					if len(records) == 4 {
						break
					}
					continue
				}
				if state != nil && state.Pending != nil && state.Pending.MachineID == domain.ID(req.Msg.MachineId) && state.Pending.Phase == domain.SubscriptionQueued && state.Lease == nil && !state.RecoveryRequired {
					records = append(records, r)
					if len(records) == 4 {
						break
					}
				}
			}
			return nil
		})
		if err != nil {
			return rpc.Error(err, c)
		}
		for _, r := range records {
			if err := stream.Send(&pb.WatchSubscriptionResponse{Account: rpc.Resource(r)}); err != nil {
				return err
			}
		}
		if err := stream.Send(&pb.WatchSubscriptionResponse{}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) subscriptionLease(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, error) {
	r, a, err := subscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, err
	}
	if a.Subscription == nil || a.Subscription.Lease == nil {
		return r, a, subscriptionDenied()
	}
	l := a.Subscription.Lease
	actor, _ := domain.PrincipalFrom(ctx)
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID {
		return r, a, subscriptionDenied()
	}
	if l.ID != lease || l.MachineID != machine || l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, err
	}
	if _, _, err := activeMachine(tx, machine); err != nil {
		return r, a, err
	}
	return r, a, nil
}

func (s *Service) TakeSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("subscription_recovery_publication_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.takeClaudeSubscription(ctx, req)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	action := subscriptionAction(req.Msg.Action)
	input := struct {
		Account, Lease, Machine, Instance, Operation domain.ID
		Revision                                     uint64
		Action                                       domain.SubscriptionAction
		Actor                                        domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, action, actor}
	if input.Operation.Validate() != nil || action == "" {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	directExecution := false
	var forkRevision uint64
	result, err := s.Store.Mutate(ctx, input.Lease, "subscription.take", input, func(tx *store.Tx) (any, error) {
		// Take retains the original observation while another lease or metadata
		// update advances the account. Lifecycle authority is the exact still-queued
		// operation below; execution authority is its independently checked job.
		r, a, err := subscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		if action != domain.SubscriptionExecute && input.Revision > r.Revision {
			return nil, domain.Fail(domain.Conflict, "The account revision has not been observed.", "Retain the original queued operation and its observed revision.")
		}
		state := a.Subscription
		if state == nil || state.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		if state.ServerQuotaActive() || state.Lease != nil || state.ServerOperation != nil && state.ServerOperation.NativeStarted {
			return nil, domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		if action != domain.SubscriptionExecute {
			if _, err := subscriptionInstallation(tx, input.Machine); err != nil {
				return nil, err
			}
		}
		if action == domain.SubscriptionExecute {
			if state.Observation != nil && state.Observation.Active() {
				return nil, domain.Fail(domain.ResourceExhausted, "The selected account has a retained native observation.", "Wait for its original operation or explicitly reconcile the original reset-credit key.")
			}
			if state.Pending != nil && state.Pending.Action == domain.SubscriptionRefresh {
				return nil, domain.Fail(domain.ResourceExhausted, "The selected account is refreshing under exclusive ownership.", "Wait on this account's original operation without failover.")
			}
			if state.Pending != nil || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil || a.Connection == nil || state.Generation == "" {
				return nil, subscriptionDenied()
			}
			jr, err := tx.Get(domain.JobKind, input.Operation)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](jr)
			var execution domain.ExecutionJobInput
			if err != nil || jr.Revision != input.Revision || job.State != domain.JobClaimed || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID {
				return nil, subscriptionDenied()
			}
			switch job.Type {
			case domain.ExecuteSessionJob:
				if domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil {
					return nil, subscriptionDenied()
				}

			case domain.ForkSessionJob:
				var fork domain.ForkJobInput
				if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || fork.Purpose != domain.SidechatFork || fork.SubscriptionGeneration != state.Generation || fork.SourceSessionID != jr.SessionID {
					return nil, subscriptionDenied()
				}
				if err := validateForkAuthority(tx, fork); err != nil {
					return nil, err
				}
				execution = fork.SourceAssignment
				forkRevision = jr.Revision
			case domain.CompactSessionJob:
				var compact domain.SessionCompactionInput
				if domain.DecodeCompactionInput(job.Input, &compact) != nil || compact.Validate() != nil || compact.Version != 2 {
					return nil, subscriptionDenied()
				}
				sr, session, err := sessionRecord(tx, jr.SessionID)
				_, machine, machineErr := activeMachine(tx, input.Machine)
				if err != nil || machineErr != nil || !machineCapabilityContains(machine.WorkerCapabilities, domain.NativeSessionCompactionV1) || !machineCapabilityContains(machine.WorkerCapabilities, domain.CodexSessionCompactionV1) || session.CompactionJobID != jr.ID || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || !session.OwnsExecution(compact.Assignment) {
					return nil, subscriptionDenied()
				}
				if err := checkedExecutionSource(tx, sr, session, machine, compact.Assignment); err != nil {
					return nil, err
				}
				if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
					return nil, err
				}
				execution = compact.Assignment
			default:
				return nil, subscriptionDenied()
			}
			if execution.Version == 4 {
				_, machine, err := activeMachine(tx, input.Machine)
				if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1) || !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
					return nil, subscriptionDenied()
				}
				directExecution = true
			} else if _, err := subscriptionInstallation(tx, input.Machine); err != nil {
				return nil, err
			}

			if execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionChatGPT || execution.Configuration.Harness != domain.Codex {
				return nil, subscriptionDenied()
			}
			if canceled, err := tx.JobCancellationRequested(jr.ID); err != nil || canceled {
				return nil, subscriptionDenied()
			}
		} else if action == domain.SubscriptionQuota || action == domain.SubscriptionResetCredit {
			op := state.Observation
			if op == nil || op.ID != input.Operation || op.Action != action || op.MachineID != input.Machine || op.Phase != domain.SubscriptionObservationQueued || op.Generation != state.Generation || !quotaAccountReady(a) || state.Pending != nil || observationMachine(tx, input.Machine) != nil || subscriptionActorValid(tx, op.Actor) != nil {
				return nil, subscriptionDenied()
			}
		} else {
			op := state.Pending
			if op == nil || op.ID != input.Operation || op.Action != action || op.MachineID != input.Machine || op.Phase != domain.SubscriptionQueued || op.Canceled {
				return nil, subscriptionDenied()
			}
			if err := subscriptionActorValid(tx, op.Actor); err != nil {
				return nil, err
			}
			op.Phase = domain.SubscriptionClaimed
		}
		if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
			return nil, err
		}
		state.Lease = &domain.SubscriptionLease{ForkRevision: forkRevision, ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: action, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: state.Generation, StartedAt: time.Now().UTC()}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// A receipt never redistributes a secret. An uncertain first delivery retains
	// the lease and requires recovery rather than launching another native owner.
	if result.Replayed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	var installation domain.Installation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err == nil {
			generation = a.Subscription.Generation
			leaseRevision = a.Subscription.Lease.Revision
			// The v4 job already owns its immutable startup selection. Protected
			// bundle delivery cannot reintroduce discovery or version admission.
			if !directExecution {
				installation, err = subscriptionInstallation(tx, input.Machine)
			}
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var bundle []byte
	if generation != "" {
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		bundle, err = vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if _, _, err := subscription.Parse(bundle); err != nil {
			clear(bundle)
			return nil, rpc.Error(err, c)
		}
	}
	// Authorization after native I/O prevents revocation from winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil || a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		if action == domain.SubscriptionExecute {
			if !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil {
				return subscriptionDenied()
			}
			if canceled, err := tx.JobCancellationRequested(input.Operation); err != nil || canceled {
				return subscriptionDenied()
			}
		} else if action == domain.SubscriptionQuota || action == domain.SubscriptionResetCredit {
			op := a.Subscription.Observation
			if op == nil || op.Phase != domain.SubscriptionObservationQueued || op.Generation != generation || subscriptionActorValid(tx, op.Actor) != nil || !quotaAccountReady(a) {
				return subscriptionDenied()
			}
		} else {
			op := a.Subscription.Pending
			if op == nil || op.Canceled || subscriptionActorValid(tx, op.Actor) != nil {
				return subscriptionDenied()
			}
		}
		return nil
	})
	if err != nil {
		clear(bundle)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_lease_granted", "account_id", input.Account, "lease_id", input.Lease, "action", action)
	installationJSON, _ := json.Marshal(installation)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), Bundle: bundle, LeaseRevision: leaseRevision, InstallationJson: installationJSON}), nil
}

var subscriptionUserCode = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

func (s *Service) PublishSubscriptionProgress(ctx context.Context, req *connect.Request[pb.PublishSubscriptionProgressRequest]) (*connect.Response[pb.PublishSubscriptionProgressResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if matched, err := s.isClaudeSubscription(ctx, req.Msg.AccountId); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.publishClaudeSubscriptionProgress(ctx, req)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	u, err := url.Parse(req.Msg.Url)
	if err != nil || len(req.Msg.Url) > 8192 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Host != "auth.openai.com" && u.Host != "chatgpt.com") || (req.Msg.UserCode != "" && !subscriptionUserCode.MatchString(req.Msg.UserCode)) {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var op *domain.SubscriptionOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := s.subscriptionLease(ctx, tx, domain.ID(req.Msg.AccountId), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId))
		if err != nil {
			return err
		}
		op = a.Subscription.Pending
		if a.Subscription.RecoveryRequired || op == nil || op.Action != domain.SubscriptionLogin {
			return subscriptionDenied()
		}
		return subscriptionActorValid(tx, op.Actor)
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if s.subscriptionProgress == nil {
		s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
	}
	if !op.Canceled {
		s.subscriptionProgress[op.ID] = subscriptionProgress{URL: req.Msg.Url, UserCode: req.Msg.UserCode}
	}
	return connect.NewResponse(&pb.PublishSubscriptionProgressResponse{Canceled: op.Canceled}), nil
}

func (s *Service) FinishSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (response *connect.Response[pb.FinishSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("subscription_recovery_publication_unconfirmed", "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	defer clear(req.Msg.Bundle)
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if matched, err := s.isClaudeSubscription(ctx, m.Id); err != nil {
		return nil, rpc.Error(err, c)
	} else if matched {
		return s.finishClaudeSubscription(ctx, req)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(req.Msg.Bundle) > subscription.MaxBundle {
		return nil, rpc.Error(subscription.Invalid(), c)
	}
	input := struct {
		Account, Lease, Machine, Instance, Generation domain.ID
		Commitment                                    string
		Revision                                      uint64
		Cleanup, Refresh, Success                     bool
	}{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle), m.ExpectedRevision, req.Msg.CleanupConfirmed, req.Msg.RefreshConfirmed, req.Msg.Succeeded}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "subscription.finish", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var original domain.Account
	if !replayed {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			_, original, err = s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			if err == nil && original.Subscription.Lease.Action == domain.SubscriptionExecute {
				_, err = managedSidechatLease(tx, *original.Subscription.Lease)
			}
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		lease := state.Lease
		// Receipt replay above is read-only. A new finish cannot resolve an
		// ownership loss or touch its vault generations through this channel.
		if state.RecoveryRequired || state.Generation != input.Generation || lease.Generation != input.Generation || lease.Revision != input.Revision {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		owned = true
		usable := input.Success && input.Cleanup && (lease.Action != domain.SubscriptionRefresh || input.Refresh)
		if state.Pending != nil && state.Pending.Canceled {
			usable = false
		}
		var identityCommitment string
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// A failed execution may release only confirmed pre-native ownership.
		// Cleanup alone is insufficient: the Worker must return the exact unused
		// original bundle, independently compared with its immutable generation.
		preNativeFailure := lease.Action == domain.SubscriptionExecute && !input.Success && input.Cleanup && !input.Refresh && len(req.Msg.Bundle) != 0
		if preNativeFailure {
			old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			unchanged := bytes.Equal(old, req.Msg.Bundle)
			clear(old)
			if !unchanged {
				return nil, rpc.Error(subscriptionDenied(), c)
			}
		}
		if usable && lease.Action != domain.SubscriptionLogout {
			_, identity, err := subscription.Parse(req.Msg.Bundle)
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
			identityCommitment = s.accountCommitment(s.Identity.ServerID, identityJSON)
			clear(identityJSON)
			if state.IdentityCommitment != "" && identityCommitment != state.IdentityCommitment {
				return nil, rpc.Error(subscriptionDenied(), c)
			}
			if lease.Action == domain.SubscriptionLogin {
				err := s.Store.Read(ctx, func(tx *store.Tx) error {
					return uniqueSubscriptionIdentity(tx, input.Account, identityCommitment)
				})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			if state.Generation != "" {
				old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
				if err != nil {
					return nil, rpc.Error(err, c)
				}
				if lease.Action == domain.SubscriptionRefresh || subscription.Digest(old) != subscription.Digest(req.Msg.Bundle) {
					err = subscription.Refreshed(old, req.Msg.Bundle)
				}
				clear(old)
				if err != nil {
					return nil, rpc.Error(err, c)
				}
			}
			// Only the exact authenticated Worker lease can stage this freshly
			// produced native bundle. No owner/client import endpoint exists.
			if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: domain.ID(m.RequestId), Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		if input.Cleanup {
			// An uncertain failed operation retains its blocked original material
			// for recovery. Explicit logout still removes every owned reference.
			keep := state.Generation
			if usable && lease.Action != domain.SubscriptionLogout {
				keep = domain.ID(m.RequestId)
			} else if lease.Action == domain.SubscriptionLogout {
				keep = ""
			}
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, keep); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.finish", input, func(tx *store.Tx) (any, error) {
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			r, a, err := s.subscriptionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if lease.Action == domain.SubscriptionExecute {
				if _, err := managedSidechatLease(tx, *lease); err != nil {
					return nil, err
				}
			}
			if state.RecoveryRequired || state.Generation != input.Generation {
				return nil, subscriptionDenied()
			}
			if state.Pending != nil {
				if err := subscriptionActorValid(tx, state.Pending.Actor); err != nil {
					return nil, err
				}
				if state.Pending.Canceled {
					usable = false
				}
			}
			if usable && lease.Action == domain.SubscriptionLogin {
				if err := uniqueSubscriptionIdentity(tx, r.ID, identityCommitment); err != nil {
					return nil, err
				}
			}
			if !input.Cleanup || (!usable && lease.Action != domain.SubscriptionLogin && !preNativeFailure) {
				state.RecoveryRequired = true
				a.Health = domain.AccountFailed
				// Keep the lease and old generation denied. Neither an error nor
				// a process timeout establishes safe refresh-token redistribution.
			} else {
				state.Lease = nil
				if state.Pending != nil && state.Pending.ID == lease.OperationID {
					state.Pending = nil
				}
				if usable && lease.Action != domain.SubscriptionLogout {
					state.Generation = domain.ID(m.RequestId)
					state.IdentityCommitment = identityCommitment
					state.OwnerMachineID = lease.MachineID
					if state.Observation != nil && state.Observation.Generation == input.Generation {
						if state.Observation.Phase == domain.SubscriptionObservationQueued {
							state.Observation.Phase = domain.SubscriptionObservationFailed
							state.Observation.ErrorCode = domain.Conflict
						} else if state.Observation.Phase == domain.SubscriptionObservationSending {
							if state.Observation.Action == domain.SubscriptionQuota {
								// Quota reads consume nothing. Joined owner cleanup settles
								// a lost publication as failure, preserving last good values
								// without blocking future reads or native execution.
								state.Observation.Phase = domain.SubscriptionObservationFailed
								state.Observation.ErrorCode = domain.Unavailable
								state.QuotaState = domain.ObservationFailed
							} else {
								state.Observation.Phase = domain.SubscriptionObservationUncertain
							}
						}
					}
					if a.Connection == nil {
						a.Connection = &domain.AccountConnection{ID: domain.ID(m.RequestId), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
						a.Health = domain.AccountReady
					}
					// A queued logout may be canceled by initiator revocation while
					// this execution finishes. Write-back cannot undo its revocation.
					if state.Pending == nil && a.Health != domain.AccountRevoked {
						a.Health = domain.AccountReady
					}
				} else if lease.Action == domain.SubscriptionLogout {
					a.Connection = nil
					a.Health = domain.AccountDisconnected
					a.Validation = nil
					a.Catalog = nil
					a.Quota = nil
					a.ConfirmedExhausted = false
					state.Generation = ""
					state.IdentityCommitment = ""
					state.ResetCredits = nil
					state.QuotaObservedAt = nil
					state.QuotaState = domain.ObservationUnknown
					state.SpendControlReached = nil
					state.SpendControlObservedAt = nil
					if state.Observation != nil && (state.Observation.Phase == domain.SubscriptionObservationSending || state.Observation.Phase == domain.SubscriptionObservationUncertain) {
						state.Observation.Phase = domain.SubscriptionObservationRetiredUncertain
					}
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}

			receipt := accountReceipt{ID: r.ID}
			if usable && input.Cleanup && lease.Action == domain.SubscriptionExecute && lease.ForkRevision != 0 {
				jr, err := tx.Get(domain.JobKind, lease.OperationID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](jr)
				if err != nil {
					return nil, err
				}
				if job.Type == domain.ForkSessionJob {
					var fork domain.ForkJobInput
					if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || fork.Purpose != domain.SidechatFork || fork.SubscriptionGeneration != input.Generation || job.State != domain.JobClaimed || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
						return nil, subscriptionDenied()
					}
					receipt.ManagedSidechat = &domain.SubscriptionForkFinish{JobRevision: jr.Revision, Job: jr.ID, Account: r.ID, Machine: lease.MachineID, Instance: lease.InstanceID, Device: lease.DeviceID, Generation: input.Generation, Finish: domain.ID(m.RequestId)}
				}
			}
			return receipt, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if original.Subscription.Pending != nil {
			delete(s.subscriptionProgress, original.Subscription.Pending.ID)
		}
		// Cancellation can win during vault staging. Take remains blocked by the
		// account gate until the unpublished new generation is tombstoned too.
		if !usable && input.Cleanup {
			keep := original.Subscription.Generation
			if lease.Action == domain.SubscriptionLogout {
				keep = ""
			}
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, keep); err != nil {
				// The owned-error defer records recovery after releasing accountGate.
				return nil, rpc.Error(err, c)
			}
		}
	}
	// Ownership mutation and the last vault cleanup are settled. A canceled
	// response-resource read cannot turn that committed receipt into recovery.
	owned = false
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		s.logger.WarnContext(ctx, "subscription_completion_presentation_unavailable", "account_id", input.Account, "lease_id", input.Lease, "replayed", replayed, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	// Unreferenced staged/old generations stay vault-owned and are never granted.
	// The explicit logout cleanup removes every owned reference before success.
	s.logger.InfoContext(ctx, "subscription_operation_finished", "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}

func uniqueSubscriptionIdentity(tx *store.Tx, owner domain.ID, identity string) error {
	accounts, err := all(tx, domain.AccountKind)
	if err != nil {
		return err
	}
	for _, other := range accounts {
		a, err := store.Decode[domain.Account](other)
		if err != nil {
			return err
		}
		if other.ID != owner && a.Subscription != nil && a.Subscription.IdentityCommitment == identity {
			return domain.Fail(domain.Conflict, "This provider account already has a managed owner.", "Use that account's exclusive connection instead of duplicating its credentials.")
		}
	}
	return nil
}

func cleanupSubscriptionReferences(ctx context.Context, vault accountSecrets, owner, keep domain.ID) error {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return err
	}
	if len(refs) > 256 {
		return subscriptionDenied()
	}
	for _, ref := range refs {
		if ref.Owner != owner || ref.Purpose != credentials.AccountLogin {
			return subscriptionDenied()
		}
		if ref.ID != keep {
			if err := vault.Delete(ctx, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) markSubscriptionRecovery(id domain.ID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var pending domain.ID
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.recovery-required", id, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil {
			return nil, subscriptionDenied()
		}
		if a.Subscription.Pending != nil {
			pending = a.Subscription.Pending.ID
		}
		a.Subscription.RecoveryRequired = true
		a.Health = domain.AccountFailed
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err == nil {
		s.clearClaudeLoginInput(pending)
	}
	return err
}

// Missing native/write-back acknowledgment never releases a lease, including
// across server restarts and Worker replacement. This denial publishes no key.
func (s *Service) retainLostSubscriptionLeases(machine, instance domain.ID, executionOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Serialize recovery publication and cache invalidation with progress reads.
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var presentations []domain.ID
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.lost-owner", struct {
		Machine, Instance domain.ID
		ExecutionOnly     bool
		Epoch             domain.ID
	}{machine, instance, executionOnly, s.subscriptionServerEpoch()}, func(tx *store.Tx) (any, error) {
		accounts, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		count := 0
		for _, r := range accounts {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			state := a.Subscription
			if state == nil || state.Lease == nil {
				continue
			}
			lease := state.Lease
			if machine == "" {
				if lease.Epoch == s.subscriptionServerEpoch() {
					continue
				}
			} else if lease.MachineID != machine || lease.InstanceID != instance || (lease.Action == domain.SubscriptionExecute) != executionOnly {
				continue
			}
			if state.Pending != nil {
				presentations = append(presentations, state.Pending.ID)
			}
			if state.RecoveryRequired {
				continue
			}
			state.RecoveryRequired = true
			a.Health = domain.AccountFailed
			if err := cancelAccountExecutions(tx, r.ID); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			count++
		}
		return struct{ Count int }{count}, nil
	})
	if err == nil {
		for _, operation := range presentations {
			s.clearClaudeLoginInput(operation)
		}
	}
	return err
}

// managedSidechatLease checks original Fork ownership before vault staging and
// again inside protected Finish. Ordinary execution and compaction keep their
// existing Finish behavior; a Sidechat cannot borrow those jobs' authority.
func managedSidechatLease(tx *store.Tx, lease domain.SubscriptionLease) (*domain.ForkJobInput, error) {
	if lease.ForkRevision == 0 {
		return nil, nil
	}
	jr, err := tx.Get(domain.JobKind, lease.OperationID)
	if err != nil {
		return nil, err
	}
	job, err := store.Decode[domain.Job](jr)
	if err != nil {
		return nil, err
	}
	if job.Type != domain.ForkSessionJob {
		return nil, nil
	}
	var fork domain.ForkJobInput
	if domain.Decode(job.Input, &fork) != nil || fork.Validate() != nil || fork.Purpose != domain.SidechatFork || fork.SubscriptionGeneration != lease.Generation || lease.ForkRevision == 0 || jr.Revision != lease.ForkRevision || job.State != domain.JobClaimed || job.MachineID != lease.MachineID || job.InstanceID != lease.InstanceID || job.AssignedDeviceID != lease.DeviceID {
		return nil, subscriptionDenied()
	}
	if err := validateForkAuthority(tx, fork); err != nil {
		return nil, err
	}
	return &fork, nil
}
