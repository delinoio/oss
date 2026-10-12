// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Restart reconciles only the original runtime cleanup. It never sends login,
// consume or quota, and possible consumption retains its original provider key.
func (s *Service) reconcileRetainedServerCredits(ctx context.Context) error {
	type retained struct {
		Account   domain.ID
		Operation domain.ServerCreditOperation
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
			if a.Subscription != nil && a.Subscription.ServerCredit != nil {
				o := a.Subscription.ServerCredit
				if o.AccessOnly && o.Active() && o.AttemptEpoch != s.subscriptionServerEpoch() {
					owners = append(owners, retained{r.ID, *o})
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, owner := range owners {
		err := func() error {
			if !owner.Operation.CleanupConfirmed {
				if err := reconcileServerCreditRuntime(ctx, s.Store.Root(), owner.Operation); err != nil {
					return err
				}
			}
			unlock, err := s.lockAccounts(ctx)
			if err != nil {
				return err
			}
			defer unlock()
			_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.reconcile.cleanup", owner, func(tx *store.Tx) (any, error) {
				r, a, err := subscriptionAccount(tx, owner.Account, 0)
				if err != nil {
					return nil, err
				}
				if !reflect.DeepEqual(a.Subscription.ServerCredit, &owner.Operation) {
					return nil, subscriptionDenied()
				}
				a.Subscription.ServerCredit.CleanupConfirmed = true
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return struct{}{}, err
			})
			if err != nil {
				return err
			}
			owner.Operation.CleanupConfirmed = true
			if err = s.retireServerCreditReference(ctx, owner.Account, owner.Operation); err != nil {
				return err
			}
			_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.reconciled", owner, func(tx *store.Tx) (any, error) {
				r, a, err := subscriptionAccount(tx, owner.Account, 0)
				if err != nil {
					return nil, err
				}
				o := a.Subscription.ServerCredit
				if !reflect.DeepEqual(o, &owner.Operation) {
					return nil, subscriptionDenied()
				}
				if o.Outcome.Valid() {
					o.Phase = domain.SubscriptionObservationSucceeded
					o.ErrorCode = ""
				} else if o.EverSent {
					o.Phase = domain.SubscriptionObservationUncertain
					o.ErrorCode = domain.RecoveryRequired
				} else {
					o.Phase = domain.SubscriptionObservationFailed
					o.ErrorCode = domain.Canceled
				}
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return struct{}{}, err
			})
			return err
		}()
		if err != nil {
			s.logger.WarnContext(ctx, "server_credit_cleanup_retained", "operation_id", owner.Operation.ID, "code", creditCode(err))
		}
	}
	return nil
}
func reconcileServerCreditRuntime(ctx context.Context, root string, o domain.ServerCreditOperation) error {
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	processRoot := filepath.Join(runtimeRoot, "processes")
	if o.Validate() != nil || !o.AccessOnly || o.Phase != domain.SubscriptionObservationUncertain || security.CheckPrivateDir(runtimeRoot) != nil || security.CheckPrivateDir(processRoot) != nil {
		return subscriptionDenied()
	}
	if err := lockFailedServerLoginControllers(ctx, processRoot, o.AttemptID); err != nil {
		return err
	}
	if err := process.ReconcileOwnerContext(ctx, processRoot, o.AttemptID); err != nil {
		return err
	}
	if err := cleanupFailedServerLoginProbes(ctx, runtimeRoot, o.AttemptID, true); err != nil {
		return err
	}
	home := filepath.Join(runtimeRoot, "credit", string(o.AttemptID))
	if _, err := os.Lstat(filepath.Join(home, "codex", "auth.json")); !os.IsNotExist(err) {
		return subscriptionDenied()
	}
	return cleanupFailedServerLoginDirectory(home)
}

// Caller holds account serialization. Native cleanup and the exact retained
// operation authorize only retirement of its obsolete captured reference.
func (s *Service) retireServerCreditReference(ctx context.Context, id domain.ID, original domain.ServerCreditOperation) error {
	var current domain.ID
	unresolvedSend := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := subscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		st := a.Subscription
		o := st.ServerCredit
		if original.Validate() != nil || !sameServerCreditProof(o, original) || o == nil || o.Validate() != nil || o.ID != original.ID || o.Epoch != original.Epoch || o.Actor != original.Actor || o.Generation != original.Generation || o.ConnectionID != original.ConnectionID || o.AttemptID != original.AttemptID || o.FinishID != original.FinishID || !o.RequestedAt.Equal(original.RequestedAt) || o.AccessOnly != original.AccessOnly || !o.CleanupConfirmed || (o.Phase == domain.SubscriptionObservationQueued) {
			return subscriptionDenied()
		}
		// An unresolved provider send retains its original credential reference.
		// Checked cleanup does not manufacture an outcome or transfer reconciliation
		// to the Execute writer's replacement generation.
		unresolvedSend = o.EverSent && !o.Outcome.Valid()
		current = st.Generation
		if current != original.Generation && (current.Validate() != nil || st.Lease != nil && st.Lease.Generation == original.Generation || st.ServerQuota != nil && st.ServerQuota.Active() && st.ServerQuota.Generation == original.Generation || st.Observation != nil && st.Observation.Active() && st.Observation.Generation == original.Generation || st.ServerOperation != nil && (st.ServerOperation.Active() || st.ServerOperation.NativeStarted) && st.ServerOperation.Generation == original.Generation) {
			return subscriptionDenied()
		}
		return nil
	})
	if err != nil || !original.AccessOnly || unresolvedSend || current == original.Generation {
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
	s.logger.InfoContext(ctx, "server_credit_reference_retired", "operation_id", original.ID)
	return nil
}
