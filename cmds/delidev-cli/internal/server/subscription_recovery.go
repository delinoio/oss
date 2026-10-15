package server

import (
	"bytes"
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

// The ChatGPT (managed Codex) Execute recovery lane.
//
// A fenced Execute lease (RecoveryRequired) is reconciled only by the original
// Worker device through three Worker-only methods:
//
//   - SyncSubscriptionRecoveryCapture stores a protected, non-readable capture of
//     the latest validated authentication of the live lease before the Worker
//     deletes its transient auth.json.
//   - ReadSubscriptionRecoveryCapture returns that capture to the same Worker so
//     it can scan its retained history for the latest tokens.
//   - RecoverSubscription settles the fence under a recovery request identity
//     that is distinct from the original Take and Finish request IDs.
//
// None of them grants readiness, quota access or authentication to a new
// execution, and none rewrites the original Take or Finish receipts.

// recoveryFence selects which side of the recovery fence a lane method accepts.
type recoveryFence int

const (
	// recoveryLive requires the live, unfenced lease of the current instance.
	recoveryLive recoveryFence = iota
	// recoveryFenced requires the recovery fence; the lease's original instance
	// may have been replaced by the current instance of the same original Worker.
	recoveryFenced
	// recoveryAnyFence accepts either state; the instance binding follows the state.
	recoveryAnyFence
)

// subscriptionRecoveryLeaseView resolves and authorizes the exact Execute lease
// of a ChatGPT account for the recovery lane.
func (s *Service) subscriptionRecoveryLeaseView(ctx context.Context, tx *store.Tx, account, lease, machine, instance domain.ID, fence recoveryFence) (store.Record, domain.Account, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Account{}, err
	}
	r, a, err := subscriptionAccount(tx, account, 0)
	if err != nil {
		return r, a, err
	}
	state := a.Subscription
	if state == nil || state.Lease == nil {
		return r, a, subscriptionDenied()
	}
	l := state.Lease
	actor, _ := domain.PrincipalFrom(ctx)
	fenced := state.RecoveryRequired
	if actor.Type != domain.WorkerDevice || actor.DeviceID != l.DeviceID || l.MachineID != machine || l.ID != lease || l.Action != domain.SubscriptionExecute || fence == recoveryLive && fenced || fence == recoveryFenced && !fenced {
		return r, a, subscriptionDenied()
	}
	if !fenced && (l.InstanceID != instance || l.Epoch != s.subscriptionServerEpoch()) {
		return r, a, subscriptionDenied()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return r, a, err
	}
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return r, a, err
	}
	if !slices.Contains(m.WorkerCapabilities, domain.ManagedCodexRecoveryV1) {
		return r, a, domain.Fail(domain.Unsupported, "The Worker has not negotiated ChatGPT execution recovery.", "Update the original Worker; older peers keep the existing recovery fence.")
	}
	return r, a, nil
}

// subscriptionCaptureInput is the receipt digest of a capture request. The
// presenting instance is deliberately excluded: the same durable request is
// repeated unchanged by a replacement instance of the original Worker after a
// lost acknowledgement.
type subscriptionCaptureInput struct {
	Account, Lease, Machine, Generation domain.ID
	Digest                              string
	Revision                            uint64
}

// SyncSubscriptionRecoveryCapture implements the pre-deletion protected capture.
// The original Worker also uses it, once, under the fence to capture a surviving
// transient authentication file of a legacy lease that never synchronized one.
func (s *Service) SyncSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.SyncSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.SyncSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	msg := req.Msg
	defer clear(msg.Bundle)
	m := msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if len(msg.Bundle) == 0 || len(msg.Bundle) > subscription.MaxBundle {
		return nil, rpc.Error(subscription.Invalid(), c)
	}
	_, identity, err := subscription.Parse(msg.Bundle)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
	commitment := s.accountCommitment(s.Identity.ServerID, identityJSON)
	clear(identityJSON)
	input := subscriptionCaptureInput{domain.ID(m.Id), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.GenerationId), s.accountCommitment(domain.ID(m.RequestId), msg.Bundle), m.ExpectedRevision}
	instance := domain.ID(msg.InstanceId)
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "subscription.recovery-capture", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if replayed {
		s.logger.InfoContext(ctx, "subscription_recovery_capture_synced", "account_id", input.Account, "lease_id", input.Lease, "capture_id", m.RequestId, "replayed", true)
		return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: m.RequestId, Replayed: result.Replayed}), nil
	}
	// The capture is usable only as the unchanged or a validated refreshed
	// successor of the settled generation for the same immutable identity.
	var usable bool
	var settled string
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryAnyFence)
		if err != nil {
			return err
		}
		state := a.Subscription
		if state.Lease.Revision != input.Revision || state.Generation != input.Generation || state.Lease.Generation != input.Generation || a.Connection == nil {
			return subscriptionDenied()
		}
		// Under the fence the original Worker may capture a surviving transient
		// file once; a retained capture is never replaced after the lease was lost.
		if state.RecoveryRequired && state.RecoveryCapture != nil {
			return subscriptionDenied()
		}
		settled = state.IdentityCommitment
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if settled != "" && settled == commitment {
		old, getErr := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: input.Generation, Purpose: credentials.AccountLogin})
		if getErr != nil {
			return nil, rpc.Error(getErr, c)
		}
		usable = subscription.Digest(old) == subscription.Digest(msg.Bundle) || subscription.Refreshed(old, msg.Bundle) == nil
		clear(old)
	}
	if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: domain.ID(m.RequestId), Purpose: credentials.AccountLogin}, msg.Bundle); err != nil {
		return nil, rpc.Error(err, c)
	}
	var previous domain.ID
	result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.recovery-capture", input, func(tx *store.Tx) (any, error) {
		r, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryAnyFence)
		if err != nil {
			return nil, err
		}
		state := a.Subscription
		if state.Lease.Revision != input.Revision || state.Generation != input.Generation || state.IdentityCommitment != settled || state.RecoveryRequired && state.RecoveryCapture != nil {
			return nil, subscriptionDenied()
		}
		if state.RecoveryCapture != nil && state.RecoveryCapture.ID != domain.ID(m.RequestId) {
			previous = state.RecoveryCapture.ID
		}
		state.RecoveryCapture = &domain.SubscriptionRecoveryCapture{ID: domain.ID(m.RequestId), LeaseID: input.Lease, Generation: input.Generation, IdentityCommitment: commitment, Usable: usable, SyncedAt: time.Now().UTC()}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if previous != "" {
		// A superseded capture is retired after the new one is committed. A failed
		// deletion is retried by the lease's release, which retires every
		// unprotected reference.
		if deleteErr := vault.Delete(ctx, credentials.Ref{Owner: input.Account, ID: previous, Purpose: credentials.AccountLogin}); deleteErr != nil {
			s.logger.WarnContext(ctx, "subscription_recovery_capture_retire_unconfirmed", "account_id", input.Account, "capture_id", previous, "code", domain.SafeError(deleteErr).Code)
		}
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_synced", "account_id", input.Account, "lease_id", input.Lease, "capture_id", m.RequestId, "usable", usable, "replayed", false)
	return connect.NewResponse(&pb.SyncSubscriptionRecoveryCaptureResponse{CaptureId: m.RequestId, Replayed: result.Replayed}), nil
}

// ReadSubscriptionRecoveryCapture returns the retained capture to the original
// Worker device of a fenced Execute lease. It changes no state.
func (s *Service) ReadSubscriptionRecoveryCapture(ctx context.Context, req *connect.Request[pb.ReadSubscriptionRecoveryCaptureRequest]) (*connect.Response[pb.ReadSubscriptionRecoveryCaptureResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	msg := req.Msg
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	account, lease, machine, instance, capture := domain.ID(msg.AccountId), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.InstanceId), domain.ID(msg.CaptureId)
	if account.Validate() != nil || lease.Validate() != nil || capture.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, account, lease, machine, instance, recoveryFenced)
		if err != nil {
			return err
		}
		held := a.Subscription.RecoveryCapture
		if held == nil || held.ID != capture || held.LeaseID != lease || a.Connection == nil || a.Subscription.Generation != held.Generation {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	bundle, err := vault.Get(ctx, credentials.Ref{Owner: account, ID: capture, Purpose: credentials.AccountLogin})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if _, _, err := subscription.Parse(bundle); err != nil {
		clear(bundle)
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "subscription_recovery_capture_read", "account_id", account, "lease_id", lease, "capture_id", capture)
	return connect.NewResponse(&pb.ReadSubscriptionRecoveryCaptureResponse{Bundle: bundle}), nil
}

type subscriptionRecoverOutcome string

const (
	recoverRestore subscriptionRecoverOutcome = "restore"
	recoverRetire  subscriptionRecoverOutcome = "retire"
	recoverBlocked subscriptionRecoverOutcome = "blocked"
)

// subscriptionRecoverInput is the receipt digest of a recovery request. The
// presenting instance is deliberately excluded (see subscriptionCaptureInput), so
// the durable recovery request replays across Worker restarts and replacement
// instances of the same original Worker.
type subscriptionRecoverInput struct {
	Account, Lease, Machine, Generation, Capture domain.ID
	Revision                                     uint64
	Outcome                                      pb.SubscriptionRecoveryOutcome
	Reason                                       pb.SubscriptionRecoveryBlockedReason
}

func subscriptionRecoveryReason(value pb.SubscriptionRecoveryBlockedReason) domain.SubscriptionRecoveryReason {
	switch value {
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CREDENTIAL_PROOF_UNAVAILABLE:
		return domain.RecoveryCredentialProofUnavailable
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_CLEANUP_UNCONFIRMED:
		return domain.RecoveryCleanupUnconfirmed
	case pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_OWNERSHIP_UNPROVEN:
		return domain.RecoveryOwnershipUnproven
	default:
		return ""
	}
}

// RecoverSubscription settles a fenced ChatGPT Execute lease from independently
// verified original-Worker evidence. The recovery request ID is durable and
// distinct from the original Take and Finish IDs, which stay untouched.
func (s *Service) RecoverSubscription(ctx context.Context, req *connect.Request[pb.RecoverSubscriptionRequest]) (response *connect.Response[pb.RecoverSubscriptionResponse], returned error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	msg := req.Msg
	m := msg.Mutation
	if err := validateAccountMutation(m); err != nil {
		return nil, rpc.Error(err, c)
	}
	if err := workerActor(ctx, msg.MachineId, msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	input := subscriptionRecoverInput{domain.ID(m.Id), domain.ID(msg.LeaseId), domain.ID(msg.MachineId), domain.ID(msg.GenerationId), domain.ID(msg.CaptureId), m.ExpectedRevision, msg.Outcome, msg.BlockedReason}
	instance := domain.ID(msg.InstanceId)
	switch msg.Outcome {
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_SETTLED:
		if input.Capture.Validate() != nil || msg.BlockedReason != pb.SubscriptionRecoveryBlockedReason_SUBSCRIPTION_RECOVERY_BLOCKED_REASON_UNSPECIFIED {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
	case pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED:
		if subscriptionRecoveryReason(msg.BlockedReason) == "" {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
	default:
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	if input.Lease.Validate() != nil || input.Generation.Validate() != nil {
		return nil, rpc.Error(subscriptionDenied(), c)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, replayed, err := s.Store.Replay(ctx, domain.ID(m.RequestId), "subscription.recover", input)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	vault, err := s.secrets()
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if !replayed {
		var original domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			var err error
			_, original, err = s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryFenced)
			if err == nil && (original.Subscription.Lease.Revision != input.Revision || original.Subscription.Generation != input.Generation || original.Subscription.Lease.Generation != input.Generation) {
				err = subscriptionDenied()
			}
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		state := original.Subscription
		if msg.Outcome == pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED {
			reason := subscriptionRecoveryReason(msg.BlockedReason)
			result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.recover", input, func(tx *store.Tx) (any, error) {
				r, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryFenced)
				if err != nil {
					return nil, err
				}
				if a.Subscription.Lease.Revision != input.Revision || a.Subscription.Generation != input.Generation {
					return nil, subscriptionDenied()
				}
				if a.Subscription.RecoveryReason != reason {
					a.Subscription.RecoveryReason = reason
					if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
						return nil, err
					}
				}
				return accountReceipt{ID: r.ID}, nil
			})
			if err != nil {
				return nil, rpc.Error(err, c)
			}
			s.logger.WarnContext(ctx, "subscription_recovery_blocked", "account_id", input.Account, "lease_id", input.Lease, "recovery_id", m.RequestId, "reason", reason)
			return s.recoveryResponse(ctx, input.Account, result.Replayed, c)
		}
		// Settlement needs the exact retained capture and an unrevoked connection;
		// restored or foreign evidence never reaches the vault.
		if state.RecoveryCapture == nil || state.RecoveryCapture.ID != input.Capture || state.RecoveryCapture.LeaseID != input.Lease || state.RecoveryCapture.Generation != input.Generation || original.Connection == nil || state.IdentityCommitment == "" {
			return nil, rpc.Error(subscriptionDenied(), c)
		}
		// A committed retirement checkpoint means the retire decision was already
		// made and the protected references may be partly or fully deleted, so
		// the decision is never re-derived from the (possibly missing) capture.
		outcome := recoverRetire
		if !state.RecoveryRetiring {
			var err error
			outcome, err = s.recoveryDecision(ctx, vault, input, state)
			if err != nil {
				return nil, rpc.Error(err, c)
			}
		}
		if outcome == recoverRetire {
			if held := independentCredentialGenerations(state); len(held) != 0 {
				// Independent credential users keep their captured references until
				// their own cleanup settles; the owner is not retired under them.
				s.logger.WarnContext(ctx, "subscription_recovery_waiting_for_credential_users", "account_id", input.Account, "lease_id", input.Lease)
				return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "Independent credential users of this account have not settled.", "Wait for the original quota or credit readers to finish; the recovery fence stays."), c)
			}
			if !state.RecoveryRetiring {
				// Persist the retirement before any evidence is deleted, so a crash
				// or store failure between the deletions and the settlement leaves
				// a resumable state instead of a fence that references deleted
				// evidence.
				if err := s.checkpointRecoveryRetirement(ctx, input, instance); err != nil {
					return nil, rpc.Error(err, c)
				}
				s.logger.InfoContext(ctx, "subscription_recovery_retirement_checkpointed", "account_id", input.Account, "lease_id", input.Lease, "recovery_id", m.RequestId)
			}
			// Every protected reference is denied and removed before the account
			// may be disconnected, so no login or execution is admitted while an
			// original reference could still be redistributed.
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, ""); err != nil {
				return nil, rpc.Error(err, c)
			}
			if refs, err := vault.UnremovedReferences(ctx, input.Account); err != nil || len(refs) != 0 {
				if err == nil {
					err = subscriptionDenied()
				}
				return nil, rpc.Error(err, c)
			}
		} else if _, err := s.stageRecoveredGeneration(ctx, vault, input, domain.ID(m.RequestId)); err != nil {
			return nil, rpc.Error(err, c)
		}
		result, err = s.Store.Mutate(ctx, domain.ID(m.RequestId), "subscription.recover", input, func(tx *store.Tx) (any, error) {
			r, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryFenced)
			if err != nil {
				return nil, err
			}
			st := a.Subscription
			if st.Lease.Revision != input.Revision || st.Generation != input.Generation || st.RecoveryCapture == nil || st.RecoveryCapture.ID != input.Capture || a.Connection == nil {
				return nil, subscriptionDenied()
			}
			applyRecoverySettlement(&a, outcome, domain.ID(m.RequestId))
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
			return accountReceipt{ID: r.ID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
	}
	if msg.Outcome == pb.SubscriptionRecoveryOutcome_SUBSCRIPTION_RECOVERY_OUTCOME_BLOCKED {
		return s.recoveryResponse(ctx, input.Account, true, c)
	}
	// Both fresh and replayed settlements retire the references the committed
	// state no longer owns, so a crash between commit and deletion is repaired by
	// the Worker's retry of the same recovery request.
	{
		var current domain.Account
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			var err error
			_, current, err = subscriptionAccount(tx, input.Account, 0)
			return err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		if current.Subscription != nil && current.Subscription.Lease == nil {
			keep := current.Subscription.Generation
			if err := cleanupSubscriptionReferences(ctx, vault, input.Account, keep, protectedGenerations(current.Subscription)...); err != nil {
				s.logger.WarnContext(ctx, "subscription_recovery_reference_retirement_unconfirmed", "account_id", input.Account, "recovery_id", m.RequestId, "code", domain.SafeError(err).Code)
				return nil, rpc.Error(err, c)
			}
		}
	}
	s.logger.InfoContext(ctx, "subscription_recovery_settled", "account_id", input.Account, "lease_id", input.Lease, "recovery_id", m.RequestId, "replayed", replayed)
	return s.recoveryResponse(ctx, input.Account, replayed || result.Replayed, c)
}

// checkpointRecoveryRetirement commits the decision to retire the fenced
// account. It is idempotent: an already recorded checkpoint is left unchanged.
func (s *Service) checkpointRecoveryRetirement(ctx context.Context, input subscriptionRecoverInput, instance domain.ID) error {
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.recover-retirement", struct{ Account, Lease domain.ID }{input.Account, input.Lease}, func(tx *store.Tx) (any, error) {
		r, a, err := s.subscriptionRecoveryLeaseView(ctx, tx, input.Account, input.Lease, input.Machine, instance, recoveryFenced)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st.Lease.Revision != input.Revision || st.Generation != input.Generation || st.RecoveryCapture == nil || st.RecoveryCapture.ID != input.Capture || a.Connection == nil {
			return nil, subscriptionDenied()
		}
		if !st.RecoveryRetiring {
			st.RecoveryRetiring = true
			if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
				return nil, err
			}
		}
		return accountReceipt{ID: r.ID}, nil
	})
	return err
}

func (s *Service) recoveryResponse(ctx context.Context, account domain.ID, replayed bool, correlation string) (*connect.Response[pb.RecoverSubscriptionResponse], error) {
	r, err := s.accountRecord(ctx, account)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	return connect.NewResponse(&pb.RecoverSubscriptionResponse{Account: rpc.Resource(r), Replayed: replayed}), nil
}

// recoveryDecision chooses between restoring the account on a positively
// validated latest credential and retiring it as disconnected. A transient vault
// failure is returned as an error and never degrades to retirement.
func (s *Service) recoveryDecision(ctx context.Context, vault accountSecrets, input subscriptionRecoverInput, state *domain.SubscriptionState) (subscriptionRecoverOutcome, error) {
	capture := state.RecoveryCapture
	bundle, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: capture.ID, Purpose: credentials.AccountLogin})
	if err != nil {
		return "", err
	}
	defer clear(bundle)
	if !capture.Usable {
		return recoverRetire, nil
	}
	_, identity, err := subscription.Parse(bundle)
	if err != nil {
		return recoverRetire, nil
	}
	identityJSON, _ := json.Marshal(struct{ Account, User string }{identity.Account, identity.User})
	commitment := s.accountCommitment(s.Identity.ServerID, identityJSON)
	clear(identityJSON)
	if commitment != state.IdentityCommitment || commitment != capture.IdentityCommitment {
		return recoverRetire, nil
	}
	old, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: state.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		return "", err
	}
	defer clear(old)
	if bytes.Equal(old, bundle) || subscription.Refreshed(old, bundle) == nil {
		return recoverRestore, nil
	}
	return recoverRetire, nil
}

// stageRecoveredGeneration publishes the captured bundle as the one immutable
// generation named by the recovery request. Put is idempotent for identical
// content, so a retried recovery reuses the same generation.
func (s *Service) stageRecoveredGeneration(ctx context.Context, vault accountSecrets, input subscriptionRecoverInput, generation domain.ID) (domain.ID, error) {
	bundle, err := vault.Get(ctx, credentials.Ref{Owner: input.Account, ID: input.Capture, Purpose: credentials.AccountLogin})
	if err != nil {
		return "", err
	}
	defer clear(bundle)
	if _, err := vault.Put(ctx, credentials.Ref{Owner: input.Account, ID: generation, Purpose: credentials.AccountLogin}, bundle); err != nil {
		return "", err
	}
	return generation, nil
}

// applyRecoverySettlement releases the fenced lease. Restore keeps the
// connection and publishes the recovery request as the new generation; retire
// settles the account disconnected so only the existing login flow applies.
func applyRecoverySettlement(a *domain.Account, outcome subscriptionRecoverOutcome, generation domain.ID) {
	state := a.Subscription
	lease := state.Lease
	state.Lease = nil
	state.Members = nil
	state.RecoveryRequired = false
	state.RecoveryCapture = nil
	state.RecoveryRetiring = false
	state.RecoveryReason = ""
	if state.Pending != nil && state.Pending.ID == lease.OperationID {
		state.Pending = nil
	}
	observation := state.Observation
	if outcome == recoverRestore {
		state.AutomaticCreditConsent = nil
		state.Generation = generation
		state.PaidCredits = nil
		state.OwnerMachineID = lease.MachineID
		if observation != nil && observation.Generation == lease.Generation {
			if observation.Phase == domain.SubscriptionObservationQueued {
				observation.Phase = domain.SubscriptionObservationFailed
				observation.ErrorCode = domain.Conflict
			} else if observation.Phase == domain.SubscriptionObservationSending {
				if observation.Action == domain.SubscriptionQuota {
					observation.Phase = domain.SubscriptionObservationFailed
					observation.ErrorCode = domain.Unavailable
					state.QuotaState = domain.ObservationFailed
				} else {
					observation.Phase = domain.SubscriptionObservationUncertain
				}
			}
		}
		if state.Pending == nil && a.Health != domain.AccountRevoked {
			a.Health = domain.AccountReady
		}
		return
	}
	state.AutomaticCreditConsent = nil
	a.Connection = nil
	a.Health = domain.AccountDisconnected
	a.Validation = nil
	a.Catalog = nil
	a.Quota = nil
	a.ConfirmedExhausted = false
	state.Generation = ""
	state.IdentityCommitment = ""
	state.PaidCredits = nil
	state.ResetCredits = nil
	state.QuotaObservedAt = nil
	state.QuotaState = domain.ObservationUnknown
	state.SpendControlReached = nil
	state.SpendControlObservedAt = nil
	if observation != nil && (observation.Phase == domain.SubscriptionObservationSending || observation.Phase == domain.SubscriptionObservationUncertain) {
		observation.Phase = domain.SubscriptionObservationRetiredUncertain
	}
}
