// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// A Grok logout or refresh can be accepted while another lease (an execution)
// still holds the account. The accepted operation then waits, queued and
// unclaimed, until that lease is released. The lease's own Finish can commit its
// release and still fail afterwards, either because the removal of the
// superseded generation failed or because the commit acknowledgement was lost;
// the owned-error guard of that Finish then fences the account for recovery.
// That fence is not a lease fence: no lease remains, nothing was sent for the
// queued operation and no claim exists. Left alone it would strand the accepted
// operation, because the scanner never claims a fenced account and settles only
// operations that reached the recovery state.
//
// grokQueuedFenceNeedsReconcile recognizes exactly that shape: a connected Grok
// account that is fenced, holds no lease, and owns a queued, unclaimed logout or
// refresh of the current server epoch. An operation fenced by a restart is in
// the recovery state instead and is settled by its own recovery kind.
func grokQueuedFenceNeedsReconcile(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.ProviderID != "" || a.Removal != nil || a.Connection == nil || st == nil || st.Generation == "" || st.Lease != nil || !st.RecoveryRequired {
		return false
	}
	o := st.ServerOperation
	if o == nil || o.State != domain.SubscriptionPreparing || o.NativeStarted || o.CodeConsumed || o.RefreshClaimed || o.GrokRevocation != "" || st.Pending == nil || st.Pending.ID != o.ID || st.Pending.Phase != domain.SubscriptionQueued || st.Pending.Canceled {
		return false
	}
	return o.Action == domain.SubscriptionLogout || o.Action == domain.SubscriptionRefresh
}

// grokHealthAfterQueuedFence is the health a queued operation's account returns
// to when its fence is cleared: an accepted logout keeps readiness revoked until
// a logout settles, and a refresh restores the health it was admitted under.
func grokHealthAfterQueuedFence(o domain.ServerSubscriptionOperation) domain.AccountHealth {
	switch {
	case o.Action == domain.SubscriptionLogout:
		return domain.AccountRevoked
	case o.HealthBefore != "":
		return o.HealthBefore
	}
	return domain.AccountReady
}

// reconcileGrokQueuedFence clears the fence left by a Finish that released its
// lease and then failed, so the queued operation can proceed. It first finishes
// the vault cleanup that Finish owed, keeping only the generation the account
// references, and clears the fence only after that removal is confirmed; a
// failed removal keeps the fence and the scanner retries with bounded backoff.
// It holds the account gate for the whole check, so it cannot race the Finish
// that produced the fence.
func (s *Service) reconcileGrokQueuedFence(parent context.Context, id, operation domain.ID) error {
	ctx, cancel := context.WithTimeout(parent, 2*grokCleanupTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var current domain.Account
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		if !grokQueuedFenceNeedsReconcile(a) || a.Subscription.ServerOperation.ID != operation || a.Subscription.ServerOperation.Epoch != s.subscriptionServerEpoch() {
			return subscriptionDenied()
		}
		current = a
		return nil
	})
	if err != nil {
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	cleanup, stop := grokBookkeepingContext(ctx, grokCleanupTimeout)
	err = cleanupSubscriptionReferences(cleanup, vault, id, current.Subscription.Generation, protectedGenerations(current.Subscription)...)
	stop()
	if err != nil {
		s.logger.WarnContext(ctx, "grok_queued_fence_cleanup_pending", "phase", grokLogPhaseCleanup, "operation_id", operation, "correlation_id", operation, "step", grokStepCleanup, "action", current.Subscription.ServerOperation.Action, "code", domain.SafeError(err).Code)
		return err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.queued-fence-reconcile", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !grokQueuedFenceNeedsReconcile(a) || a.Subscription.ServerOperation.ID != operation || a.Subscription.ServerOperation.Epoch != s.subscriptionServerEpoch() || a.Subscription.Generation != current.Subscription.Generation {
			return nil, subscriptionDenied()
		}
		a.Subscription.RecoveryRequired = false
		if a.Health == domain.AccountFailed {
			a.Health = grokHealthAfterQueuedFence(*a.Subscription.ServerOperation)
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	s.logger.Log(ctx, slog.LevelInfo, "grok_queued_fence_reconciled", "phase", grokLogPhaseCleanup, "operation_id", operation, "correlation_id", operation, "step", grokStepCleanup, "action", current.Subscription.ServerOperation.Action)
	return nil
}

// grokHealthAfterExecuteRecovery is the health an account returns to when the
// original Worker's recovery Finish releases a fenced execution lease. A logout
// accepted while the lease ran keeps readiness revoked until a logout settles
// (pending, or settled as failed without sending anything); a refresh accepted
// while the lease ran returns the health it was admitted under, pending or
// settled as failed by a restart. An operation that predates the lease says
// nothing about it.
func grokHealthAfterExecuteRecovery(a domain.Account, lease domain.SubscriptionLease) domain.AccountHealth {
	st := a.Subscription
	if a.GrokLoggingOut() {
		return domain.AccountRevoked
	}
	o := st.ServerOperation
	if o == nil || !o.StartedAt.After(lease.StartedAt) {
		return domain.AccountReady
	}
	switch {
	case o.Action == domain.SubscriptionLogout && o.State == domain.SubscriptionFailed:
		return domain.AccountRevoked
	case o.Action == domain.SubscriptionRefresh && (o.State == domain.SubscriptionFailed || st.Pending != nil && st.Pending.ID == o.ID):
		return grokHealthAfterQueuedFence(*o)
	}
	return domain.AccountReady
}
