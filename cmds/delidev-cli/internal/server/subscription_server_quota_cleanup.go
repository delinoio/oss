// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Startup may reconcile original cleanup, but never repeats external auth or
// quota. No account lock or transaction spans native ownership reconciliation.
func (s *Service) reconcileRetainedServerQuotas(ctx context.Context) error {
	type retained struct {
		Account   domain.ID
		Operation domain.ServerQuotaOperation
	}
	var owners []retained
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		rows, err := all(tx, domain.AccountKind)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return err
			}
			if a.Subscription == nil || a.Subscription.ServerQuota == nil {
				continue
			}
			o := a.Subscription.ServerQuota
			if o.AccessOnly && o.Epoch != s.subscriptionServerEpoch() && o.Phase == domain.SubscriptionObservationUncertain {
				owners = append(owners, retained{r.ID, *o})
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, owner := range owners {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		if !owner.Operation.CleanupConfirmed {
			err = s.reconcileServerQuotaRuntime(bounded, owner.Account, owner.Operation)
		}
		if err != nil {
			cancel()
			s.logger.WarnContext(ctx, "server_quota_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code)
			continue
		}
		err = func() error {
			unlock, err := s.lockAccounts(bounded)
			if err != nil {
				return err
			}
			defer unlock()
			// Checkpoint original native cleanup independently of vault cleanup.
			_, err = s.Store.Mutate(bounded, domain.NewID(), "subscription.server.quota.reconcile.cleanup", owner, func(tx *store.Tx) (any, error) {
				r, a, err := subscriptionAccount(tx, owner.Account, 0)
				if err != nil {
					return nil, err
				}
				if a.Subscription.ServerQuota == nil || *a.Subscription.ServerQuota != owner.Operation {
					return nil, subscriptionDenied()
				}
				a.Subscription.ServerQuota.CleanupConfirmed = true
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return struct{}{}, err
			})
			if err != nil {
				return err
			}
			owner.Operation.CleanupConfirmed = true
			if err = s.retireServerQuotaReference(bounded, owner.Account, owner.Operation); err != nil {
				return err
			}
			_, err = s.Store.Mutate(bounded, domain.NewID(), "subscription.server.quota.reconciled", owner, func(tx *store.Tx) (any, error) {
				r, a, err := subscriptionAccount(tx, owner.Account, 0)
				if err != nil {
					return nil, err
				}
				o := a.Subscription.ServerQuota
				if o == nil || *o != owner.Operation {
					return nil, subscriptionDenied()
				}
				o.Phase = domain.SubscriptionObservationFailed
				o.ErrorCode = domain.Canceled
				a.Subscription.QuotaState = domain.ObservationFailed
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return struct{}{}, err
			})
			if err != nil {
				return err
			}
			// The terminal receipt step is recorded only after the single settlement.
			return markLegacyAccessOnlySettled(bounded, s.scopeFor(owner.Account), legacyQuota(owner.Account, owner.Operation))
		}()
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "server_quota_reference_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code)
			continue
		}
		s.logger.InfoContext(ctx, "server_quota_cleanup_reconciled", "operation_id", owner.Operation.ID)
	}
	return s.reconcilePendingServerQuotaRetirements(ctx)
}
func (s *Service) reconcileServerQuotaRuntime(ctx context.Context, account domain.ID, o domain.ServerQuotaOperation) error {
	if o.Validate() != nil || !o.AccessOnly || o.Phase != domain.SubscriptionObservationUncertain {
		return subscriptionDenied()
	}
	return reconcileAccessOnlyRuntime(ctx, s.scopeFor(account), legacyQuota(account, o))
}

func legacyQuota(account domain.ID, o domain.ServerQuotaOperation) legacyAccessOnly {
	return legacyAccessOnly{Kind: nativeprofile.ReceiptLegacyQuota, Directory: "quota", Purpose: nativeprofile.PurposeQuota, Account: account, Connection: o.ConnectionID, Generation: o.Generation, Operation: o.ID}
}

// generationHeld reports whether another live owner still depends on the
// credential generation. ignoreQuota and ignoreCredit exclude the access-only
// owner whose obsolete captured reference is being retired.
func generationHeld(st *domain.SubscriptionState, generation domain.ID, ignoreQuota, ignoreCredit bool) bool {
	return st.Lease != nil && st.Lease.Generation == generation ||
		!ignoreCredit && st.ServerCredit != nil && st.ServerCredit.Active() && st.ServerCredit.Generation == generation ||
		!ignoreQuota && st.ServerQuota != nil && st.ServerQuota.Active() && st.ServerQuota.Generation == generation ||
		st.Observation != nil && st.Observation.Active() && st.Observation.Generation == generation ||
		st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) && st.ServerOperation.Generation == generation
}

// Caller holds account serialization. Native cleanup and the exact retained
// operation authorize only retirement of its obsolete captured reference.
func (s *Service) retireServerQuotaReference(ctx context.Context, id domain.ID, original domain.ServerQuotaOperation) error {
	var current domain.ID
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		o := st.ServerQuota
		if original.Validate() != nil || o == nil || o.Validate() != nil || o.ID != original.ID || o.Epoch != original.Epoch || o.Actor != original.Actor || o.Generation != original.Generation || o.ConnectionID != original.ConnectionID || o.FinishID != original.FinishID || !o.RequestedAt.Equal(original.RequestedAt) || o.AccessOnly != original.AccessOnly || o.AutomaticEpisodeID != original.AutomaticEpisodeID || !o.CleanupConfirmed || (o.Phase != domain.SubscriptionObservationSending && o.Phase != domain.SubscriptionObservationUncertain) {
			return subscriptionDenied()
		}
		current = st.Generation
		if current != original.Generation && (current.Validate() != nil || generationHeld(st, original.Generation, true, false)) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil || !original.AccessOnly || current == original.Generation {
		return err
	}
	return s.deleteCapturedReference(ctx, id, original.Generation, current, "server_quota_reference_retained", original.ID)
}

// deleteCapturedReference settles the obsolete captured AccountLogin reference
// of a rotated frozen operation. The reference is retained, not deleted: only
// the current published generation authorizes use, so the captured generation
// grants nothing, and explicit logout or account deletion removes every
// remaining generation. The current generation must still be readable, so a
// rotation whose published credential is missing never settles silently.
func (s *Service) deleteCapturedReference(ctx context.Context, id, generation, current domain.ID, event string, operation domain.ID) error {
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	bundle, err := vault.Get(ctx, credentials.Ref{Owner: id, ID: current, Purpose: credentials.AccountLogin})
	clear(bundle)
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, event, "operation_id", operation, "result", "retained")
	return nil
}

// retireSettledServerQuotaReference retires the captured reference of a settled
// frozen automatic refresh. Caller holds account serialization.
func (s *Service) retireSettledServerQuotaReference(ctx context.Context, id domain.ID, original domain.ServerQuotaOperation) error {
	var current domain.ID
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		o := st.ServerQuota
		if o == nil || o.ID != original.ID || o.Active() || !o.CleanupConfirmed || !o.AccessOnly {
			return subscriptionDenied()
		}
		current = st.Generation
		if current != original.Generation && (current.Validate() != nil || generationHeld(st, original.Generation, false, false)) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil || current == original.Generation {
		return err
	}
	return s.deleteCapturedReference(ctx, id, original.Generation, current, "server_quota_reference_retained", original.ID)
}

// completeServerQuotaRetirement finishes the checked retirement of a settled
// frozen automatic refresh and then clears its durable fence. When the credit
// admitted by the same finish holds the captured generation, that credit owns
// the reference and its own checked retirement, so the quota fence is released
// without deleting it. Any other failure leaves the fence for retry. Caller
// holds account serialization.
func (s *Service) completeServerQuotaRetirement(ctx context.Context, id domain.ID, original domain.ServerQuotaOperation) error {
	var handedOff bool
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		c := a.Subscription.ServerCredit
		handedOff = c != nil && c.AccessOnly && c.Active() && c.Generation == original.Generation
		return nil
	}); err != nil {
		return err
	}
	if !handedOff {
		if err := s.retireSettledServerQuotaReference(ctx, id, original); err != nil {
			return err
		}
	}
	_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.reference.retired", struct {
		Account, Operation domain.ID
		HandedOff          bool
	}{id, original.ID, handedOff}, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerQuota
		if o == nil || o.ID != original.ID || o.Active() || !o.ReferenceRetirementPending {
			return struct{}{}, nil
		}
		o.ReferenceRetirementPending = false
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		s.logger.InfoContext(ctx, "server_quota_reference_fence_released", "operation_id", original.ID, "handed_off", handedOff)
	}
	return err
}

// reconcilePendingServerQuotaRetirements retries the checked retirement of
// settled automatic refreshes whose fence survived a failure or restart.
func (s *Service) reconcilePendingServerQuotaRetirements(ctx context.Context) error {
	type pending struct {
		Account   domain.ID
		Operation domain.ServerQuotaOperation
	}
	var owners []pending
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		rows, err := all(tx, domain.AccountKind)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return err
			}
			if a.Subscription != nil && a.Subscription.ServerQuota != nil && a.Subscription.ServerQuota.ReferenceRetirementPending && !a.Subscription.ServerQuota.Active() {
				owners = append(owners, pending{r.ID, *a.Subscription.ServerQuota})
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, owner := range owners {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := func() error {
			unlock, err := s.lockAccounts(bounded)
			if err != nil {
				return err
			}
			defer unlock()
			return s.completeServerQuotaRetirement(bounded, owner.Account, owner.Operation)
		}()
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "server_quota_reference_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code)
		}
	}
	return nil
}
