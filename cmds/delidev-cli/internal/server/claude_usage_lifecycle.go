// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Lifecycle ownership of the server-owned Claude usage connection.
//
// Exactly one native login per account runs in a registered run. Cancel,
// disconnect, account logout and shutdown cancel that run and join it before
// anything else acts on the account. Durable cleanup (owed vault references and
// native credential storage) is idempotent and is retried by the server
// subscription scanner, so neither a crash nor a failed deletion can resurrect
// usage authority or lose a staged credential.

var (
	errClaudeUsageCanceled = errors.New("claude usage login canceled")
	errClaudeUsageRevoked  = errors.New("claude usage login revoked")
	errClaudeUsageShutdown = errors.New("claude usage login interrupted by server shutdown")
)

type claudeUsageURL struct {
	URL   string
	Until time.Time
}

// claudeUsageRun is the single live login of one account.
type claudeUsageRun struct {
	account   domain.ID
	operation domain.ID
	cancel    context.CancelCauseFunc
	done      chan struct{}
}

// claudeUsageRuns is the registry of live usage logins. Its zero value is ready.
// Presentation (the authorization address) and the single-use approval code
// live only here, in memory, bound to their operation.
type claudeUsageRuns struct {
	mu      sync.Mutex
	closing bool
	jobs    sync.WaitGroup
	active  map[domain.ID]*claudeUsageRun
	codes   map[domain.ID][]byte
	urls    map[domain.ID]claudeUsageURL
}

// reserve registers the live login of account before its start is committed, so
// a second concurrent login or a login during shutdown is refused.
func (r *claudeUsageRuns) reserve(account, operation domain.ID, deadline time.Time) (*claudeUsageRun, context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return nil, nil, domain.Fail(domain.Unavailable, "The server is stopping.", "Retry after the server restarts.")
	}
	if r.active[account] != nil {
		return nil, nil, domain.Fail(domain.Conflict, "A Claude usage login is already running for this account.", "Cancel it or wait for it to finish.")
	}
	base := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	ctx, cancel := context.WithCancelCause(base)
	ctx, stop := context.WithDeadline(ctx, deadline)
	run := &claudeUsageRun{account: account, operation: operation, done: make(chan struct{})}
	run.cancel = func(cause error) { cancel(cause); stop() }
	if r.active == nil {
		r.active = map[domain.ID]*claudeUsageRun{}
	}
	r.active[account] = run
	r.jobs.Add(1)
	return run, ctx, nil
}

// release forgets a reservation whose start was never launched.
func (r *claudeUsageRuns) release(run *claudeUsageRun) {
	run.cancel(errClaudeUsageCanceled)
	r.finish(run)
}

// finish ends one run: it drops the registration and all in-memory input.
func (r *claudeUsageRuns) finish(run *claudeUsageRun) {
	r.mu.Lock()
	if r.active[run.account] == run {
		delete(r.active, run.account)
	}
	clear(r.codes[run.operation])
	delete(r.codes, run.operation)
	delete(r.urls, run.operation)
	r.mu.Unlock()
	close(run.done)
	r.jobs.Done()
}

// live reports whether account has a registered login.
func (r *claudeUsageRuns) live(account domain.ID) bool { return r.liveExcept(account, "") }

// liveExcept reports whether account has a registered login other than the one
// of operation, which is the caller itself and has already joined its child.
func (r *claudeUsageRuns) liveExcept(account, operation domain.ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.active[account]
	return run != nil && (operation == "" || run.operation != operation)
}

// cancelAndJoin cancels the live login of account (when it belongs to
// operation, or to any operation when operation is empty) and waits until its
// goroutine, including its native child, has been joined.
func (r *claudeUsageRuns) cancelAndJoin(ctx context.Context, account, operation domain.ID, cause error) error {
	r.mu.Lock()
	run := r.active[account]
	r.mu.Unlock()
	if run == nil || operation != "" && run.operation != operation {
		return nil
	}
	run.cancel(cause)
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	}
}

// close refuses new logins, interrupts every live run and joins all of them.
func (r *claudeUsageRuns) close() {
	r.mu.Lock()
	r.closing = true
	runs := make([]*claudeUsageRun, 0, len(r.active))
	for _, run := range r.active {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	for _, run := range runs {
		run.cancel(errClaudeUsageShutdown)
	}
	r.jobs.Wait()
}

func (r *claudeUsageRuns) setCode(operation domain.ID, code []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.codes == nil {
		r.codes = map[domain.ID][]byte{}
	}
	clear(r.codes[operation])
	r.codes[operation] = slices.Clone(code)
}

func (r *claudeUsageRuns) hasCode(operation domain.ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.codes[operation]) != 0
}

// takeCode hands the single-use code to its owner and forgets it.
func (r *claudeUsageRuns) takeCode(operation domain.ID) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	code := r.codes[operation]
	delete(r.codes, operation)
	return code
}

func (r *claudeUsageRuns) setURL(operation domain.ID, value claudeUsageURL) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.urls == nil {
		r.urls = map[domain.ID]claudeUsageURL{}
	}
	r.urls[operation] = value
}

func (r *claudeUsageRuns) url(operation domain.ID) claudeUsageURL {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.urls[operation]
}

func (r *claudeUsageRuns) clearInput(operation domain.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.codes[operation])
	delete(r.codes, operation)
	delete(r.urls, operation)
}

// closeClaudeUsage joins every live usage login. It runs before the account
// secrets are released so no native child or vault write outlives its owner.
func (s *Service) closeClaudeUsage() { s.claudeUsage.close() }

// claudeUsageActive reports whether the usage feature is active: the six RPCs
// are served, the read owner and its scheduler run and GetStatus advertises
// System capability 108. It is the single decision behind all of them, so the
// advertisement and the served operations can never diverge.
func (s *Service) claudeUsageActive() bool {
	return claudeServerUsageAdvertised && !s.claudeUsageDisabledTest
}

// claudeUsageIdentityCommitment is the keyed commitment of one reviewed OAuth
// identity. It is bound to this server and account, carries no raw identity and
// is the only identity proof the confirmation echoes.
func (s *Service) claudeUsageIdentityCommitment(account domain.ID, email, organization string) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/claude-usage-identity/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(account) + "\x00" + email + "\x00" + organization))
	return hex.EncodeToString(mac.Sum(nil))
}

func claudeUsageRef(account, credential domain.ID) credentials.Ref {
	return credentials.Ref{Owner: account, ID: credential, Purpose: credentials.ClaudeUsage}
}

// claudeUsageAccountTx loads a server-token Claude account for a usage
// operation. A device-owned Claude, ChatGPT or Grok account never matches.
func claudeUsageAccountTx(tx *store.Tx, id domain.ID, revision uint64) (store.Record, domain.Account, error) {
	r, a, err := accountFromTx(tx, id, revision)
	if err == nil && !a.ManagedClaudeAccount() {
		err = domain.Fail(domain.Unsupported, "Claude usage tracking requires a server-token Claude account.", "Connect a new Claude account with server sign-in first.")
	}
	return r, a, err
}

// claudeUsageStartable reports whether a usage login may begin: a connected,
// settled server-token account with no queued, leased-for-logout or fenced
// execution lifecycle operation.
func claudeUsageStartable(a domain.Account) bool {
	st := a.Subscription
	if st == nil || a.Connection == nil || st.Generation == "" || a.Removal != nil || st.Pending != nil || st.RecoveryRequired {
		return false
	}
	return st.ServerOperation == nil || !st.ServerOperation.Active()
}

// claudeUsageCleanupCandidate selects an account whose usage section owes
// cleanup that no live login owns. A login whose run is registered is owned by
// it and is never selected. A connecting login without a registered owner has
// lost its run: either its server epoch ended (a restart) or its goroutine
// finished without settling the section (for example a failed settlement
// transaction after the child was joined), so it is cleaned up and settled
// instead of waiting for a restart or an explicit cancel. An identity review
// that elapsed, or whose actor lost authority and can therefore never confirm
// it, is discarded.
func (s *Service) claudeUsageCleanupCandidate(account domain.ID, a domain.Account, now time.Time, actorGone bool) bool {
	if !a.ManagedClaudeAccount() || a.Subscription.ClaudeUsage == nil {
		return false
	}
	usage := a.Subscription.ClaudeUsage
	switch usage.Connection {
	case domain.ClaudeUsageConnecting:
		return usage.Operation != nil && (usage.Operation.Epoch != s.subscriptionServerEpoch() || usage.Operation.State == domain.SubscriptionRecovery || !s.claudeUsage.live(account))
	case domain.ClaudeUsageConfirming:
		return usage.Operation != nil && (!now.Before(usage.Operation.ExpiresAt) || actorGone) || usage.OwesCleanup()
	case domain.ClaudeUsageLinked:
		// A claimed read or renewal whose registered owner is gone (its settlement
		// failed) would reject every later admission until a restart.
		return usage.OwesCleanup() || claudeUsageReadInFlight(usage) && !s.claudeUsage.live(account)
	}
	return usage.OwesCleanup()
}

// claudeUsageReadInFlight reports whether the durable section holds a claimed
// provider read or supervised renewal.
func claudeUsageReadInFlight(usage *domain.ClaudeServerUsageState) bool {
	return usage.Connection == domain.ClaudeUsageLinked && (usage.Refresh == domain.ClaudeUsageReading || usage.Refresh == domain.ClaudeUsageRenewing)
}

// fenceClaudeUsageLogin settles every usage state whose owner died with an older
// server epoch. It runs inside the startup transaction, before any owner starts.
// A login is marked as needing recovery and is never relaunched, and its
// credentials are never trusted. An interrupted read returns to idle, while an
// interrupted renewal may have rotated the credential and therefore requires an
// explicit reconnect: it is never repeated.
func (s *Service) fenceClaudeUsageLogin(a *domain.Account) bool {
	if !a.ManagedClaudeAccount() || a.Subscription.ClaudeUsage == nil {
		return false
	}
	usage := a.Subscription.ClaudeUsage
	fenced := false
	if usage.Connection == domain.ClaudeUsageConnecting && usage.Operation != nil && usage.Operation.Epoch != s.subscriptionServerEpoch() {
		fenced = usage.MarkRecovery()
	}
	return usage.FenceInterruptedRead() || fenced
}

// prepareClaudeUsageLogout cancels and joins a live usage login before an
// execution logout is accepted. It validates the logout request first so a
// request that would be refused never cancels live work; the logout's own
// transaction repeats every check and retires the usage connection.
func (s *Service) prepareClaudeUsageLogout(ctx context.Context, account domain.ID, revision uint64) error {
	if !s.claudeUsage.live(account) {
		return nil
	}
	valid := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := managedClaudeAccount(tx, account, revision)
		valid = err == nil && claudeUsageStartable(a)
		return nil
	})
	if err != nil || !valid {
		return err
	}
	s.logger.InfoContext(ctx, "claude_usage_login_joined_for_logout", "account_id", account)
	return s.claudeUsage.cancelAndJoin(ctx, account, "", errClaudeUsageRevoked)
}

// refuseLiveClaudeUsageLogout rechecks, under the account gate, that no usage
// run is registered when an execution logout is about to be accepted. The join
// in prepareClaudeUsageLogout runs outside the gate, so a usage login may have
// been admitted since; accepting the logout then would retire the usage section
// while that login's native child still runs. The refusal is retryable and the
// retry joins the new run before the logout is accepted.
func (s *Service) refuseLiveClaudeUsageLogout(account domain.ID) error {
	if !s.claudeUsage.live(account) {
		return nil
	}
	s.logger.Warn("claude_usage_logout_refused_live_run", "account_id", account)
	return claudeUsageConflict("A Claude usage login started before the logout was accepted.")
}

// cleanupClaudeUsage runs one idempotent cleanup pass for account.
func (s *Service) cleanupClaudeUsage(ctx context.Context, account domain.ID) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.cleanupClaudeUsageLocked(ctx, account)
}

func (s *Service) removeUsageStorage(ctx context.Context, account domain.ID) error {
	remove := s.claudeUsageRemove
	if remove == nil {
		remove = removeClaudeUsageStorage
	}
	return remove(ctx, s.scopeFor(account), s.logger)
}

// cleanupClaudeUsageLocked deletes every owed vault reference and, when owed,
// the native credential storage of the usage profile, then records exactly what
// was confirmed. A login of an older epoch is settled as failed only after its
// native storage removal is proven; an elapsed identity review is discarded.
// The caller holds the account gate. A live login is never touched.
func (s *Service) cleanupClaudeUsageLocked(ctx context.Context, account domain.ID) error {
	return s.cleanupClaudeUsageExcept(ctx, account, "")
}

// cleanupClaudeUsageExcept is cleanupClaudeUsageLocked for the finishing login
// of operation itself, whose registration is still present.
func (s *Service) cleanupClaudeUsageExcept(ctx context.Context, account, self domain.ID) error {
	if s.claudeUsage.liveExcept(account, self) {
		return nil
	}
	var usage domain.ClaudeServerUsageState
	found, actorGone := false, false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return err
		}
		// An account of another kind owns no usage section.
		if a.ManagedClaudeAccount() && a.Subscription.ClaudeUsage != nil {
			usage, found = *a.Subscription.ClaudeUsage, true
			usage.RetiredCredentials = slices.Clone(usage.RetiredCredentials)
			actorGone = usage.Connection == domain.ClaudeUsageConfirming && usage.Operation != nil && subscriptionActorValid(tx, usage.Operation.Actor) != nil
		}
		return nil
	})
	if err != nil || !found {
		return err
	}
	orphan := usage.Connection == domain.ClaudeUsageConnecting
	expired := usage.Connection == domain.ClaudeUsageConfirming && usage.Operation != nil && (!time.Now().Before(usage.Operation.ExpiresAt) || actorGone)
	interrupted := claudeUsageReadInFlight(&usage)
	if !usage.OwesCleanup() && !orphan && !expired && !interrupted {
		return nil
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	var first error
	var removed []domain.ID
	for _, id := range usage.RetiredCredentials {
		if err := vault.Delete(ctx, claudeUsageRef(account, id)); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		removed = append(removed, id)
	}
	profile := false
	if usage.ProfileCleanup || orphan || expired {
		if err := s.removeUsageStorage(ctx, account); err != nil {
			if first == nil {
				first = err
			}
		} else {
			profile = true
		}
	}
	generation := usage.Generation
	discarded := false
	operation := domain.ID("")
	if usage.Operation != nil {
		operation = usage.Operation.ID
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.cleanup", struct{ Account, Generation, Operation domain.ID }{account, generation, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		current := a.Subscription.ClaudeUsage
		if current == nil || current.Generation != generation || s.claudeUsage.liveExcept(account, self) {
			return struct{}{}, nil
		}
		current.ConfirmCleanup(removed, profile)
		if claudeUsageReadInFlight(current) {
			// No registered owner remains: an interrupted read returns to idle and
			// an interrupted renewal requires an explicit reconnect, never a repeat.
			current.FenceInterruptedRead()
		}
		switch {
		case current.Connection == domain.ClaudeUsageConnecting && profile:
			diagnostic := claudeDiagnostic(operation, domain.NativeSubscriptionCleanup, "", domain.Fail(domain.RecoveryRequired, "Interrupted.", "Connect usage again."))
			if err := current.Settle(domain.SubscriptionFailed, diagnostic); err != nil {
				return nil, err
			}
		case current.Connection == domain.ClaudeUsageConfirming && expired:
			if err := current.DiscardConfirmation(domain.SubscriptionExpired); err != nil {
				return nil, err
			}
			discarded = true
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		return err
	}
	if discarded && first == nil {
		// The discarded review's credential was just retired: delete it now.
		return s.cleanupClaudeUsageExcept(ctx, account, self)
	}
	s.logger.InfoContext(ctx, "claude_usage_cleanup", "account_id", account, "operation_id", operation, "references_removed", len(removed), "profile_cleaned", profile, "complete", first == nil)
	return first
}

// removeClaudeUsageReferences deletes every remaining claude-usage reference of
// owner. Account deletion owns the final removal; no other purpose is touched.
func removeClaudeUsageReferences(ctx context.Context, vault accountSecrets, owner domain.ID) error {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Purpose != credentials.ClaudeUsage {
			continue
		}
		if ref.Owner != owner || ref.ID.Validate() != nil {
			return subscriptionDenied()
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if err := vault.Delete(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// removeAccountClaudeUsage is the usage part of explicit account deletion: every
// remaining claude-usage reference and the native credential storage of the
// usage profile are removed before the account (and with it the section that
// recorded them) disappears. It does not rewrite the account, whose revision the
// deletion commits against. The caller holds the account gate; a usage login
// cannot be live because deletion requires a disconnected account.
func (s *Service) removeAccountClaudeUsage(ctx context.Context, account domain.ID) error {
	if s.claudeUsage.live(account) {
		return claudeUsageUnavailable()
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	if err := removeClaudeUsageReferences(ctx, vault, account); err != nil {
		return err
	}
	return s.removeUsageStorage(ctx, account)
}

// removeRetiredClaudeUsage deletes the claude-usage references that the usage
// section of a server-token Claude account retired, so an explicit execution
// logout is complete only once they are gone. A live (current) credential is
// never deleted here: only the section's retirement list is. The section is not
// rewritten; the cleanup pass later releases the confirmed entries, and deleting
// an already deleted reference is idempotent. An account without a usage section
// owes nothing, and an account of another kind is ignored. The caller holds the
// account gate.
func (s *Service) removeRetiredClaudeUsage(ctx context.Context, account domain.ID) error {
	var retired []domain.ID
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, account, 0)
		if err != nil {
			return err
		}
		if a.ManagedClaudeAccount() && a.Subscription.ClaudeUsage != nil {
			retired = slices.Clone(a.Subscription.ClaudeUsage.RetiredCredentials)
		}
		return nil
	})
	if err != nil || len(retired) == 0 {
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	for _, id := range retired {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if err := vault.Delete(ctx, claudeUsageRef(account, id)); err != nil {
			return err
		}
	}
	return nil
}
