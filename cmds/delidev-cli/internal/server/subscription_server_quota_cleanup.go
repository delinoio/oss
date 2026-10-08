// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
			err = reconcileServerQuotaRuntime(bounded, s.Store.Root(), owner.Operation)
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
			return err
		}()
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "server_quota_reference_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code)
			continue
		}
		s.logger.InfoContext(ctx, "server_quota_cleanup_reconciled", "operation_id", owner.Operation.ID)
	}
	return nil
}
func reconcileServerQuotaRuntime(ctx context.Context, root string, o domain.ServerQuotaOperation) error {
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	processRoot := filepath.Join(runtimeRoot, "processes")
	if o.Validate() != nil || !o.AccessOnly || o.Phase != domain.SubscriptionObservationUncertain || security.CheckPrivateDir(runtimeRoot) != nil || security.CheckPrivateDir(processRoot) != nil {
		return subscriptionDenied()
	}
	// A missing index cannot prove no send. Require the original retained index
	// and independently exclude a still-live controller before process recovery.
	if err := lockFailedServerLoginControllers(ctx, processRoot, o.ID); err != nil {
		return err
	}
	if err := process.ReconcileOwnerContext(ctx, processRoot, o.ID); err != nil {
		return err
	}
	if err := cleanupFailedServerLoginProbes(ctx, runtimeRoot, o.ID, true); err != nil {
		return err
	}
	home := filepath.Join(runtimeRoot, "quota", string(o.ID))
	if _, err := os.Lstat(filepath.Join(home, "codex", "auth.json")); !os.IsNotExist(err) {
		return subscriptionDenied()
	}
	return cleanupFailedServerLoginDirectory(home)
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
		if original.Validate() != nil || o == nil || o.Validate() != nil || o.ID != original.ID || o.Epoch != original.Epoch || o.Actor != original.Actor || o.Generation != original.Generation || o.ConnectionID != original.ConnectionID || o.FinishID != original.FinishID || !o.RequestedAt.Equal(original.RequestedAt) || o.AccessOnly != original.AccessOnly || !o.CleanupConfirmed || (o.Phase != domain.SubscriptionObservationSending && o.Phase != domain.SubscriptionObservationUncertain) {
			return subscriptionDenied()
		}
		current = st.Generation
		if current != original.Generation && (current.Validate() != nil || st.Lease != nil && st.Lease.Generation == original.Generation || st.ServerCredit != nil && st.ServerCredit.Active() && st.ServerCredit.Generation == original.Generation || st.Observation != nil && st.Observation.Active() && st.Observation.Generation == original.Generation || st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) && st.ServerOperation.Generation == original.Generation) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil || !original.AccessOnly || current == original.Generation {
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	refs, err := vault.UnremovedReferences(ctx, id)
	if err != nil {
		return err
	}
	if len(refs) > 256 {
		return subscriptionDenied()
	}
	captured := credentials.Ref{Owner: id, ID: original.Generation, Purpose: credentials.AccountLogin}
	found, currentFound := false, false
	for _, ref := range refs {
		if ref.Owner != id || ref.Purpose != credentials.AccountLogin || ref.ID.Validate() != nil {
			return subscriptionDenied()
		}
		found = found || ref == captured
		currentFound = currentFound || ref.ID == current
	}
	if !found {
		return nil
	} // A durable prior deletion needs no second native send.
	if !currentFound {
		return subscriptionDenied()
	}
	if err = vault.Delete(ctx, captured); err != nil {
		return err
	}
	refs, err = vault.UnremovedReferences(ctx, id)
	if err != nil {
		return err
	}
	if len(refs) > 256 {
		return subscriptionDenied()
	}
	for _, ref := range refs {
		if ref == captured || ref.Owner != id || ref.Purpose != credentials.AccountLogin || ref.ID.Validate() != nil {
			return subscriptionDenied()
		}
	}
	s.logger.InfoContext(ctx, "server_quota_reference_retired", "operation_id", original.ID)
	return nil
}
