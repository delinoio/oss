// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

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
			if o.AccessOnly && o.Epoch != s.subscriptionServerEpoch() && o.Phase == domain.SubscriptionObservationUncertain && !o.CleanupConfirmed {
				owners = append(owners, retained{r.ID, *o})
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, owner := range owners {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := reconcileServerQuotaRuntime(bounded, s.Store.Root(), owner.Operation)
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "server_quota_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code)
			continue
		}
		_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.quota.reconciled", owner, func(tx *store.Tx) (any, error) {
			r, a, err := subscriptionAccount(tx, owner.Account, 0)
			if err != nil {
				return nil, err
			}
			o := a.Subscription.ServerQuota
			if o == nil || *o != owner.Operation {
				return nil, subscriptionDenied()
			}
			o.CleanupConfirmed = true
			o.Phase = domain.SubscriptionObservationFailed
			o.ErrorCode = domain.Canceled
			a.Subscription.QuotaState = domain.ObservationFailed
			_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			return struct{}{}, err
		})
		if err != nil {
			return err
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
