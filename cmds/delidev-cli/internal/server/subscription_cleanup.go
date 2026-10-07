// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// An initial server login has no independently retained authentication or
// Worker authority. Cleanup must never adopt refresh, logout or restored state.
func failedServerLoginOwner(a domain.Account, operation domain.ID) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionChatGPT || a.Connection != nil || a.Removal != nil || st == nil || st.Generation != "" || st.IdentityCommitment != "" || st.OwnerMachineID != "" || st.Lease != nil || st.Observation != nil || st.ResetCredits != nil {
		return false
	}
	o := st.ServerOperation
	if o == nil || o.ID != operation || o.Action != domain.SubscriptionLogin || o.Generation != "" || o.State == domain.SubscriptionSucceeded || st.Validate(a) != nil {
		return false
	}
	p := st.Pending
	return p == nil && !o.NativeStarted || p != nil && p.ID == o.ID && p.Action == o.Action && p.Actor == o.Actor && p.MachineID == ""
}

func failedServerLoginNeedsCleanup(a domain.Account) bool {
	st := a.Subscription
	if st == nil || st.ServerOperation == nil || !failedServerLoginOwner(a, st.ServerOperation.ID) || st.ServerOperation.CleanupPhase == domain.SubscriptionCredentialCleanupConfirmed {
		return false
	}
	o := st.ServerOperation
	return st.RecoveryRequired || o.State == domain.SubscriptionRecovery || !o.Active() && a.Health != domain.AccountDisconnected
}

func failedServerLoginOutcome(a domain.Account, result domain.SubscriptionLoginState) domain.SubscriptionLoginState {
	if a.Subscription.ServerOperation.State == domain.SubscriptionCanceled || a.Subscription.Pending != nil && a.Subscription.Pending.Canceled {
		return domain.SubscriptionCanceled
	}
	switch result {
	case domain.SubscriptionCanceled, domain.SubscriptionExpired, domain.SubscriptionUnsupported:
		return result
	default:
		return domain.SubscriptionFailed
	}
}

func (s *Service) recoverFailedServerLogin(ctx context.Context, id, operation domain.ID) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	var batchOwned bool
	err = s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		batchOwned, err = tx.FailedSubscriptionCleanupOwns(id, operation)
		return err
	})
	if err != nil || batchOwned {
		return err
	}
	_, err = s.cleanupFailedServerLoginLocked(bounded, id, operation, false, domain.SubscriptionFailed, nil)
	logSubscriptionRuntimeCleanup(s.logger, operation, err)
	return err
}

// Called under accountGate after the original native runner has joined. The
// native checkpoint precedes vault work, so an interrupted cleanup can resume
// without replaying login, callback forwarding or credential publication.
func (s *Service) cleanupFailedServerLoginLocked(ctx context.Context, id, operation domain.ID, nativeConfirmed bool, result domain.SubscriptionLoginState, diagnostic *domain.CodexDiagnostic) (domain.SubscriptionLoginState, error) {
	return s.cleanupFailedServerLoginCheckpointLocked(ctx, id, operation, nativeConfirmed, result, diagnostic, nil, nil)
}

// A batch checkpoint advances its original expected revision in the same
// transaction as account cleanup. Unrelated edits never become delete authority.
func (s *Service) cleanupFailedServerLoginCheckpointLocked(ctx context.Context, id, operation domain.ID, nativeConfirmed bool, result domain.SubscriptionLoginState, diagnostic *domain.CodexDiagnostic, checkpoint func(*store.Tx, store.Record, store.Record) error, credentialAttempt func() error) (domain.SubscriptionLoginState, error) {
	var a domain.Account
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, current, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if !failedServerLoginOwner(current, operation) || !nativeConfirmed && !failedServerLoginNeedsCleanup(current) {
			return subscriptionDenied()
		}
		a = current
		return nil
	})
	if err != nil {
		return "", err
	}
	o := a.Subscription.ServerOperation
	if !nativeConfirmed {
		result = o.State
	}
	result = failedServerLoginOutcome(a, result)
	if o.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed {
		if !nativeConfirmed {
			var err error
			nativeConfirmed, err = reconcileFailedServerLoginRuntime(ctx, s.Store.Root(), *o)
			if err != nil {
				return "", err
			}
		}
		_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.cleanup.native", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
			r, current, err := subscriptionAccount(tx, id, 0)
			if err != nil {
				return nil, err
			}
			if !failedServerLoginOwner(current, operation) {
				return nil, subscriptionDenied()
			}
			st := current.Subscription
			if nativeConfirmed {
				st.ServerOperation.CleanupPhase = domain.SubscriptionNativeCleanupConfirmed
			} else {
				domain.ObserveOwnership(domain.OwnershipCleanup, operation)
			}
			st.ServerOperation.State = result
			if st.ServerOperation.Diagnostic == nil {
				st.ServerOperation.Diagnostic = diagnostic
			}
			st.RecoveryRequired = true
			if st.Pending == nil {
				st.Pending = operationPlaceholder(st.ServerOperation)
			}
			// Preserve cancellation through a restart of the cleanup itself.
			if result == domain.SubscriptionCanceled {
				st.Pending.Canceled = true
			}
			current.Health = domain.AccountFailed
			updated, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", current)
			if err == nil && checkpoint != nil {
				err = checkpoint(tx, r, updated)
			}
			return struct{}{}, err
		})
		if err != nil {
			return "", err
		}
		s.logger.InfoContext(ctx, "server_subscription_cleanup_observed", "operation_id", operation, "phase", domain.SubscriptionNativeCleanupConfirmed, "confirmed", nativeConfirmed)
	}
	if credentialAttempt != nil {
		if err := credentialAttempt(); err != nil {
			return "", err
		}
	}
	vault, err := s.secrets()
	if err != nil {
		return "", err
	}
	if err := cleanupSubscriptionReferences(ctx, vault, id, ""); err != nil {
		return "", err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.cleanup.credentials", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, current, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !failedServerLoginOwner(current, operation) || domain.OwnershipBlocks(domain.OwnershipCleanup, operation, current.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed) {
			return nil, subscriptionDenied()
		}
		st := current.Subscription
		if nativeConfirmed || st.ServerOperation.CleanupPhase == domain.SubscriptionNativeCleanupConfirmed {
			st.ServerOperation.CleanupPhase = domain.SubscriptionCredentialCleanupConfirmed
			st.ServerOperation.NativeStarted = false
		}
		st.ServerOperation.State = failedServerLoginOutcome(current, result)
		st.Pending = nil
		st.RecoveryRequired = st.ServerOperation.NativeStarted
		current.Health = domain.AccountDisconnected
		current.Validation, current.Catalog, current.Quota = nil, nil, nil
		current.ConfirmedExhausted = false
		updated, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", current)
		if err == nil && checkpoint != nil {
			err = checkpoint(tx, r, updated)
		}
		return struct{}{}, err
	})
	if err != nil {
		return "", err
	}
	delete(s.subscriptionProgress, operation)
	s.logger.InfoContext(ctx, "server_subscription_credential_cleanup_finished", "operation_id", operation, "phase", domain.SubscriptionCredentialCleanupConfirmed, "native_confirmed", nativeConfirmed)
	return result, nil
}
