// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
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

// managedClaudeExecutionLease validates one exclusive original execution lease
// of a server-token account. Only the authenticated Worker device and instance
// that took it can use it, and only while its server epoch is current.
func (s *Service) managedClaudeExecutionLease(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID) (store.Record, domain.Account, error) {
	r, a, err := managedClaudeAccount(tx, id, 0)
	if err != nil {
		return r, a, err
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st.Lease == nil {
		return r, a, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != machine || l.ID != lease || l.MachineID != machine || l.InstanceID != instance || l.Action != domain.SubscriptionExecute || l.Epoch != s.subscriptionServerEpoch() {
		return r, a, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, err
	}
	return r, a, nil
}

func (s *Service) takeManagedClaude(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	action := subscriptionAction(req.Msg.Action)
	input := struct {
		Account, Lease, Machine, Instance, Job domain.ID
		Revision                               uint64
		Actor                                  domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, actor}
	if input.Job.Validate() != nil || action != domain.SubscriptionExecute {
		// Login and logout belong to the server; a Worker never takes them.
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(input.Account); err != nil {
				s.logger.Warn("claude_managed_recovery_unconfirmed", "account_id", input.Account, "code", domain.SafeError(err).Code)
			}
		}
	}()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, input.Lease, "claude.managed.take", input, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		if st.Lease != nil || st.ServerOperation != nil && st.ServerOperation.Active() {
			return nil, domain.Fail(domain.ResourceExhausted, "This Claude account is exclusively owned by another operation.", "Wait for that original operation and its cleanup; no other account or Runner is substituted.")
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		_, machine, err := activeMachine(tx, input.Machine)
		if err != nil {
			return nil, err
		}
		// A legacy-only Worker never receives the token: only the independently
		// negotiated adapter capability makes it eligible.
		if !slices.Contains(machine.WorkerCapabilities, domain.ManagedClaudeExecutionV1) {
			return nil, domain.Fail(domain.Unsupported, "The selected Runner Device cannot execute server-owned Claude accounts.", "Update the Runner Device before sending input.")
		}
		if st.Pending != nil || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil || a.Connection == nil || st.Generation == "" {
			return nil, subscriptionDenied()
		}
		jr, err := tx.Get(domain.JobKind, input.Job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		var execution domain.ExecutionJobInput
		if err != nil || jr.Revision != input.Revision || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID || domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil || execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionClaude || execution.Configuration.Harness != domain.ClaudeCode {
			return nil, subscriptionDenied()
		}
		if canceled, err := tx.JobCancellationRequested(jr.ID); err != nil || canceled {
			return nil, subscriptionDenied()
		}
		if err := tx.WorkerUpdateAdmission(input.Machine); err != nil {
			return nil, err
		}
		st.Lease = &domain.SubscriptionLease{ID: input.Lease, OperationID: input.Job, Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: st.Generation, StartedAt: time.Now().UTC()}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// A receipt never redistributes a secret. An uncertain first delivery retains
	// the lease and requires original-Worker recovery instead of a second grant.
	if result.Replayed {
		// The first response may have been lost after the lease committed. Fence
		// that exact unfinished lease for original-Worker cleanup; the envelope is
		// never redistributed and a finished or foreign lease is left untouched.
		if err := s.fenceReplayedManagedTake(ctx, input.Account, input.Lease, input.Machine, input.Instance, actor); err != nil {
			s.logger.WarnContext(ctx, "claude_managed_replay_fence_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
		}
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.managedClaudeExecutionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err == nil {
			generation = a.Subscription.Generation
			leaseRevision = a.Subscription.Lease.Revision
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	envelope, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// The envelope must be exactly this server, account and settled generation.
	token, err := subscription.OpenClaudeToken(envelope, s.Identity.ServerID, input.Account, generation)
	if err != nil {
		clear(envelope)
		return nil, rpc.Error(err, c)
	}
	token.Clear()
	// Authorization after vault I/O prevents revocation from winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, err := s.managedClaudeExecutionLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance)
		if err != nil || a.Subscription.RecoveryRequired || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil {
			return subscriptionDenied()
		}
		if canceled, err := tx.JobCancellationRequested(input.Job); err != nil || canceled {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		clear(envelope)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_managed_lease_granted", "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Job)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), LeaseRevision: leaseRevision, Bundle: envelope}), nil
}

// fenceReplayedManagedTake marks the still-held original lease recovery-required
// so WatchSubscription offers it to the Worker that took it. The caller holds the
// account gate. Any other lease state, device or instance changes nothing.
func (s *Service) fenceReplayedManagedTake(ctx context.Context, id, lease, machine, instance domain.ID, actor domain.Principal) error {
	fenced := false
	_, err := s.Store.Mutate(ctx, domain.NewID(), "claude.managed.take-replay-fence", struct{ Account, Lease domain.ID }{id, lease}, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		l := st.Lease
		if l == nil || st.RecoveryRequired || l.ID != lease || l.Action != domain.SubscriptionExecute || l.MachineID != machine || l.InstanceID != instance || l.DeviceID != actor.DeviceID {
			return struct{}{}, nil
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		fenced = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil && fenced {
		s.logger.WarnContext(ctx, "claude_managed_replayed_take_fenced", "account_id", id, "lease_id", lease)
	}
	return err
}

// finishManagedClaudeExecution releases the original lease only on joined
// cleanup evidence. An unconfirmed or unauthenticated report fences the account.
func (s *Service) finishManagedClaudeExecution(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	msg := req.Msg
	defer clear(msg.Bundle)
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(msg.Bundle) != 0 || msg.RefreshConfirmed || msg.NativeIdentity != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		Account, Lease, Machine, Instance, Generation domain.ID
		Revision                                      uint64
		Cleanup, Success                              bool
	}{domain.ID(m.Id), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId), domain.ID(msg.GenerationId), m.ExpectedRevision, msg.CleanupConfirmed, msg.Succeeded}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.managed.finish-execution", input, func(tx *store.Tx) (any, error) {
		r, a, err := managedClaudeAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		actor, _ := domain.PrincipalFrom(ctx)
		l := st.Lease
		if l == nil || l.ID != input.Lease || l.MachineID != input.Machine || l.InstanceID != input.Instance || actor.DeviceID != l.DeviceID || l.Generation != input.Generation || st.Generation != input.Generation || l.Revision != input.Revision {
			return nil, subscriptionDenied()
		}
		// Only the original device can cross a restart or recovery fence, and then
		// only with cleanup evidence: never authentication or a new grant.
		recoveryCleanup := st.RecoveryRequired
		if recoveryCleanup && (!input.Cleanup || input.Success) {
			return nil, subscriptionDenied()
		}
		if !recoveryCleanup {
			if l.Epoch != s.subscriptionServerEpoch() {
				return nil, subscriptionDenied()
			}
			if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
				return nil, err
			}
		}
		if !input.Cleanup {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
		} else {
			st.Lease = nil
			st.RecoveryRequired = false
			if o := st.ServerOperation; o != nil && o.Action == domain.SubscriptionLogout && o.Active() && st.Pending != nil && st.Pending.ID == o.ID {
				// An accepted logout queued behind this lease keeps readiness revoked.
				// A claimable operation of the current epoch is admitted by the next
				// scan; an operation fenced by a restart or an earlier fence is not
				// claimable, so the recovery fence stays until recovery completes it.
				a.Health = domain.AccountRevoked
				if o.State != domain.SubscriptionPreparing || o.NativeStarted || o.Epoch != s.subscriptionServerEpoch() {
					st.RecoveryRequired = true
				} else {
					// The logout window starts when the original lease clears, not at
					// acceptance, so a long execution cannot expire the queued logout.
					// Both ends move together to keep the persisted window within its
					// bound.
					now := time.Now().UTC()
					o.StartedAt = now
					o.ExpiresAt = now.Add(managedClaudeOperationWindow)
				}
			} else if a.Health != domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_managed_lease_finished", "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", result.Replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
