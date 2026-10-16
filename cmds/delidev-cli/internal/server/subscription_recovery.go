package server

import (
	"context"
	"encoding/json"
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

// Automatic recovery of a retained managed ChatGPT Execute lease.
//
// The original Worker synchronizes a protected capture of the latest validated
// bundle before it deletes transient authentication. When authentication
// cleanup cannot be confirmed, the account is fenced with its lease. The same
// Worker device then proves independent cleanup and settles one durable result
// through RecoverSubscription. The recovery lane never grants a credential to a
// new Take, reuses the failed Finish request or readmits an old generation.

type subscriptionCaptureInput struct {
	Account, Lease, Machine, Instance, Generation domain.ID
	Revision                                      uint64
	Digest                                        string
}

type subscriptionRecoverInput struct {
	Account, Lease, Machine, Instance, Generation, Capture domain.ID
	Revision                                               uint64
	Outcome                                                domain.SubscriptionRecoveryOutcome
	Reason                                                 domain.SubscriptionRecoveryReason
}

// recoveryCapabilities reports whether the machine negotiated the Worker-only
// recovery lane together with managed ChatGPT subscriptions.
func recoveryCapabilities(machine domain.Machine) bool {
	return slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexRecoveryV1) && slices.Contains(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1)
}

// recoveryEligible reports whether a fenced account is a managed ChatGPT
// Execute owner lease of this machine that the recovery lane may reconcile.
func recoveryEligible(a domain.Account, machine domain.ID) bool {
	st := a.Subscription
	return a.SubscriptionService == domain.SubscriptionChatGPT && st != nil && st.RecoveryRequired && st.Lease != nil && st.Lease.Action == domain.SubscriptionExecute && st.Lease.MachineID == machine
}

// recoveryLease resolves the exact original owner lease for a recovery-lane
// call. Ordinary capture synchronization during a live execution uses the strict
// owner view. A fenced lease is reconciled by the same Worker device on its
// original machine, from the original or a current replacement instance, and
// across a server restart.
func (s *Service) recoveryLease(ctx context.Context, tx *store.Tx, account, lease, machine, instance, generation domain.ID) (store.Record, domain.Account, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Account{}, err
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return store.Record{}, domain.Account{}, err
	}
	_, mach, err := activeMachine(tx, machine)
	if err != nil {
		return store.Record{}, domain.Account{}, err
	}
	if !recoveryCapabilities(mach) {
		return store.Record{}, domain.Account{}, subscriptionDenied()
	}
	r, a, err := subscriptionAccount(tx, account, 0)
	if err != nil {
		return r, a, err
	}
	st := a.Subscription
	if st == nil || st.Lease == nil || st.Lease.ID != lease || st.Lease.Action != domain.SubscriptionExecute || st.Generation != generation || st.Lease.Generation != generation {
		return r, a, subscriptionDenied()
	}
	l := st.Lease
	actor, _ := domain.PrincipalFrom(ctx)
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || actor.MachineID != l.MachineID || l.MachineID != machine {
		return r, a, subscriptionDenied()
	}
	if !st.RecoveryRequired && (l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch()) {
		return r, a, subscriptionDenied()
	}
	return r, a, nil
}

// SyncSubscriptionRecoveryCapture stores the latest validated bundle of the
// original Execute lease as a protected vault reference bound to the lease and
// the immutable identity commitment.
func (s *Service) SyncSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	defer clear(req.Msg.Bundle)
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(req.Msg.Bundle) == 0 || len(req.Msg.Bundle) > subscription.MaxBundle {
		return nil, rpc.Error(subscription.Invalid(), c)
	}
	input := subscriptionCaptureInput{domain.ID(m.Id), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId), m.ExpectedRevision, s.accountCommitment(domain.ID(m.RequestId), req.Msg.Bundle)}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	if _, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "subscription.recovery-capture", input); err != nil {
		return nil, rpc.Error(err, c)
	} else if replayed {
		s.logger.InfoContext(ctx, "subscription_recovery_capture_synced", "account_id", input.Account, "lease_id", input.Lease, "capture_id", m.RequestId, "replayed", true)
		return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: m.RequestId, Replayed: true}), nil
	}
	var state *domain.SubscriptionState
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
		if err != nil {
			return err
		}
		if a.Subscription.Lease.Revision != input.Revision {
			return subscriptionDenied()
		}
		state = a.Subscription
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	_, identity, err := subscription.Parse(req.Msg.Bundle)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
	commitment := s.accountCommitment(s.Identity.ServerID, identityJSON)
	clear(identityJSON)
	if state.IdentityCommitment != "" && commitment != state.IdentityCommitment {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	// The capture must be the original generation or one of its refreshes.
	old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if subscription.Digest(old) != subscription.Digest(req.Msg.Bundle) {
		err = subscription.Refreshed(old, req.Msg.Bundle)
	}
	clear(old)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: domain.ID(m.RequestId), Purpose: credentials.AccountLogin}, req.Msg.Bundle); err != nil {
		return nil, rpc.Error(err, c)
	}
	var superseded domain.ID
	_, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.recovery-capture", input, func(tx *store.Tx) (any, error) {
		r, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
		if err != nil {
			return nil, err
		}
		l := a.Subscription.Lease
		if l.Revision != input.Revision {
			return nil, subscriptionDenied()
		}
		if l.RecoveryCapture != nil {
			superseded = l.RecoveryCapture.ID
		}
		l.RecoveryCapture = &domain.SubscriptionRecoveryCapture{ID: domain.ID(m.RequestId), IdentityCommitment: commitment, CapturedAt: time.Now().UTC()}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if superseded != "" && superseded != domain.ID(m.RequestId) {
		if err := vault.Delete(ctx, credentials.Ref{Owner: input.Account, ID: superseded, Purpose: credentials.AccountLogin}); err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_retire_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
		}
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_synced", "account_id", input.Account, "lease_id", input.Lease, "capture_id", m.RequestId, "replayed", false)
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: m.RequestId}), nil
}

// ReadSubscriptionRecoveryCapture returns the protected material of a fenced
// original Execute lease to the same Worker device that was granted it. The
// original bundle was already delivered to that device; the latest capture is
// the only newer generation. Neither grants readiness or a new lease.
func (s *Service) ReadSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	account, lease, machine, instance, generation := domain.ID(req.Msg.AccountId), domain.ID(req.Msg.LeaseId), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.GenerationId)
	for _, id := range []domain.ID{account, lease, machine, instance, generation} {
		if err := id.Validate(); err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var state *domain.SubscriptionState
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.recoveryLease(ctx, tx, account, lease, machine, instance, generation)
		if err != nil {
			return err
		}
		if !a.Subscription.RecoveryRequired {
			return subscriptionDenied()
		}
		state = a.Subscription
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	response := &pb.ReadSubscriptionRecoveryCaptureResponse{}
	original, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: state.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	response.OriginalBundle = original
	if capture := state.Lease.RecoveryCapture; capture != nil {
		latest, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: capture.ID, Purpose: credentials.AccountLogin})
		if err != nil {
			// A missing capture reads as no proof; it never fabricates one.
			s.logger.WarnContext(ctx, "subscription_recovery_capture_unreadable", "account_id", account, "lease_id", lease, "capture_id", capture.ID, "code", domain.SafeError(err).Code)
		} else {
			response.CaptureId, response.LatestBundle = string(capture.ID), latest
		}
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_read", "account_id", account, "lease_id", lease, "capture_present", response.CaptureId != "")
	return connect.NewResponse(response), nil
}

func recoveryOutcome(value pb.SubscriptionRecoveryOutcome) domain.SubscriptionRecoveryOutcome {
	switch value {
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_CREDENTIALS_VERIFIED:
		return domain.RecoveryCredentialsVerified
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_NO_USABLE_CREDENTIALS:
		return domain.RecoveryNoUsableCredentials
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED:
		return domain.RecoveryBlocked
	}
	return ""
}

func recoveryReason(value pb.SubscriptionRecoveryReason) domain.SubscriptionRecoveryReason {
	switch value {
	case pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_PROOF_UNAVAILABLE:
		return domain.RecoveryCredentialProofUnavailable
	case pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_OWNERSHIP_EVIDENCE_UNAVAILABLE:
		return domain.RecoveryOwnershipEvidenceUnavailable
	case pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_NATIVE_PROCESS_UNCONFIRMED:
		return domain.RecoveryNativeProcessUnconfirmed
	case pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_AUTHENTICATION_CLEANUP_UNCONFIRMED:
		return domain.RecoveryAuthenticationCleanupUnconfirmed
	case pb.SubscriptionRecoveryReason_SUBSCRIPTION_RECOVERY_REASON_CREDENTIAL_VALIDATION_FAILED:
		return domain.RecoveryCredentialValidationFailed
	}
	return ""
}

// recoveryDependentsActive reports server-owned credential users that depend on
// the retained generation. They keep the original protection until they settle.
func recoveryDependentsActive(st *domain.SubscriptionState) bool {
	return st.ServerOperation != nil && st.ServerOperation.NativeStarted || st.ServerQuotaActive() || st.ServerCreditActive()
}

// RecoverSubscription settles one durable, idempotent recovery result for a
// fenced original Execute lease. CREDENTIALS_VERIFIED publishes the protected
// capture as one new immutable generation. NO_USABLE_CREDENTIALS retires every
// protected reference and settles the account disconnected, so the existing
// explicit login applies. BLOCKED records only a closed diagnostic.
func (s *Service) RecoverSubscription(ctx context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	outcome, reason := recoveryOutcome(req.Msg.Outcome), recoveryReason(req.Msg.Reason)
	if outcome == "" || outcome == domain.RecoveryBlocked != (reason != "") || outcome == domain.RecoveryCredentialsVerified != (req.Msg.CaptureId != "") {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The recovery outcome is invalid.", "Report one closed outcome with its matching evidence."), c)
	}
	input := subscriptionRecoverInput{Account: domain.ID(m.Id), Lease: domain.ID(req.Msg.LeaseId), Machine: domain.ID(req.Msg.MachineId), Instance: domain.ID(req.Msg.InstanceId), Generation: domain.ID(req.Msg.GenerationId), Capture: domain.ID(req.Msg.CaptureId), Revision: m.ExpectedRevision, Outcome: outcome, Reason: reason}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	requestID := domain.ID(m.RequestId)
	result, replayed, err := s.Store.Replay(ctx, requestID, "subscription.recover", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		result, err = s.recoverSubscription(ctx, requestID, input)
		if err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_refused", "account_id", input.Account, "lease_id", input.Lease, "outcome", outcome, "code", domain.SafeError(err).Code)
			return nil, rpc.Error(err, c)
		}
	}
	r, err := s.accountRecord(ctx, input.Account)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_recovery_settled", "account_id", input.Account, "lease_id", input.Lease, "request_id", requestID, "outcome", outcome, "reason", reason, "replayed", replayed || result.Replayed)
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{Account: rpc.Resource(r), Replayed: replayed || result.Replayed}), nil
}

func (s *Service) recoverSubscription(ctx context.Context, requestID domain.ID, input subscriptionRecoverInput) (store.Result, error) {
	var state *domain.SubscriptionState
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		r, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
		if err != nil {
			return err
		}
		_ = r
		if !a.Subscription.RecoveryRequired || a.Subscription.Lease.Revision != input.Revision {
			return subscriptionDenied()
		}
		state = a.Subscription
		return nil
	})
	if err != nil {
		return store.Result{}, err
	}
	if input.Outcome == domain.RecoveryBlocked {
		return s.Store.Mutate(ctx, requestID, "subscription.recover", input, func(tx *store.Tx) (any, error) {
			r, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
			if err != nil {
				return nil, err
			}
			a.Subscription.RecoveryReason = input.Reason
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return accountReceipt{ID: r.ID}, nil
		})
	}
	if recoveryDependentsActive(state) {
		return store.Result{}, domain.Fail(domain.Conflict, "A dependent credential user still holds this account.", "Wait for the dependent operation to settle before recovery retires the original owner.")
	}
	vault, err := s.secrets()
	if err != nil {
		return store.Result{}, err
	}
	switch input.Outcome {
	case domain.RecoveryCredentialsVerified:
		capture := state.Lease.RecoveryCapture
		if capture == nil || capture.ID != input.Capture {
			return store.Result{}, subscriptionDenied()
		}
		latest, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: capture.ID, Purpose: credentials.AccountLogin})
		if err != nil {
			return store.Result{}, err
		}
		defer clear(latest)
		_, identity, err := subscription.Parse(latest)
		if err != nil {
			return store.Result{}, err
		}
		identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
		commitment := s.accountCommitment(s.Identity.ServerID, identityJSON)
		clear(identityJSON)
		if commitment != capture.IdentityCommitment || state.IdentityCommitment != "" && commitment != state.IdentityCommitment {
			return store.Result{}, subscriptionDenied()
		}
		// Publish the capture as one new immutable generation under the recovery
		// request identity. A replay writes the same bytes to the same reference.
		if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: requestID, Purpose: credentials.AccountLogin}, latest); err != nil {
			return store.Result{}, err
		}
		result, err := s.Store.Mutate(ctx, requestID, "subscription.recover", input, func(tx *store.Tx) (any, error) {
			r, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
			if err != nil {
				return nil, err
			}
			st := a.Subscription
			if !st.RecoveryRequired || st.Lease.RecoveryCapture == nil || st.Lease.RecoveryCapture.ID != input.Capture || recoveryDependentsActive(st) {
				return nil, subscriptionDenied()
			}
			if st.Pending != nil && st.Pending.ID == st.Lease.OperationID {
				st.Pending = nil
			}
			st.Lease, st.Members, st.RecoveryRequired, st.RecoveryReason = nil, nil, false, ""
			st.AutomaticCreditConsent = nil
			st.Generation, st.PaidCredits, st.IdentityCommitment = requestID, nil, commitment
			st.OwnerMachineID = input.Machine
			if st.Observation != nil && st.Observation.Generation == input.Generation {
				if st.Observation.Phase == domain.SubscriptionObservationQueued {
					st.Observation.Phase = domain.SubscriptionObservationFailed
					st.Observation.ErrorCode = domain.Conflict
				} else if st.Observation.Phase == domain.SubscriptionObservationSending {
					if st.Observation.Action == domain.SubscriptionQuota {
						st.Observation.Phase = domain.SubscriptionObservationFailed
						st.Observation.ErrorCode = domain.Unavailable
						st.QuotaState = domain.ObservationFailed
					} else {
						st.Observation.Phase = domain.SubscriptionObservationUncertain
					}
				}
			}
			if a.Connection == nil {
				a.Connection = &domain.AccountConnection{ID: requestID, Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()}
			}
			if st.Pending == nil && a.Health != domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			return result, err
		}
		// Unreferenced original and capture references stay vault-owned and are
		// never granted. The next Finish retires them with its ordinary cleanup.
		if err := cleanupSubscriptionReferences(ctx, vault, input.Account, requestID, protectedGenerations(state)...); err != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_reference_retirement_unconfirmed", "account_id", input.Account, "lease_id", input.Lease, "code", domain.SafeError(err).Code)
		}
		return result, nil
	case domain.RecoveryNoUsableCredentials:
		// Retire every protected reference first. An incomplete deletion keeps the
		// fence and the same request is retried; nothing is admitted meanwhile.
		if err := cleanupSubscriptionReferences(ctx, vault, input.Account, "", protectedGenerations(state)...); err != nil {
			return store.Result{}, err
		}
		return s.Store.Mutate(ctx, requestID, "subscription.recover", input, func(tx *store.Tx) (any, error) {
			r, a, err := s.recoveryLease(ctx, tx, input.Account, input.Lease, input.Machine, input.Instance, input.Generation)
			if err != nil {
				return nil, err
			}
			st := a.Subscription
			if !st.RecoveryRequired || recoveryDependentsActive(st) {
				return nil, subscriptionDenied()
			}
			if st.Pending != nil && st.Pending.ID == st.Lease.OperationID {
				st.Pending = nil
			}
			st.Lease, st.Members, st.RecoveryRequired, st.RecoveryReason = nil, nil, false, ""
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Validation, a.Catalog, a.Quota = nil, nil, nil
			a.ConfirmedExhausted = false
			st.Generation, st.IdentityCommitment, st.PaidCredits, st.ResetCredits = "", "", nil, nil
			st.AutomaticCreditConsent = nil
			st.QuotaObservedAt, st.QuotaState = nil, domain.ObservationUnknown
			st.SpendControlReached, st.SpendControlObservedAt = nil, nil
			if st.Observation != nil && (st.Observation.Phase == domain.SubscriptionObservationSending || st.Observation.Phase == domain.SubscriptionObservationUncertain) {
				st.Observation.Phase = domain.SubscriptionObservationRetiredUncertain
			}
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return accountReceipt{ID: r.ID}, nil
		})
	}
	return store.Result{}, subscriptionDenied()
}
