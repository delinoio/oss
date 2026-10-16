// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// claudeUsageStorageOpener exclusively locks the usage profile of an account and
// returns its native credential storage. It performs no installation discovery.
type claudeUsageStorageOpener func(ctx context.Context, scope nativeScope) (claudeUsageStorage, func() error, error)

func openClaudeUsageStorage(ctx context.Context, scope nativeScope) (claudeUsageStorage, func() error, error) {
	lease, err := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeUsage)
	if err != nil {
		return nil, nil, err
	}
	return claude.NewUsageStore(lease.Root), lease.Close, nil
}

// removeClaudeUsageNative removes only the usage profile's native credential
// storage (the exact Keychain item and the profile-owned file) and, when asked,
// the usage profile itself. A locked or unremovable profile returns an error and
// leaves the removal obligation durable.
func (s *Service) removeClaudeUsageNative(ctx context.Context, id domain.ID, deleteProfile bool) error {
	scope := s.scopeFor(id)
	if scope.Profiles == nil {
		return subscriptionDenied()
	}
	exists, err := scope.Profiles.Exists(ctx, nativeprofile.DeleteScope{Account: id, Purpose: nativeprofile.PurposeUsage, Harness: domain.ClaudeCode})
	if err != nil {
		return err
	}
	if !exists && s.claudeUsageStorageOpen == nil {
		// No usage profile was ever created, so no native storage can exist. The
		// exact Keychain item is derived from the profile directory and is only
		// created by a login inside it.
		return nil
	}
	open := s.claudeUsageStorageOpen
	if open == nil {
		open = openClaudeUsageStorage
	}
	storage, release, err := open(ctx, scope)
	if err != nil {
		return err
	}
	removeErr := storage.Remove(ctx)
	if releaseErr := release(); releaseErr != nil && removeErr == nil {
		removeErr = releaseErr
	}
	if removeErr != nil {
		return removeErr
	}
	if deleteProfile && exists {
		if _, err := scope.Profiles.Delete(ctx, nativeprofile.DeleteScope{Account: id, Purpose: nativeprofile.PurposeUsage, Harness: domain.ClaudeCode}); err != nil {
			return err
		}
	}
	return nil
}

// completeClaudeUsageCleanup finishes the durable removal obligations of one
// account: native storage (and optionally the profile) first, then every retired
// claude-usage reference, and only then clears the markers. A failure keeps the
// obligation and is never reported as success.
func (s *Service) completeClaudeUsageCleanup(ctx context.Context, id domain.ID, deleteProfile bool) (err error) {
	var cleanup bool
	var retired []domain.ID
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription != nil && a.Subscription.ClaudeUsage != nil {
			usage := a.Subscription.ClaudeUsage
			cleanup = usage.CleanupPending
			retired = slices.Clone(usage.RetiredCredentials)
			// A disconnected connection with no operation owns no usage profile
			// any more, so a retried cleanup removes the profile as well.
			deleteProfile = deleteProfile || usage.Operation == nil && !usage.Linked()
		}
		return nil
	})
	unlock()
	if err != nil || !cleanup && len(retired) == 0 {
		return err
	}
	defer func() {
		result := credentialRemovalConfirmed
		if err != nil {
			result = credentialRemovalRetained
		}
		s.logger.InfoContext(ctx, "claude_usage_cleanup", "account_id", id, "result", result, "references", len(retired), "storage", cleanup)
	}()
	if cleanup {
		if err = s.removeClaudeUsageNative(ctx, id, deleteProfile); err != nil {
			return err
		}
	}
	unlock, err = s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if len(retired) != 0 {
		vault, e := s.secrets()
		if e != nil {
			return e
		}
		for _, credential := range retired {
			if err = vault.Delete(ctx, credentials.Ref{Owner: id, ID: credential, Purpose: credentials.ClaudeUsage}); err != nil {
				return err
			}
		}
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.cleanup-confirmed", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
			return struct{}{}, nil
		}
		usage := a.Subscription.ClaudeUsage
		usage.RetiredCredentials = slices.DeleteFunc(usage.RetiredCredentials, func(credential domain.ID) bool { return slices.Contains(retired, credential) })
		if cleanup {
			usage.CleanupPending = false
			if o := usage.Operation; o != nil && o.State == domain.ClaudeLimitLoginRecovery && !o.NativeStarted {
				// The joined operation's storage is now proven empty. A failed
				// operation retains neither a pending identity nor a staged
				// credential: the reference becomes a removal obligation.
				addClaudeUsageRetired(usage, o.CredentialID)
				o.CredentialID, o.Identity = "", nil
				o.State = domain.ClaudeLimitLoginFailed
			}
		}
		if usage.Generation == "" && usage.Operation == nil && !usage.CleanupPending && len(usage.RetiredCredentials) == 0 && usage.Identity == nil {
			a.Subscription.ClaudeUsage = nil
		}
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	return err
}

// retireClaudeUsage ends the account's usage connection as part of an explicit
// logout or account deletion: reads stop, live work is cancelled, the native
// storage and usage profile are removed and the state is cleared. The caller
// holds the account gate. A busy profile returns an error so the caller keeps
// its own retry obligation; the other credential removal is unchanged.
func (s *Service) retireClaudeUsage(ctx context.Context, id domain.ID) error {
	var present bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		present = a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionClaude && a.Subscription != nil && a.Subscription.ClaudeUsage != nil
		return nil
	})
	if err != nil {
		return err
	}
	// The usage profile of a server-token account may exist without durable
	// state only after an interrupted start; the profile registry is the proof.
	scope := s.scopeFor(id)
	profiled := false
	if scope.Profiles != nil {
		profiled, err = scope.Profiles.Exists(ctx, nativeprofile.DeleteScope{Account: id, Purpose: nativeprofile.PurposeUsage, Harness: domain.ClaudeCode})
		if err != nil {
			return err
		}
	}
	if !present && !profiled {
		return nil
	}
	if present {
		_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.retire", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
			r, a, err := accountFromTx(tx, id, 0)
			if err != nil {
				return nil, err
			}
			revokeClaudeUsage(claudeUsageStateOf(&a))
			return struct{}{}, putClaudeUsage(tx, r, a)
		})
		if err != nil {
			return err
		}
	}
	// The caller holds the account gate, so the run cannot settle here. Callers
	// join live work before taking the gate (stopClaudeUsageWork); a run that is
	// still stopping keeps the profile locked, so removal is retried instead of
	// racing its native child.
	if run := s.cancelClaudeUsageRun(id); run != nil {
		select {
		case <-run.done:
		default:
			s.logger.WarnContext(ctx, "claude_usage_retire", "account_id", id, "result", credentialRemovalRetained, "reason", "usage_work_still_stopping")
			return domain.Fail(domain.Unavailable, "Claude usage work is still stopping.", "Retry once the usage work has finished.")
		}
	}
	if err := s.removeClaudeUsageNative(ctx, id, true); err != nil {
		s.logger.WarnContext(ctx, "claude_usage_retire", "account_id", id, "result", credentialRemovalRetained, "code", domain.SafeError(err).Code)
		return err
	}
	if present {
		vault, err := s.secrets()
		if err != nil {
			return err
		}
		var retired []domain.ID
		if err := s.Store.Read(ctx, func(tx *store.Tx) error {
			_, a, err := accountFromTx(tx, id, 0)
			if err == nil && a.Subscription != nil && a.Subscription.ClaudeUsage != nil {
				retired = slices.Clone(a.Subscription.ClaudeUsage.RetiredCredentials)
			}
			return err
		}); err != nil {
			return err
		}
		for _, credential := range retired {
			if err := vault.Delete(ctx, credentials.Ref{Owner: id, ID: credential, Purpose: credentials.ClaudeUsage}); err != nil {
				return err
			}
		}
		_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.retired", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
			r, a, err := accountFromTx(tx, id, 0)
			if err != nil {
				return nil, err
			}
			a.Subscription.ClaudeUsage = nil
			return struct{}{}, putClaudeUsage(tx, r, a)
		})
		if err != nil {
			return err
		}
	}
	s.logger.InfoContext(ctx, "claude_usage_retire", "account_id", id, "result", credentialRemovalConfirmed)
	return nil
}

// reconcileClaudeUsage settles state a previous server epoch left behind. It
// never repeats a browser sign-in and never repeats a refresh exchange: a login
// whose child belonged to a dead epoch fails closed with its storage removed,
// an interrupted fetch is simply idle again (a GET has no side effect) and an
// interrupted refresh-login requires an explicit reconnect.
func (s *Service) reconcileClaudeUsage(ctx context.Context, id domain.ID) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.reconcile", struct{ Account domain.ID }{id}, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if a.Subscription == nil || a.Subscription.ClaudeUsage == nil || !claudeUsageNeedsReconcile(*a.Subscription.ClaudeUsage, s.subscriptionServerEpoch()) {
			return struct{}{}, nil
		}
		usage := a.Subscription.ClaudeUsage
		now := time.Now().UTC()
		if o := usage.Operation; o != nil && o.Active() && o.State != domain.ClaudeLimitLoginConfirming && o.Epoch != s.subscriptionServerEpoch() {
			if o.CredentialID != "" {
				usage.RetiredCredentials = append(usage.RetiredCredentials, o.CredentialID)
				o.CredentialID = ""
			}
			o.State, o.NativeStarted, o.Failure, o.Identity = domain.ClaudeLimitLoginFailed, false, domain.ClaudeLimitFailureInterrupted, nil
			usage.CleanupPending = true
		}
		if usage.Read.Active() && usage.Read.Epoch != s.subscriptionServerEpoch() {
			if usage.Read.Phase == domain.ClaudeLimitReadRefreshing || usage.Read.RefreshClaimed {
				// The rotated bundle may exist only in a dead process. The old one
				// may be consumed, so the connection requires an explicit reconnect.
				usage.RecordFailure(domain.ClaudeLimitFailureRefreshUncertain, now, 0)
				usage.CleanupPending = true
			} else {
				usage.RecordFailure(domain.ClaudeLimitFailureInterrupted, now, 0)
			}
			usage.Read.Phase, usage.Read.ID, usage.Read.Epoch, usage.Read.StartedAt = "", "", "", nil
		}
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	unlock()
	if err != nil {
		return err
	}
	return s.completeClaudeUsageCleanup(ctx, id, false)
}

// claudeUsageNeedsReconcile selects state that no live goroutine of this server
// epoch owns.
func claudeUsageNeedsReconcile(usage domain.ClaudeLimitState, epoch domain.ID) bool {
	if o := usage.Operation; o != nil && o.Active() && o.State != domain.ClaudeLimitLoginConfirming && o.Epoch != epoch {
		return true
	}
	if usage.Read.Active() && usage.Read.Epoch != epoch {
		return true
	}
	return usage.CleanupPending && (usage.Operation == nil || !usage.Operation.NativeStarted) || len(usage.RetiredCredentials) != 0
}

// runClaudeUsage is the single scanner of the usage lane: it starts accepted
// logins, executes accepted reads, queues the due automatic reads and retries
// durable cleanup with bounded backoff.
func (s *Service) runClaudeUsage(ctx context.Context) {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	child, stop := context.WithCancel(ctx)
	defer stop()
	var workers sync.WaitGroup
	type finished struct {
		account domain.ID
		cleanup bool
		failed  bool
	}
	done := make(chan finished, 16)
	running := map[domain.ID]bool{}
	retries := map[domain.ID]cleanupRetry{}
	defer func() { stop(); workers.Wait() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		type candidate struct {
			account domain.ID
			login   bool
			read    bool
			cleanup bool
		}
		var candidates []candidate
		// Due automatic reads are detected by the same scan, so an idle server
		// decodes the account inventory once per iteration.
		var due []domain.ID
		eligible := map[domain.ID]bool{}
		now := time.Now()
		err := s.Store.Read(child, func(tx *store.Tx) error {
			records, err := all(tx, domain.AccountKind)
			if err != nil {
				return err
			}
			for _, r := range records {
				a, err := store.Decode[domain.Account](r)
				if err != nil {
					return err
				}
				if a.Subscription == nil || a.Subscription.ClaudeUsage == nil {
					continue
				}
				usage := a.Subscription.ClaudeUsage
				epoch := s.subscriptionServerEpoch()
				if claudeUsageTracked(a) && a.Connection != nil && usage.DueAt(now) {
					due = append(due, r.ID)
				}
				switch {
				case usage.Operation != nil && usage.Operation.State == domain.ClaudeLimitLoginPreparing && !usage.Operation.NativeStarted && usage.Operation.Epoch == epoch:
					candidates = append(candidates, candidate{account: r.ID, login: true})
				case usage.Read.Active() && usage.Read.Epoch == epoch:
					candidates = append(candidates, candidate{account: r.ID, read: true})
				case claudeUsageNeedsReconcile(*usage, epoch):
					eligible[r.ID] = true
					if retries[r.ID].due(now) {
						candidates = append(candidates, candidate{account: r.ID, cleanup: true})
					}
				}
			}
			return nil
		})
		if err != nil && child.Err() == nil {
			s.logger.WarnContext(ctx, "claude_usage_scan_failed", "code", domain.SafeError(err).Code)
		} else if err == nil {
			for account := range retries {
				if !eligible[account] {
					delete(retries, account)
				}
			}
		}
		if err == nil {
			if err := s.acceptDueClaudeUsage(child, due, now.UTC()); err != nil && child.Err() == nil {
				s.logger.WarnContext(ctx, "claude_usage_queue_failed", "code", domain.SafeError(err).Code)
			}
		}
		for _, next := range candidates {
			if running[next.account] || len(running) >= 16 {
				continue
			}
			running[next.account] = true
			workers.Add(1)
			go func() {
				defer workers.Done()
				result := finished{account: next.account, cleanup: next.cleanup}
				switch {
				case next.login:
					s.runClaudeUsageLogin(child, next.account)
				case next.read:
					s.runClaudeUsageRead(child, next.account)
				default:
					if err := s.reconcileClaudeUsage(child, next.account); err != nil && child.Err() == nil {
						result.failed = true
						s.logger.WarnContext(child, "claude_usage_cleanup_retry", "account_id", next.account, "code", domain.SafeError(err).Code)
					}
				}
				done <- result
			}()
		}
		select {
		case <-child.Done():
			return
		case result := <-done:
			delete(running, result.account)
			if result.cleanup {
				retry := retries[result.account]
				retries[result.account] = retry.after(time.Now(), result.failed, s.cleanupRetryBase())
			}
		case <-ticker.C:
		}
	}
}
