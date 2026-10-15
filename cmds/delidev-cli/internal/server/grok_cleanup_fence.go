// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Ownerless cleanup fence. A Finish that published a rotated credential (an
// execute whose native process rotated it, a refresh, a login) clears its lease
// and pending operation in the settlement commit and only then removes the
// superseded vault generations. When that removal fails the account is fenced
// for recovery with nothing left that owns the fence: no lease for a Worker to
// report against, no pending operation for the queued-fence reconcile, no
// running operation. The published generation is intact and the only outstanding
// work is the idempotent removal of every other generation, so the scanner owns
// it and retries with bounded backoff.
//
//	account shape                                                                | owner of the fence
//	fenced, connected, no lease, no pending operation, no active operation       | the scanner (this file)
//	fenced, queued lifecycle operation behind the finished lease                 | the queued-fence reconcile
//	fenced, lease kept                                                           | the lease's exact Worker (Finish) or the stale-lease rules
//	fenced, claimed or pending operation                                         | the operation's own recovery
//
// The recovery removes every generation except the current one (and the quota
// and credit generations that protectedGenerations lists), then clears the fence
// and restores readiness. It never touches the credential, so it is safe to
// repeat.

// grokOwnerlessCleanupFence selects the shape described above.
func grokOwnerlessCleanupFence(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.ProviderID != "" || a.Removal != nil || a.Connection == nil || st == nil {
		return false
	}
	if !st.RecoveryRequired || st.Generation == "" || st.RevokedGeneration != "" || st.Lease != nil || len(st.Members) != 0 || st.Pending != nil {
		return false
	}
	o := st.ServerOperation
	return o == nil || !o.Active() && !o.NativeStarted && o.CleanupPhase == ""
}

// recoverGrokOwnerlessCleanupFence repeats the vault removal of an ownerless
// cleanup fence and clears the fence once the removal is confirmed. It holds the
// account gate, so it cannot race the Finish that produced the fence.
func (s *Service) recoverGrokOwnerlessCleanupFence(parent context.Context, id domain.ID) error {
	ctx, cancel := context.WithTimeout(parent, 2*grokCleanupTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var current domain.Account
	stillFenced := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		current, stillFenced = a, grokOwnerlessCleanupFence(a)
		return nil
	})
	if err != nil || !stillFenced {
		// Another actor settled or re-owned the fence meanwhile.
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	cleanup, cancelCleanup := grokBookkeepingContext(ctx, grokCleanupTimeout)
	err = cleanupSubscriptionReferences(cleanup, vault, id, current.Subscription.Generation, protectedGenerations(current.Subscription)...)
	cancelCleanup()
	if err != nil {
		s.logger.WarnContext(ctx, "grok_cleanup_fence_pending", "phase", grokLogPhaseCleanup, "account_id", id, "step", grokStepCleanup, "code", domain.SafeError(err).Code)
		return err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.subscription.cleanup-fence", struct{ Account, Generation domain.ID }{id, current.Subscription.Generation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		// The fence is cleared only for the exact shape and generation whose
		// removal was just confirmed.
		if !grokOwnerlessCleanupFence(a) || a.Subscription.Generation != current.Subscription.Generation {
			return nil, subscriptionDenied()
		}
		a.Subscription.RecoveryRequired = false
		if a.Health == domain.AccountFailed {
			a.Health = domain.AccountReady
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	s.logger.Log(ctx, slog.LevelInfo, "grok_cleanup_fence_reconciled", "phase", grokLogPhaseCleanup, "account_id", id, "step", grokStepCleanup)
	return nil
}
