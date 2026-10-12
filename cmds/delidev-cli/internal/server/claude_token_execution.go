// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func managedClaudeInstallation(tx *store.Tx, machine domain.ID) (domain.Installation, error) {
	_, m, e := activeMachine(tx, machine)
	if e != nil {
		return domain.Installation{}, e
	}
	if !slices.Contains(m.WorkerCapabilities, domain.ManagedClaudeTokensV1) {
		return domain.Installation{}, domain.Fail(domain.Unsupported, "This Runner Device cannot execute server-managed Claude accounts.", "Update the Runner Device; no token was granted.")
	}
	return claudeSubscriptionInstallation(tx, machine)
}
func (s *Service) managedClaudeLease(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID, recovery bool) (store.Record, domain.Account, error) {
	r, a, e := claudeSubscriptionAccount(tx, id, 0)
	if e != nil {
		return r, a, e
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st == nil || !st.ManagedClaudeToken || st.Lease == nil {
		return r, a, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != machine || l.ID != lease || l.MachineID != machine || l.InstanceID != instance || l.Action != domain.SubscriptionExecute || l.Generation != st.Generation || !recovery && (st.RecoveryRequired || l.Epoch != s.subscriptionServerEpoch()) {
		return r, a, subscriptionDenied()
	}
	if !recovery {
		if e = currentInstance(tx, machine, instance); e != nil {
			return r, a, e
		}
		_, e = managedClaudeInstallation(tx, machine)
	}
	return r, a, e
}
func (s *Service) takeClaudeToken(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId) != nil || subscriptionAction(req.Msg.Action) != domain.SubscriptionExecute {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	input := struct {
		Account, Lease, Machine, Instance, Operation domain.ID
		Revision                                     uint64
		Actor                                        domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, actor}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, input.Lease, "claude.token.take", input, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, input.Account, 0)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st == nil || !st.ManagedClaudeToken || st.RecoveryRequired || st.Pending != nil || st.ServerOperation != nil && st.ServerOperation.NativeStarted || st.Generation == "" || a.Connection == nil || a.Health != domain.AccountReady || !a.Enabled || a.Removal != nil {
			return nil, subscriptionDenied()
		}
		if st.Lease != nil {
			return nil, domain.Fail(domain.ResourceExhausted, "This Claude account has an exclusive execution lease.", "Wait for original execution cleanup before using another Runner Device.")
		}
		if currentInstance(tx, input.Machine, input.Instance) != nil {
			return nil, subscriptionDenied()
		}
		if _, e = managedClaudeInstallation(tx, input.Machine); e != nil {
			return nil, e
		}
		jr, e := tx.Get(domain.JobKind, input.Operation)
		if e != nil {
			return nil, e
		}
		j, e := store.Decode[domain.Job](jr)
		var execution domain.ExecutionJobInput
		if e != nil || jr.Revision != input.Revision || j.Type != domain.ExecuteSessionJob || j.State != domain.JobClaimed || j.MachineID != input.Machine || j.InstanceID != input.Instance || j.AssignedDeviceID != actor.DeviceID || domain.Decode(j.Input, &execution) != nil || execution.Validate() != nil || execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionClaude || execution.Configuration.Harness != domain.ClaudeCode {
			return nil, subscriptionDenied()
		}
		if canceled, e := tx.JobCancellationRequested(jr.ID); e != nil || canceled {
			return nil, subscriptionDenied()
		}
		if e := tx.WorkerUpdateAdmission(input.Machine); e != nil {
			return nil, e
		}
		st.Lease = &domain.SubscriptionLease{ID: input.Lease, OperationID: input.Operation, Revision: r.Revision + 1, Action: domain.SubscriptionExecute, MachineID: input.Machine, InstanceID: input.Instance, DeviceID: actor.DeviceID, Epoch: s.subscriptionServerEpoch(), Generation: st.Generation, StartedAt: time.Now().UTC()}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, e
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if result.Replayed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	granted := false
	defer func() {
		if !granted {
			_, _ = s.Store.Mutate(context.WithoutCancel(ctx), domain.NewID(), "claude.token.delivery.recovery", input, func(tx *store.Tx) (any, error) {
				r, a, e := claudeSubscriptionAccount(tx, input.Account, 0)
				if e != nil {
					return nil, e
				}
				if a.Subscription.Lease == nil || a.Subscription.Lease.ID != input.Lease {
					return nil, subscriptionDenied()
				}
				a.Subscription.RecoveryRequired = true
				a.Health = domain.AccountFailed
				_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return struct{}{}, e
			})
		}
	}()
	var a domain.Account
	var installation domain.Installation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err = s.managedClaudeLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
		if err == nil {
			installation, err = managedClaudeInstallation(tx, input.Machine)
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
	st := a.Subscription
	raw, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: st.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if _, err = subscription.ParseClaudeToken(raw, s.Identity.ServerID, input.Account, st.Generation); err != nil || st.IdentityCommitment != s.accountCommitment(st.Generation, raw) {
		clear(raw)
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	installed, _ := json.Marshal(installation)
	s.logger.InfoContext(ctx, "claude_token_execution_granted", "lease_id", input.Lease, "machine_id", input.Machine, "generation_id", st.Generation)
	granted = true
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), LeaseRevision: st.Lease.Revision, GenerationId: string(st.Generation), InstallationJson: installed, Bundle: raw}), nil
}
func (s *Service) finishClaudeToken(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	msg := req.Msg
	defer clear(msg.Bundle)
	m := msg.Mutation
	c := req.Header().Get(rpc.CorrelationHeader)
	if workerActor(ctx, msg.MachineId, msg.InstanceId) != nil || msg.NativeIdentity != nil || msg.RefreshConfirmed {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	input := struct {
		Account, Lease, Machine, Instance, Generation domain.ID
		Revision                                      uint64
		Cleanup, Success                              bool
		Digest                                        string
	}{domain.ID(m.Id), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId), domain.ID(msg.GenerationId), m.ExpectedRevision, msg.CleanupConfirmed, msg.Succeeded, subscription.Digest(msg.Bundle)}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	// Returned bytes prove unchanged possession, never authorize generation rotation.
	// Cleanup-only reconciliation of an interrupted delivery supplies no bytes and
	// retains account failure, rather than granting readiness after a restart.
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "claude.token.execution.finish", input, func(tx *store.Tx) (any, error) {
		_, current, e := claudeSubscriptionAccount(tx, input.Account, 0)
		if e != nil {
			return nil, e
		}
		cleanupRecovery := current.Subscription != nil && current.Subscription.RecoveryRequired && input.Cleanup && !input.Success && len(msg.Bundle) == 0
		r, a, e := s.managedClaudeLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, cleanupRecovery)
		if e != nil {
			return nil, e
		}
		if !cleanupRecovery {
			original, e := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: input.Generation, Purpose: credentials.AccountLogin})
			defer clear(original)
			if e != nil || !bytes.Equal(original, msg.Bundle) || a.Subscription.IdentityCommitment != s.accountCommitment(input.Generation, original) {
				return nil, subscriptionDenied()
			}
		}
		st := a.Subscription
		l := st.Lease
		if l.Revision != input.Revision || l.Generation != input.Generation {
			return nil, subscriptionDenied()
		}
		if !input.Cleanup {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
		} else {
			st.Lease = nil
			st.RecoveryRequired = false
			if !input.Success || cleanupRecovery {
				a.Health = domain.AccountFailed
			}
		}
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, e
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "claude_token_execution_finished", "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", result.Replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
