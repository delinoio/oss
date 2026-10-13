// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Startup reconciles only the original runtime cleanup of prior-epoch
// access-only credits. It never sends login, consume or quota, and possible
// consumption retains its original provider key for explicit reconciliation.
// Each owner has its own bound so a stuck retained process cannot block startup.
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
				if o.AccessOnly && o.Phase == domain.SubscriptionObservationUncertain && o.AttemptEpoch != s.subscriptionServerEpoch() {
					owners = append(owners, retained{r.ID, *o})
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, owner := range owners {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.reconcileRetainedServerCredit(bounded, owner.Account, owner.Operation)
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "server_credit_cleanup_retained", "operation_id", owner.Operation.ID, "code", domain.SafeError(err).Code, "cleanup_confirmed", owner.Operation.CleanupConfirmed)
			continue
		}
		s.logger.InfoContext(ctx, "server_credit_cleanup_reconciled", "operation_id", owner.Operation.ID)
	}
	return nil
}

func (s *Service) reconcileRetainedServerCredit(ctx context.Context, account domain.ID, operation domain.ServerCreditOperation) error {
	if !operation.CleanupConfirmed {
		if err := reconcileAccessOnlyRuntime(ctx, s.Store.Root(), "credit", operation.AttemptID); err != nil {
			return err
		}
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// Checkpoint original native cleanup independently of vault cleanup.
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.reconcile.cleanup", operation, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		if !sameAccessCredit(a.Subscription.ServerCredit, operation) || a.Subscription.ServerCredit.Phase != domain.SubscriptionObservationUncertain {
			return nil, subscriptionDenied()
		}
		a.Subscription.ServerCredit.CleanupConfirmed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		return err
	}
	operation.CleanupConfirmed = true
	// A possible send without a checkpointed outcome keeps its reference and
	// original key. Everything else is terminal after checked retirement.
	terminal := operation.Outcome.Valid() || !operation.EverSent
	if terminal {
		if err := s.retireServerCreditReference(ctx, account, operation); err != nil {
			return err
		}
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.server.credit.reconciled", operation, func(tx *store.Tx) (any, error) {
		r, a, err := subscriptionAccount(tx, account, 0)
		if err != nil {
			return nil, err
		}
		o := a.Subscription.ServerCredit
		if !sameAccessCredit(o, operation) || o.Phase != domain.SubscriptionObservationUncertain || !o.CleanupConfirmed {
			return nil, subscriptionDenied()
		}
		accessCreditSettlePhase(o, domain.Canceled)
		if o.Phase == domain.SubscriptionObservationUncertain {
			o.ErrorCode = domain.RecoveryRequired
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}
