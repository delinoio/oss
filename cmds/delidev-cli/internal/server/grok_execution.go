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

// grokExecutionPhase is the phase every structured record of a managed Grok
// execution lease carries. Records never contain a credential, a path or an
// identity value.
const grokExecutionPhase = "EXECUTION"

// Managed Grok execution. One execute Take grants the exclusive singleton
// account lease of an `ExecuteSessionJob` whose frozen assignment selected this
// Grok subscription account. The sealed `subscription.GrokBundle` envelope moves
// to the Worker only through this protected Take and returns only through the
// protected Finish of the same lease (a refreshed bundle after identity
// verification, or nothing). The lease is keyed by the job, not by a pending
// account operation, and it is exclusive: a second session on the same account
// receives the definite busy refusal and the Worker retries the Take, so two
// sessions on one account serialize. Sessions on different accounts hold
// different leases and run in parallel.
//
// The whole path is unreachable in the default build: the Take requires Worker
// capability 21, which AttachWorker admits only behind grokManagedExecutionAdvertised.

// grokExecutionLeaseView resolves the exact Execute lease for the authenticated
// Worker. Every owner binding (Runner, device, instance) is checked. A recovery
// cleanup crosses a server restart or a Worker replacement, so with stale set
// the server epoch and the current Worker instance are not compared: the lease
// stays bound to the original Runner and Worker device, and the caller admits
// only a cleanup-only report in that case.
func (s *Service) grokExecutionLeaseView(ctx context.Context, tx *store.Tx, id, lease, machine, instance domain.ID, stale bool) (store.Record, domain.Account, domain.SubscriptionLease, error) {
	r, a, err := grokSubscriptionAccount(tx, id, 0)
	if err != nil {
		return r, a, domain.SubscriptionLease{}, err
	}
	st := a.Subscription
	actor, _ := domain.PrincipalFrom(ctx)
	if st == nil || st.Lease == nil || st.Lease.ID != lease || st.Lease.Shared || len(st.Members) != 0 || st.Lease.Action != domain.SubscriptionExecute {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	l := st.Lease
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != machine || l.MachineID != machine || l.InstanceID != instance {
		return r, a, domain.SubscriptionLease{}, subscriptionDenied()
	}
	if !stale {
		if l.Epoch != s.subscriptionServerEpoch() {
			return r, a, *l, subscriptionDenied()
		}
		if err := currentInstance(tx, machine, instance); err != nil {
			return r, a, *l, err
		}
	}
	return r, a, *l, nil
}

// grokExecuteLease reports whether the account currently holds the named Execute
// lease. A lease that is gone or of another action returns false, so a replayed
// Finish of a released lease reaches the receipt replay of the shared Finish.
func (s *Service) grokExecuteLease(ctx context.Context, account, lease string) (bool, error) {
	if domain.ID(account).Validate() != nil || domain.ID(lease).Validate() != nil {
		return false, nil
	}
	held := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, domain.ID(account), 0)
		if err != nil {
			return err
		}
		held = a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok && a.Subscription != nil && a.Subscription.Lease != nil && a.Subscription.Lease.ID == domain.ID(lease) && a.Subscription.Lease.Action == domain.SubscriptionExecute
		return nil
	})
	return held, err
}

func (s *Service) takeGrokExecution(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (response *connect.Response[pb.TakeSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	input := struct {
		Account, Lease, Machine, Instance, Job domain.ID
		Revision                               uint64
		Actor                                  domain.Principal
	}{domain.ID(m.Id), domain.ID(m.RequestId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.OperationId), m.ExpectedRevision, actor}
	if input.Job.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned := false
	defer func() {
		if returned != nil && owned {
			if err := s.markSubscriptionRecovery(input.Account); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "phase", grokExecutionPhase, "account_id", input.Account, "operation_id", input.Job, "code", domain.SafeError(err).Code)
			}
		}
	}()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, input.Lease, "grok.execution.take", input, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, input.Account, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.RecoveryRequired {
			return nil, subscriptionDenied()
		}
		// An accepted server-owned logout holds the account: new work is refused
		// with the typed unavailable problem, never silently queued behind it.
		if grokAccountLoggingOut(a) {
			s.logger.WarnContext(ctx, "grok_subscription_take_refused_logging_out", "phase", grokLogoutPhase, "account_id", input.Account, "operation_id", input.Job, "action", domain.SubscriptionExecute)
			return nil, grokLoggingOutProblem()
		}
		if st.Lease != nil || st.ServerOperation != nil && st.ServerOperation.Active() || st.ExclusiveServerObservationActive() {
			return nil, domain.Fail(domain.ResourceExhausted, "The selected account is exclusively leased.", "Wait for its original cleanup; never switch accounts automatically.")
		}
		if err := currentInstance(tx, input.Machine, input.Instance); err != nil {
			return nil, err
		}
		_, machine, err := activeMachine(tx, input.Machine)
		if err != nil {
			return nil, err
		}
		// A Worker without the independently negotiated managed-Grok capability
		// never receives the bundle.
		if !slices.Contains(machine.WorkerCapabilities, domain.ManagedGrokSubscriptionsV1) {
			return nil, grokUnsupported()
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
		if err != nil || jr.Revision != input.Revision || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != input.Machine || job.InstanceID != input.Instance || job.AssignedDeviceID != actor.DeviceID || domain.Decode(job.Input, &execution) != nil || execution.Validate() != nil || execution.AccountID != r.ID || execution.ConnectionID != a.Connection.ID || !execution.Configuration.Subscription || execution.Configuration.SubscriptionService != domain.SubscriptionGrok || execution.Configuration.Harness != domain.GrokBuild {
			return nil, subscriptionDenied()
		}
		// Grok 1.0.41 records, version-1 results and any continuation keep their
		// attribution but acquire no resume authority: only a first input on the
		// same account and Worker may hold a managed lease.
		if execution.Continuation != nil || execution.Fork != nil {
			return nil, subscriptionDenied()
		}
		if execution.Version != 4 {
			// An inspected assignment needs the verified subscription installation.
			// A direct-startup (version 4) assignment is verified by the Worker's own
			// executable identity and the version its native process reports.
			if _, err := grokSubscriptionInstallation(tx, input.Machine); err != nil {
				return nil, err
			}
		} else if !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
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
		if err := s.fenceReplayedGrokTake(ctx, input.Account, input.Lease, input.Job, input.Machine, input.Instance, domain.SubscriptionExecute, actor); err != nil {
			s.logger.WarnContext(ctx, "grok_subscription_replay_fence_unconfirmed", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Job, "code", domain.SafeError(err).Code)
		}
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	owned = true
	var generation domain.ID
	var leaseRevision uint64
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, _, view, err := s.grokExecutionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
		if err == nil {
			generation, leaseRevision = view.Generation, view.Revision
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
	bundle, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	// Only this server's exact, unrebound envelope may cross the boundary.
	opened, err := subscription.OpenGrokBundle(bundle, s.Identity.ServerID, input.Account, generation)
	if err != nil {
		clear(bundle)
		s.logger.WarnContext(ctx, "grok_subscription_bundle_invalid", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Job)
		return nil, rpc.Error(err, c)
	}
	opened.Clear()
	// Authorization after vault I/O prevents revocation, cancellation or a logout
	// from winning during Get.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, a, _, err := s.grokExecutionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, false)
		if err != nil || a.Subscription.RecoveryRequired || !a.Enabled || a.Health != domain.AccountReady || a.Removal != nil || grokAccountLoggingOut(a) {
			return subscriptionDenied()
		}
		if canceled, err := tx.JobCancellationRequested(input.Job); err != nil || canceled {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		clear(bundle)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_lease_granted", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "operation_id", input.Job, "action", domain.SubscriptionExecute, "bundle_delivered", true)
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(input.Lease), GenerationId: string(generation), Bundle: bundle, LeaseRevision: leaseRevision}), nil
}

// grokCredentialUsable reports whether the account's settled generation still
// opens as exactly this server's Grok envelope. The secret is cleared at once
// and every failure counts as unusable, so readiness is restored only on
// positive proof.
func (s *Service) grokCredentialUsable(ctx context.Context, id, generation domain.ID) bool {
	vault, err := s.secrets()
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_credential_check_failed", "phase", grokExecutionPhase, "account_id", id, "code", domain.SafeError(err).Code)
		return false
	}
	envelope, err := vault.Get(ctx, credentials.Ref{Owner: id, ID: generation, Purpose: credentials.AccountLogin})
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_credential_check_failed", "phase", grokExecutionPhase, "account_id", id, "code", domain.SafeError(err).Code)
		return false
	}
	defer clear(envelope)
	opened, err := subscription.OpenGrokBundle(envelope, s.Identity.ServerID, id, generation)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_credential_check_failed", "phase", grokExecutionPhase, "account_id", id, "code", domain.SafeError(err).Code)
		return false
	}
	opened.Clear()
	return true
}

// finishGrokExecution settles one Execute lease.
//
//   - Success with cleanup confirmed and no bundle: the credential came back
//     unchanged; the lease is released.
//   - Success with cleanup confirmed and a bundle (refresh confirmed): the native
//     process refreshed the credential. The bundle must open for the generation
//     this Finish creates, carry the account's identity and pass
//     subscription.GrokRefreshed against the stored generation; it is staged in
//     the vault and published atomically with the release.
//   - Anything else with cleanup confirmed (the Worker could not verify the file):
//     the lease is kept and the account fenced for recovery. The same Worker then
//     reports a cleanup-only recovery Finish.
//   - Cleanup not confirmed: the lease is kept, the account is fenced and only
//     the original owner may report again. After a server restart (the lease
//     epoch is no longer current) that report is cleanup-only as well.
//   - A recovery fence (server restart or lost Worker) accepts only a cleanup-only
//     report from the original Runner and Worker device: it never carries a bundle
//     or success, and readiness returns only if the stored generation still opens.
func (s *Service) finishGrokExecution(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (response *connect.Response[pb.FinishSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	owned := false
	// replaced reports that the machine's current Worker instance is no longer
	// the lease owner. The asynchronous lost-Worker fence may not have run yet,
	// so Finish decides this itself instead of waiting for it, and it persists
	// that fence itself when it refuses a report because of the replacement.
	replaced := false
	defer func() {
		if returned == nil {
			return
		}
		fenced := false
		if replaced {
			// The same fence the lost-Worker callback records, so a refused report
			// never leaves the lease held without the recovery obligation (new Takes
			// would stay busy while the recovery watch ignored the unfenced lease).
			if err := s.retainLostSubscriptionLeases(domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), true); err != nil {
				s.logger.Warn("grok_subscription_replacement_fence_unconfirmed", "phase", grokExecutionPhase, "account_id", m.Id, "lease_id", req.Msg.LeaseId, "code", domain.SafeError(err).Code)
			} else {
				fenced = true
			}
		}
		if owned && !fenced {
			if err := s.markSubscriptionRecovery(domain.ID(m.Id)); err != nil {
				s.logger.Warn("grok_subscription_recovery_unconfirmed", "phase", grokExecutionPhase, "account_id", m.Id, "code", domain.SafeError(err).Code)
			}
		}
	}()
	input := subscriptionFinishInput{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle), m.ExpectedRevision, req.Msg.CleanupConfirmed, req.Msg.RefreshConfirmed, req.Msg.Succeeded, domain.SubscriptionFinishLegacy}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		var original domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			_, original, _, err = s.grokExecutionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, true)
			if err != nil {
				return err
			}
			replaced = currentInstance(tx, input.Machine, input.Instance) != nil
			return nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		lease := state.Lease
		// A fence set by a restart or a lost Worker is crossed only by a cleanup-only
		// report. A fence set by the owner's own unconfirmed cleanup is retried by
		// the same owner with the ordinary report, and a cleanup-only report from
		// that owner (its recovery runner, after the in-process retries gave up) also
		// crosses it.
		cleanupOnly := input.Cleanup && !input.Success && !input.Refresh && len(req.Msg.Bundle) == 0
		// A lease from before a server restart carries an older epoch even when its
		// own unconfirmed-cleanup flag is still set: its owner no longer holds a live
		// lease, so it is held to the cleanup-only recovery like any other fence and
		// can neither publish a rotated bundle nor report success.
		staleEpoch := lease.Epoch != s.subscriptionServerEpoch()
		// A replaced Worker instance lost the lease like a restart did: it may only
		// settle the cleanup of its own private home, never report success or
		// publish a rotated bundle, even before the asynchronous fence is recorded.
		recoveryFence := state.RecoveryRequired && (!lease.CleanupUnconfirmed || cleanupOnly || staleEpoch) || replaced
		if state.Generation != input.Generation || lease.Generation != input.Generation || lease.Revision != input.Revision || recoveryFence && (!input.Cleanup || input.Success || input.Refresh || len(req.Msg.Bundle) != 0) {
			s.logger.WarnContext(ctx, "grok_subscription_finish_refused", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "recovery_fence", recoveryFence, "replaced_instance", replaced)
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		if !state.RecoveryRequired && lease.Epoch != s.subscriptionServerEpoch() {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		// A bundle travels only with its refresh confirmation, and that only with a
		// confirmed success and cleanup.
		usable := input.Success && input.Cleanup && !recoveryFence
		if input.Refresh && !usable || !input.Refresh && len(req.Msg.Bundle) != 0 {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		owned = true
		s.logger.DebugContext(ctx, "grok_subscription_finish_validated", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "recovery_fence", recoveryFence, "replaced_instance", replaced, "success", input.Success, "refresh", input.Refresh, "cleanup_confirmed", input.Cleanup)
		credentialUsable := false
		if recoveryFence {
			credentialUsable = s.grokCredentialUsable(ctx, input.Account, input.Generation)
		}
		vault, err := s.secrets()
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		newGeneration := domain.ID(m.RequestId)
		staged := false
		defer func() {
			if staged && returned != nil {
				s.retireUnpublishedGrokGeneration(ctx, vault, input.Account, input.Lease, newGeneration)
			}
		}()
		if usable && input.Refresh {
			bundle, err := subscription.OpenGrokBundle(req.Msg.Bundle, s.Identity.ServerID, input.Account, newGeneration)
			if err != nil {
				s.logger.WarnContext(ctx, "grok_subscription_finish_bundle_rejected", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease)
				return nil, rpc.Error(err, c)
			}
			defer bundle.Clear()
			if commitment := s.grokIdentityCommitment(bundle); state.IdentityCommitment == "" || commitment != state.IdentityCommitment {
				s.logger.WarnContext(ctx, "grok_subscription_identity_changed", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease)
				return nil, rpc.Error(subscriptionDenied(), c)
			}
			oldRaw, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			old, err := subscription.OpenGrokBundle(oldRaw, s.Identity.ServerID, input.Account, state.Generation)
			clear(oldRaw)
			if err == nil {
				err = subscription.GrokRefreshed(old, bundle)
			}
			old.Clear()
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			// Only the exact authenticated Worker lease can stage this bundle.
			staged = true
			if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: newGeneration, Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		var committed domain.ID
		var released bool
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "grok.subscription.finish", input, func(tx *store.Tx) (any, error) {
			released = false
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			r, a, _, err := s.grokExecutionLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, true)
			if err != nil {
				return nil, err
			}
			replacedNow := currentInstance(tx, input.Machine, input.Instance) != nil
			state := a.Subscription
			if state.RecoveryRequired != original.Subscription.RecoveryRequired || state.Generation != input.Generation || state.Lease.Revision != input.Revision || !state.RecoveryRequired && state.Lease.Epoch != s.subscriptionServerEpoch() {
				return nil, subscriptionDenied()
			}
			switch {
			case replacedNow && !recoveryFence:
				// The instance was replaced after the report was validated as the
				// current owner's, whatever it carries: a cleanup-only report that
				// found the lease unfenced is the unverified-exit report and would
				// only fence it. Refuse it whole: the staged generation is retired and
				// the lease is fenced here for the original cleanup-only recovery,
				// which a fully validated report then settles.
				replaced = true
				s.logger.WarnContext(ctx, "grok_subscription_finish_refused", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "recovery_fence", recoveryFence, "replaced_instance", true)
				return nil, subscriptionDenied()
			case !input.Cleanup:
				// Keep the lease and the old generation denied. The original owner
				// reports the private cleanup again.
				state.RecoveryRequired = true
				state.Lease.CleanupUnconfirmed = true
				a.Health = domain.AccountFailed
			case !recoveryFence && !input.Success:
				// The native credential file could not be verified, so the account is
				// fenced; the same Worker reports a cleanup-only recovery next.
				state.RecoveryRequired = true
				a.Health = domain.AccountFailed
			default:
				state.Lease = nil
				state.Members = nil
				state.RecoveryRequired = false
				released = true
				if usable && input.Refresh {
					state.Generation = newGeneration
					// The identity is unchanged: it was verified against the stored one.
				}
				// An accepted logout keeps readiness revoked and starts after this
				// release. A fence crossed by recovery restores readiness only when the
				// stored generation still opens.
				if a.Health != domain.AccountRevoked {
					if recoveryFence && !credentialUsable || a.Connection == nil {
						a.Health = domain.AccountFailed
						if a.Connection == nil {
							a.Health = domain.AccountDisconnected
						}
					} else {
						a.Health = domain.AccountReady
					}
				}
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			committed = state.Generation
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		// The settlement is committed. From here on a failure must not apply the
		// lease-recovery fence: the lease is already released (or fenced by the
		// commit itself), and the account is valid with its published generation.
		owned = false
		s.logger.InfoContext(ctx, "grok_subscription_execution_settled", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "released", released, "success", input.Success, "refreshed", input.Refresh && usable, "cleanup_confirmed", input.Cleanup, "recovery_cleanup", recoveryFence)
		// Only after the commit are the unreferenced generations removed: the
		// superseded one after a refresh publication.
		if input.Cleanup {
			cleanupCtx, cancelCleanup := grokBookkeepingContext(ctx, grokCleanupTimeout)
			err := cleanupSubscriptionReferences(cleanupCtx, vault, input.Account, committed, protectedGenerations(original.Subscription)...)
			cancelCleanup()
			if err != nil {
				// A superseded generation that cannot be removed now stays vault-owned:
				// it is unreferenced, never granted (Take reads only the referenced
				// generation) and removed by the next cleanup. The committed Finish is
				// reported as success so the Worker does not treat a settled lease as
				// uncertain, and no fence is applied to a lease that no longer exists.
				s.logger.WarnContext(ctx, "grok_subscription_cleanup_failed", "phase", grokCleanupPhase, "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
			}
		}
	}
	owned = false
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_subscription_completion_presentation_unavailable", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "replayed", replayed, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "grok_subscription_operation_finished", "phase", grokExecutionPhase, "account_id", input.Account, "lease_id", input.Lease, "cleanup_confirmed", input.Cleanup, "replayed", replayed)
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: rpc.Resource(r), Replayed: result.Replayed}), nil
}
