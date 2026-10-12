// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"path/filepath"
)

// Recovery joins only the original ownership journal. It cannot launch a CLI,
// resend code, publish a token or turn a previous-epoch login into a connection.
func (s *Service) recoverServerClaudeToken(ctx context.Context, id, operation domain.ID) error {
	var o domain.ServerSubscriptionOperation
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || !st.ManagedClaudeToken || !st.RecoveryRequired || st.ServerOperation == nil || st.ServerOperation.ID != operation || !st.ServerOperation.NativeStarted || st.Lease != nil {
			return subscriptionDenied()
		}
		o = *st.ServerOperation
		return nil
	})
	if err != nil {
		return err
	}
	if !o.ClaudeCleanupConfirmed {
		base := filepath.Join(s.Store.Root(), "subscription-runtime")
		processRoot := filepath.Join(base, "processes")
		if err := lockFailedServerLoginControllers(ctx, processRoot, o.ID); err != nil {
			return err
		}
		if err := process.ReconcileOwnerContext(ctx, processRoot, o.ID); err != nil {
			return err
		}
		if err := cleanupFailedServerLoginProbes(ctx, base, o.ID, true); err != nil {
			return err
		}
		if err := cleanupFailedServerLoginDirectory(filepath.Join(base, "claude-token", string(o.ID))); err != nil {
			return err
		}
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.clearClaudeLoginInput(o.ID)
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	// Freeze the independently confirmed native checkpoint before external vault
	// removal. Restart can repeat cleanup, never authorization or success.
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.token.recovery.native", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.RecoveryRequired || st.Lease != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.ClaudeCleanupConfirmed = true
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, e
	})
	if err != nil {
		return err
	}
	if err := cleanupSubscriptionReferences(ctx, vault, id, ""); err != nil {
		return err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.token.recovery.settle", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, id, 0)
		if e != nil {
			return nil, e
		}
		st := a.Subscription
		if st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.RecoveryRequired || !st.ServerOperation.ClaudeCleanupConfirmed || st.Lease != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.NativeStarted = false
		st.ServerOperation.State = domain.SubscriptionFailed
		st.Pending = nil
		st.Generation = ""
		st.IdentityCommitment = ""
		st.RecoveryRequired = false
		a.Connection = nil
		a.Health = domain.AccountDisconnected
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, e
	})
	return err
}
