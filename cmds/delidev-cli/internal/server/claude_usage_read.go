// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The server usage-read owner of a linked Claude usage connection.
//
// Exactly one read per account runs in a registered run (the same registry that
// owns the login), so a row or Manage refresh, Refresh all and inactive
// maintenance all share one owner: overlapping requests coalesce into the
// running read, a disconnect, logout or shutdown cancels and joins it, and
// cleanup never touches a live read. A read is claimed durably before the
// provider request (phase `reading`, the latest attempt time) and published only
// after the actor, account connection, usage generation, credential and server
// epoch were rechecked inside the publishing transaction.
//
// Scheduled reads (maintenance and Refresh all, which cannot be told apart from
// the 30-second foreground trigger) are due only when the latest attempt is at
// least four minutes old. An explicit refresh may read sooner but never while a
// read is in flight or a provider-requested suppression is active.

// claudeUsageTrigger is the closed set of read origins.
type claudeUsageTrigger string

const (
	// claudeUsageTriggerMaintenance is the inactive-app scheduler.
	claudeUsageTriggerMaintenance claudeUsageTrigger = "maintenance"
	// claudeUsageTriggerRefreshAll is the Refresh all request, which also carries
	// the automatic 30-second foreground cadence.
	claudeUsageTriggerRefreshAll claudeUsageTrigger = "refresh-all"
	// claudeUsageTriggerExplicit is a row or Manage refresh of one account.
	claudeUsageTriggerExplicit claudeUsageTrigger = "explicit"
)

// claudeUsageResult is the closed result code of one read, used for the
// structured result log only.
type claudeUsageResult string

const (
	claudeUsageResultObserved    claudeUsageResult = "observed"
	claudeUsageResultFailed      claudeUsageResult = "failed"
	claudeUsageResultReconnect   claudeUsageResult = "reconnect-required"
	claudeUsageResultUncertain   claudeUsageResult = "rotation-uncertain"
	claudeUsageResultDiscarded   claudeUsageResult = "discarded"
	claudeUsageResultInterrupted claudeUsageResult = "interrupted"
)

const (
	claudeUsageReadBudget           = 3 * time.Minute
	claudeUsageScanInterval         = 15 * time.Second
	claudeUsageScanAdmissionPerPass = 8
)

var (
	errClaudeUsageNotDue = domain.Fail(domain.Conflict, "No Claude usage read is due.", "")
	errClaudeUsageBusy   = domain.Fail(domain.Conflict, "A Claude usage read is already running.", "Wait for the running read to finish.")
)

func claudeUsagePaused() *domain.Error {
	return domain.Fail(domain.Unavailable, "Claude usage reads are paused until the provider-requested time.", "Wait for the pause to end; a manual refresh cannot bypass it.")
}

// claudeUsageFence is the optional execution-account fence of an explicit
// refresh: the confirmed connection and execution generation.
type claudeUsageFence struct{ connection, generation domain.ID }

// claudeUsageRead is the immutable binding of one claimed read.
type claudeUsageRead struct {
	run        *claudeUsageRun
	ctx        context.Context
	account    domain.ID
	operation  domain.ID
	trigger    claudeUsageTrigger
	actor      domain.Principal
	epoch      domain.ID
	generation domain.ID
	connection domain.ID
	commitment string
	// credential is the current vault generation. A published renewal replaces it.
	credential domain.ID
}

type claudeUsageReadKind int

const (
	readObserved claudeUsageReadKind = iota
	readFailed
	readReconnect
	// readDiscarded: the read ended without an answer (a revocation, a changed
	// binding or an unclaimed renewal). A still-bound claim only returns to idle.
	readDiscarded
	// readInterrupted: a server shutdown; the durable claim is left for the
	// restart fence, which never repeats an uncertain renewal.
	readInterrupted
)

type claudeUsageReadOutcome struct {
	kind      claudeUsageReadKind
	quota     domain.ClaudeUsageQuota
	failure   domain.ClaudeUsageFailure
	backoff   *time.Time
	uncertain bool
	renewed   bool
}

func (o claudeUsageReadOutcome) result() claudeUsageResult {
	switch o.kind {
	case readObserved:
		return claudeUsageResultObserved
	case readFailed:
		return claudeUsageResultFailed
	case readReconnect:
		if o.uncertain {
			return claudeUsageResultUncertain
		}
		return claudeUsageResultReconnect
	case readInterrupted:
		return claudeUsageResultInterrupted
	}
	return claudeUsageResultDiscarded
}

func failedRead(failure domain.ClaudeUsageFailure, backoff *time.Time) claudeUsageReadOutcome {
	return claudeUsageReadOutcome{kind: readFailed, failure: failure, backoff: backoff}
}

func reconnectRead(failure domain.ClaudeUsageFailure, uncertain bool) claudeUsageReadOutcome {
	return claudeUsageReadOutcome{kind: readReconnect, failure: failure, uncertain: uncertain}
}

// operationOf reports the operation of the live run of account, or "".
func (r *claudeUsageRuns) operationOf(account domain.ID) domain.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.active[account]; run != nil {
		return run.operation
	}
	return ""
}

// admitClaudeUsageRead claims one provider read and launches its owner. It
// returns errClaudeUsageBusy when a login or read of the account is already
// live (the caller coalesces) and errClaudeUsageNotDue when a scheduled read is
// not due. Only an explicit read may run before its due time. The account gate
// must not be held by the caller.
func (s *Service) admitClaudeUsageRead(ctx context.Context, account, operation domain.ID, actor domain.Principal, trigger claudeUsageTrigger, fence claudeUsageFence, receipt any) (*claudeUsageRead, error) {
	if !s.claudeUsageActive() || s.stopping.Load() {
		return nil, errClaudeUsageNotDue
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	run, runCtx, err := s.claudeUsage.reserve(account, operation, time.Now().Add(claudeUsageReadBudget))
	if err != nil {
		if domain.SafeError(err).Code == domain.Conflict {
			return nil, errClaudeUsageBusy
		}
		return nil, err
	}
	read := &claudeUsageRead{run: run, ctx: runCtx, account: account, operation: operation, trigger: trigger, actor: actor, epoch: s.subscriptionServerEpoch()}
	_, err = s.Store.Mutate(ctx, operation, "claude.usage.read.claim", receipt, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if a.Connection == nil || a.Removal != nil || usage == nil || usage.Connection != domain.ClaudeUsageLinked {
			return nil, claudeUsageConflict("The Claude usage connection is not linked.")
		}
		if fence.connection != "" && (a.Connection.ID != fence.connection || a.Subscription.Generation != fence.generation) {
			return nil, domain.Fail(domain.Conflict, "The confirmed account generation changed.", "Read the current account and confirm the original operation again before sending.")
		}
		if subscriptionActorValid(tx, actor) != nil {
			return nil, subscriptionDenied()
		}
		now := s.claudeUsageNow()
		switch {
		case usage.BackoffUntil != nil && now.Before(*usage.BackoffUntil):
			if trigger == claudeUsageTriggerExplicit {
				return nil, claudeUsagePaused()
			}
			return nil, errClaudeUsageNotDue
		case trigger != claudeUsageTriggerExplicit && !usage.ReadDue(now):
			return nil, errClaudeUsageNotDue
		case !usage.ReadAllowed(now):
			// A phase other than idle: a read or renewal is in flight, or an
			// ambiguous rotation is retained.
			return nil, errClaudeUsageBusy
		}
		if err := usage.BeginRead(now); err != nil {
			return nil, err
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		read.generation, read.connection, read.credential, read.commitment = usage.Generation, a.Connection.ID, usage.CredentialID, usage.Identity.Commitment
		return struct{}{}, nil
	})
	if err != nil {
		s.claudeUsage.release(run)
		return nil, err
	}
	go s.runClaudeUsageRead(read)
	s.logger.InfoContext(ctx, "claude_usage_read_claimed", "account_id", account, "operation_id", operation, "trigger", trigger)
	return read, nil
}

// runClaudeUsageRead owns one claimed read from provider request to publication.
func (s *Service) runClaudeUsageRead(read *claudeUsageRead) {
	defer s.claudeUsage.finish(read.run)
	logger := s.logger.With("account_id", read.account, "operation_id", read.operation, "trigger", read.trigger)
	outcome := s.performClaudeUsageRead(read, logger)
	// Anything after the provider answer or the child join is settled with an
	// owner context that a revocation of the run cannot cancel.
	read.run.cancel(errClaudeUsageCanceled)
	if outcome.kind == readInterrupted {
		logger.Info("claude_usage_read_finished", "result", outcome.result())
		return
	}
	// Every other outcome settles through the rechecking transaction, which
	// publishes nothing when the binding was revoked or replaced.
	if err := s.finishClaudeUsageRead(read, outcome); err != nil {
		logger.Warn("claude_usage_read_settle_failed", "code", domain.SafeError(err).Code)
		return
	}
	attrs := []any{"result", outcome.result(), "renewed", outcome.renewed}
	if outcome.failure != domain.ClaudeUsageFailureNone {
		attrs = append(attrs, "failure", outcome.failure)
	}
	if outcome.backoff != nil {
		attrs = append(attrs, "backoff_seconds", int64(outcome.backoff.Sub(s.claudeUsageNow()).Round(time.Second)/time.Second))
	}
	logger.Info("claude_usage_read_finished", attrs...)
}

// endedOutcome classifies why the run context ended.
func endedOutcome(ctx context.Context) claudeUsageReadOutcome {
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errClaudeUsageShutdown):
		return claudeUsageReadOutcome{kind: readInterrupted}
	case errors.Is(cause, context.DeadlineExceeded):
		return failedRead(domain.ClaudeUsageFailureUnavailable, nil)
	}
	return claudeUsageReadOutcome{kind: readDiscarded}
}

func (s *Service) performClaudeUsageRead(read *claudeUsageRead, logger *slog.Logger) claudeUsageReadOutcome {
	ctx := read.ctx
	bundle, err := s.openClaudeUsageBundle(ctx, read)
	if err != nil {
		code := domain.SafeError(err).Code
		logger.Warn("claude_usage_credential_unavailable", "code", code)
		if ctx.Err() != nil {
			return endedOutcome(ctx)
		}
		if code == domain.NotFound || code == domain.RecoveryRequired {
			// The bound credential is gone or foreign: reads can never resume.
			return reconnectRead(domain.ClaudeUsageFailureUnauthorized, false)
		}
		return s.backedOff(failedRead(domain.ClaudeUsageFailureUnavailable, nil), "")
	}
	defer func() { bundle.Clear() }()
	renewed := false
	if len(bundle.RefreshToken) != 0 && !bundle.ExpiresAt.After(s.claudeUsageNow().Add(domain.ClaudeUsageRenewalMargin)) {
		next, outcome := s.renewClaudeUsage(ctx, read, bundle, logger)
		if outcome != nil {
			return *outcome
		}
		bundle.Clear()
		bundle, renewed = next, true
	}
	for {
		result := s.fetchClaudeUsage(ctx, bundle.AccessToken)
		if result.Kind == claudeFetchInterrupted || ctx.Err() != nil {
			return endedOutcome(ctx)
		}
		logger.Info("claude_usage_fetch_result", "kind", result.Kind)
		var outcome claudeUsageReadOutcome
		switch result.Kind {
		case claudeFetchOK:
			outcome = claudeUsageReadOutcome{kind: readObserved, quota: result.Quota}
		case claudeFetchUnauthorized:
			if renewed || len(bundle.RefreshToken) == 0 {
				// At most one renewal per read, and none without a refresh token.
				outcome = reconnectRead(domain.ClaudeUsageFailureUnauthorized, false)
				break
			}
			next, renewal := s.renewClaudeUsage(ctx, read, bundle, logger)
			if renewal != nil {
				return *renewal
			}
			bundle.Clear()
			bundle, renewed = next, true
			continue
		case claudeFetchForbidden, claudeFetchRateLimited:
			outcome = s.backedOff(failedRead(result.Kind.failure(), nil), result.RetryAfter)
		case claudeFetchUnavailable:
			outcome = failedRead(domain.ClaudeUsageFailureUnavailable, nil)
			if result.RetryAfter != "" {
				outcome = s.backedOff(outcome, result.RetryAfter)
			}
		default:
			outcome = failedRead(result.Kind.failure(), nil)
		}
		outcome.renewed = renewed
		return outcome
	}
}

// backedOff attaches the provider-requested suppression to a failed read.
func (s *Service) backedOff(outcome claudeUsageReadOutcome, retryAfter string) claudeUsageReadOutcome {
	now := s.claudeUsageNow()
	until := now.Add(domain.ClaudeUsageBackoffDelay(retryAfter, now))
	outcome.backoff = &until
	return outcome
}

// openClaudeUsageBundle opens the current bound credential for one read.
func (s *Service) openClaudeUsageBundle(ctx context.Context, read *claudeUsageRead) (subscription.ClaudeUsageBundle, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	defer unlock()
	if err := s.claudeUsageBound(ctx, read, domain.ClaudeUsageReading); err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	vault, err := s.secrets()
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	raw, err := vault.Get(ctx, claudeUsageRef(read.account, read.credential))
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	defer clear(raw)
	return subscription.OpenClaudeUsageBundle(raw, s.Identity.ServerID, read.account, read.credential, read.commitment)
}

// claudeUsageBound proves, without mutating anything, that the stored account
// still carries the exact claim of read in the given phase.
func (s *Service) claudeUsageBound(ctx context.Context, read *claudeUsageRead, phase domain.ClaudeUsageRefreshPhase) error {
	return s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return err
		}
		return s.claudeUsageBoundTx(tx, a, read, phase)
	})
}

// claudeUsageBoundTx is the publication recheck: the account connection, usage
// generation, credential, claimed phase, actor and server epoch are all still
// the ones the read was claimed under.
func (s *Service) claudeUsageBoundTx(tx *store.Tx, a domain.Account, read *claudeUsageRead, phase domain.ClaudeUsageRefreshPhase) error {
	usage := a.Subscription.ClaudeUsage
	if a.Connection == nil || a.Connection.ID != read.connection || a.Removal != nil || usage == nil || !usage.Reading() || usage.Generation != read.generation || usage.CredentialID != read.credential || usage.Refresh != phase || s.subscriptionServerEpoch() != read.epoch || subscriptionActorValid(tx, read.actor) != nil {
		return subscriptionDenied()
	}
	return nil
}

// finishClaudeUsageRead publishes the outcome of one read under the account gate
// after rechecking the full binding. A changed binding publishes nothing.
func (s *Service) finishClaudeUsageRead(read *claudeUsageRead, outcome claudeUsageReadOutcome) error {
	ctx, cancel := claudeUsageOwnerContext(context.Background())
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.read.finish", struct{ Account, Operation domain.ID }{read.account, read.operation}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil {
			return struct{}{}, nil
		}
		phase := domain.ClaudeUsageReading
		if usage.Refresh == domain.ClaudeUsageRenewing {
			phase = domain.ClaudeUsageRenewing
		}
		if s.claudeUsageBoundTx(tx, a, read, phase) != nil {
			// Revoked, replaced or retired while the read ran: publish nothing.
			s.logger.Info("claude_usage_read_discarded", "account_id", read.account, "operation_id", read.operation)
			return struct{}{}, nil
		}
		switch outcome.kind {
		case readObserved:
			if usage.Refresh != domain.ClaudeUsageReading {
				return nil, subscriptionDenied()
			}
			if _, err := domain.ApplyClaudeUsageObservation(&a, read.generation, outcome.quota, s.claudeUsageNow()); err != nil {
				return nil, err
			}
		case readFailed:
			if err := usage.SettleRead(outcome.failure, outcome.backoff); err != nil {
				return nil, err
			}
		case readDiscarded:
			if err := usage.SettleRead(domain.ClaudeUsageFailureUnavailable, nil); err != nil {
				return nil, err
			}
		default:
			if err := usage.RequireReconnect(outcome.failure, outcome.uncertain); err != nil {
				return nil, err
			}
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// renewClaudeUsage runs the one supervised native refresh-login of a read and
// returns the rotated bundle, which the caller uses for the single GET retry. A
// non-nil outcome ends the read. The refresh token is never offered twice: once
// the child may have run, every failure keeps recovery state and requires an
// explicit reconnect.
func (s *Service) renewClaudeUsage(ctx context.Context, read *claudeUsageRead, bundle subscription.ClaudeUsageBundle, logger *slog.Logger) (subscription.ClaudeUsageBundle, *claudeUsageReadOutcome) {
	end := func(outcome claudeUsageReadOutcome) (subscription.ClaudeUsageBundle, *claudeUsageReadOutcome) {
		outcome.renewed = true
		return subscription.ClaudeUsageBundle{}, &outcome
	}
	if err := s.claimClaudeUsageRenewal(ctx, read); err != nil {
		logger.Warn("claude_usage_renewal_not_claimed", "code", domain.SafeError(err).Code)
		if ctx.Err() != nil {
			return end(endedOutcome(ctx))
		}
		return end(claudeUsageReadOutcome{kind: readDiscarded})
	}
	logger.Info("claude_usage_renewal_started")
	opener := s.claudeUsageRenew
	if opener == nil {
		opener = openClaudeUsageRenewal
	}
	renewal, version, err := opener(ctx, s.scopeFor(read.account), read.operation, bundle, s.logger)
	if err != nil {
		code := domain.SafeError(err).Code
		logger.Warn("claude_usage_renewal_open_failed", "code", code, "version_observed", version != "")
		switch {
		case ctx.Err() != nil:
			return end(endedOutcome(ctx))
		case code == domain.Unsupported:
			return end(reconnectRead(domain.ClaudeUsageFailureUnsupported, false))
		case code == domain.RecoveryRequired:
			if reason, ok := nativeprofile.Reasoned(err); ok && reason == nativeprofile.ReasonCredentialPresent {
				// Leftover storage was refused before any child ran: nothing was
				// exchanged and the owned cleanup stays recorded.
				return end(s.backedOff(failedRead(domain.ClaudeUsageFailureUnavailable, nil), ""))
			}
			return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
		}
		return end(s.backedOff(failedRead(domain.ClaudeUsageFailureUnavailable, nil), ""))
	}
	defer func() {
		if err := renewal.Release(); err != nil {
			logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(err).Code)
		}
	}()
	status, runErr := renewal.Run(ctx)
	logger.Info("claude_usage_renewal_child_finished", "status", status)
	switch status {
	case claude.UsageRefreshNotStarted:
		if ctx.Err() != nil {
			return end(endedOutcome(ctx))
		}
		if domain.SafeError(runErr).Code == domain.Unsupported {
			return end(reconnectRead(domain.ClaudeUsageFailureUnsupported, false))
		}
		return end(s.backedOff(failedRead(domain.ClaudeUsageFailureUnavailable, nil), ""))
	case claude.UsageRefreshRejected:
		return end(reconnectRead(domain.ClaudeUsageFailureUnauthorized, false))
	case claude.UsageRefreshCompleted:
	default:
		if errors.Is(context.Cause(ctx), errClaudeUsageShutdown) {
			return end(claudeUsageReadOutcome{kind: readInterrupted})
		}
		// The child ran and its result is ambiguous: a canceled, timed out or
		// unjoined renewal may have consumed the refresh token. A revoked binding
		// publishes nothing in the settling transaction.
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	return s.publishClaudeUsageRenewal(ctx, read, renewal, logger, end)
}

// publishClaudeUsageRenewal captures the rotated credential of a joined child,
// proves the identity is unchanged and publishes the credential through a
// durable intent. It uses its own bounded owner context: the rotated refresh
// token may be the only copy, so a revocation of the run must not drop it
// before the durable steps settle.
func (s *Service) publishClaudeUsageRenewal(ctx context.Context, read *claudeUsageRead, renewal claudeUsageRenewal, logger *slog.Logger, end func(claudeUsageReadOutcome) (subscription.ClaudeUsageBundle, *claudeUsageReadOutcome)) (subscription.ClaudeUsageBundle, *claudeUsageReadOutcome) {
	bounded, stop := claudeUsageOwnerContext(ctx)
	defer stop()
	capture, err := renewal.Capture(bounded)
	if err != nil {
		logger.Warn("claude_usage_renewal_capture_failed", "code", domain.SafeError(err).Code)
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	keep := false
	defer func() {
		if !keep {
			capture.clear()
		}
	}()
	commitment := s.claudeUsageIdentityCommitment(read.account, capture.Identity.Email, capture.Identity.OrganizationID)
	if subtle.ConstantTimeCompare([]byte(commitment), []byte(read.commitment)) != 1 {
		// A foreign identity never replaces the reviewed one. The old refresh
		// token was consumed, so reads cannot continue without a reconnect.
		logger.Warn("claude_usage_renewal_identity_changed")
		return end(reconnectRead(domain.ClaudeUsageFailureIdentityChanged, false))
	}
	next := domain.NewID()
	sealed, err := subscription.SealClaudeUsageBundle(s.Identity.ServerID, read.account, next, read.commitment, capture.Bundle)
	if err != nil {
		logger.Warn("claude_usage_renewal_seal_failed", "code", domain.SafeError(err).Code)
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	defer clear(sealed)
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	defer unlock()
	// 1. Durable cleanup intent for the reference about to be written.
	if _, err := s.Store.Mutate(bounded, domain.NewID(), "claude.usage.renewal.intent", struct{ Account, Operation, Credential domain.ID }{read.account, read.operation, next}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return nil, err
		}
		if err := s.claudeUsageBoundTx(tx, a, read, domain.ClaudeUsageRenewing); err != nil {
			return nil, err
		}
		if err := a.Subscription.ClaudeUsage.RecordRenewalIntent(next); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	}); err != nil {
		logger.Warn("claude_usage_renewal_intent_failed", "code", domain.SafeError(err).Code)
		// The child already rotated the credential. A revoked binding publishes
		// nothing; a transient failure keeps the recovery state.
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	// 2. Stage the sealed bundle. An uncertain write stays covered by the intent.
	vault, err := s.secrets()
	if err != nil {
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	if _, err := vault.Put(bounded, claudeUsageRef(read.account, next), sealed); err != nil {
		logger.Warn("claude_usage_renewal_vault_failed", "code", domain.SafeError(err).Code)
		return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
	}
	// 3. The native copy is no longer needed once the vault holds the bundle.
	releaseErr := renewal.Release()
	profileCleaned := releaseErr == nil && s.removeUsageStorage(bounded, read.account) == nil
	// 4. Publish the rotated credential and keep the superseded one owed.
	previous := read.credential
	_, err = s.Store.Mutate(bounded, domain.NewID(), "claude.usage.renewal.publish", struct{ Account, Operation, Credential domain.ID }{read.account, read.operation, next}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return nil, err
		}
		if err := s.claudeUsageBoundTx(tx, a, read, domain.ClaudeUsageRenewing); err != nil {
			return nil, err
		}
		if err := a.Subscription.ClaudeUsage.PublishRenewal(next, profileCleaned); err != nil {
			return nil, err
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		// An unacknowledged commit may have published. Read before deciding.
		if !s.claudeUsagePublished(read, next) {
			logger.Warn("claude_usage_renewal_publish_failed", "code", domain.SafeError(err).Code)
			return end(reconnectRead(domain.ClaudeUsageFailureNone, true))
		}
		logger.Warn("claude_usage_renewal_publish_confirmed", "code", domain.SafeError(err).Code)
	}
	read.credential = next
	keep = true
	logger.Info("claude_usage_renewal_published", "profile_cleaned", profileCleaned, "superseded", previous != "")
	return capture.Bundle, nil
}

// claudeUsagePublished reports whether the stored account holds credential as
// the current credential of read.
func (s *Service) claudeUsagePublished(read *claudeUsageRead, credential domain.ID) bool {
	ctx, cancel := claudeUsageOwnerContext(context.Background())
	defer cancel()
	published := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		published = usage != nil && usage.Connection == domain.ClaudeUsageLinked && usage.Generation == read.generation && usage.CredentialID == credential
		return nil
	})
	return err == nil && published
}

// claimClaudeUsageRenewal durably claims the single renewal of the read and owes
// the native credential storage the child will create.
func (s *Service) claimClaudeUsageRenewal(ctx context.Context, read *claudeUsageRead) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.renewal.claim", struct{ Account, Operation domain.ID }{read.account, read.operation}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, read.account, 0)
		if err != nil {
			return nil, err
		}
		if err := s.claudeUsageBoundTx(tx, a, read, domain.ClaudeUsageReading); err != nil {
			return nil, err
		}
		if err := a.Subscription.ClaudeUsage.BeginRenewal(); err != nil {
			return nil, err
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}
