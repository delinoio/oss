// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// claudeUsageSettleBudget bounds every post-join durable step. These steps use
// their own context because the run context may already be canceled by the very
// cancellation whose cleanup they complete.
const claudeUsageSettleBudget = 30 * time.Second

func claudeUsageOwnerContext(parent context.Context) (context.Context, context.CancelFunc) {
	_ = parent
	return context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), claudeUsageSettleBudget)
}

// launchClaudeUsage starts the single goroutine that owns one reserved login.
func (s *Service) launchClaudeUsage(run *claudeUsageRun, ctx context.Context, op domain.ClaudeUsageOperation, generation domain.ID) {
	go func() {
		defer s.claudeUsage.finish(run)
		s.runClaudeUsageLogin(ctx, run, op, generation)
	}()
}

// claudeUsageOutcome is the closed result of one run, used only for settlement
// and the structured result log.
type claudeUsageOutcome struct {
	state      domain.SubscriptionLoginState
	diagnostic *domain.NativeSubscriptionDiagnostic
	// untouched leaves the durable state exactly as it is (server shutdown).
	untouched bool
	// recovery fences the login because its native child could not be proven
	// stopped.
	recovery bool
}

func (s *Service) runClaudeUsageLogin(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID) {
	account := run.account
	logger := s.logger.With("account_id", account, "operation_id", op.ID)
	logger.InfoContext(ctx, "claude_usage_login_started")
	// The watcher revokes the login as soon as its durable state, actor or
	// account authority disappears.
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		s.watchClaudeUsage(ctx, run, op, generation)
	}()
	outcome := s.claudeUsageLoginBody(ctx, run, op, generation, logger)
	run.cancel(errClaudeUsageCanceled)
	<-watched
	switch {
	case outcome.untouched:
		logger.InfoContext(ctx, "claude_usage_login_interrupted", "result", "shutdown")
		return
	case outcome.recovery:
		if err := s.markClaudeUsageRecovery(account, op, generation, outcome.diagnostic); err != nil {
			logger.Warn("claude_usage_recovery_unconfirmed", "code", domain.SafeError(err).Code)
		}
		logger.Warn("claude_usage_login_finished", "result", domain.SubscriptionRecovery, "cleanup_confirmed", false)
		return
	case outcome.state == "":
		// Staged for review by the body.
		return
	}
	if err := s.settleClaudeUsage(account, op, generation, outcome.state, outcome.diagnostic); err != nil {
		logger.Warn("claude_usage_settle_failed", "code", domain.SafeError(err).Code)
	}
	logger.InfoContext(ctx, "claude_usage_login_finished", "result", outcome.state)
}

// watchClaudeUsage revokes the run when the login is no longer the account's
// current Connecting operation or its actor lost authority.
func (s *Service) watchClaudeUsage(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			_, a, err := claudeUsageAccountTx(tx, run.account, 0)
			if err != nil {
				return err
			}
			if !claudeUsageCurrent(a, op.ID, generation) || a.Connection == nil || subscriptionActorValid(tx, op.Actor) != nil {
				return subscriptionDenied()
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			run.cancel(errClaudeUsageRevoked)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// claudeUsageCurrent reports whether a still carries the live Connecting login
// of operation and generation.
func claudeUsageCurrent(a domain.Account, operation, generation domain.ID) bool {
	if !a.ManagedClaudeAccount() {
		return false
	}
	usage := a.Subscription.ClaudeUsage
	return usage != nil && usage.Connection == domain.ClaudeUsageConnecting && usage.Generation == generation && usage.Operation != nil && usage.Operation.ID == operation && usage.Operation.Active() && usage.Operation.State != domain.SubscriptionRecovery
}

func (s *Service) claudeUsageLoginBody(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID, logger *slog.Logger) claudeUsageOutcome {
	account := run.account
	opener := s.claudeUsageOpen
	if opener == nil {
		opener = openClaudeUsageLogin
	}
	native, version, err := opener(ctx, s.scopeFor(account), op.ID, s.logger)
	if err != nil {
		code := domain.SafeError(err).Code
		phase := domain.NativeSubscriptionLaunch
		var discovery managedClaudeDiscoveryError
		if errors.As(err, &discovery) {
			phase = domain.NativeSubscriptionDiscovery
		}
		logger.Warn("claude_usage_login_open_failed", "phase", phase, "code", code)
		if code == domain.RecoveryRequired {
			if reason, ok := nativeprofile.Reasoned(err); ok && reason == nativeprofile.ReasonCredentialPresent {
				// Profile admission refused leftover credential storage; the owned
				// cleanup keeps it owed and the login settles as failed.
				return claudeUsageOutcome{state: domain.SubscriptionFailed, diagnostic: claudeDiagnostic(op.ID, phase, version, err)}
			}
			// An uncertain native start: the child cannot be proven stopped, so the
			// login is fenced for recovery instead of being settled as failed.
			return claudeUsageOutcome{recovery: true, diagnostic: claudeDiagnostic(op.ID, phase, version, err)}
		}
		return s.claudeUsageEndOutcome(ctx, claudeDiagnostic(op.ID, phase, version, err), err)
	}
	defer func() {
		if err := native.Release(); err != nil {
			logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(err).Code)
		}
	}()
	published, delivered := false, false
	var runErr error
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
loop:
	for {
		progress, perr := native.Progress()
		if perr != nil {
			runErr = perr
			break
		}
		if progress.URL != "" && !published {
			if runErr = s.publishClaudeUsageProgress(ctx, run, op, generation, progress); runErr != nil {
				break
			}
			published = true
		}
		if published && !delivered {
			code, claimed, cerr := s.takeClaudeUsageCode(ctx, run, op, generation)
			if cerr != nil {
				runErr = cerr
				break
			}
			if claimed {
				submitErr := native.Submit(code)
				if submitErr != nil {
					runErr = submitErr
					break
				}
				delivered = true
				logger.InfoContext(ctx, "claude_usage_code_delivered")
			}
		}
		select {
		case <-ctx.Done():
			runErr = context.Cause(ctx)
			break loop
		case <-ticker.C:
		case <-native.Done():
			runErr = native.Wait()
			break loop
		}
	}
	// Join the original child before any state can claim cleanup.
	if closeErr := native.Close(); closeErr != nil {
		logger.Warn("claude_usage_child_unjoined", "code", domain.SafeError(closeErr).Code)
		return claudeUsageOutcome{recovery: true, diagnostic: claudeDiagnostic(op.ID, domain.NativeSubscriptionCleanup, version, domain.Fail(domain.RecoveryRequired, "The Claude usage login child could not be confirmed stopped.", "Retain the original operation."))}
	}
	if runErr != nil {
		return s.claudeUsageEndOutcome(ctx, claudeDiagnostic(op.ID, domain.NativeSubscriptionLogin, version, runErr), runErr)
	}
	if ctx.Err() != nil {
		return s.claudeUsageEndOutcome(ctx, nil, context.Cause(ctx))
	}
	return s.stageClaudeUsage(ctx, run, op, generation, native, version, logger)
}

// claudeUsageEndOutcome classifies why an unsuccessful run ended.
func (s *Service) claudeUsageEndOutcome(ctx context.Context, diagnostic *domain.NativeSubscriptionDiagnostic, err error) claudeUsageOutcome {
	cause := err
	if ctx.Err() != nil {
		cause = context.Cause(ctx)
	}
	switch {
	case errors.Is(cause, errClaudeUsageShutdown):
		return claudeUsageOutcome{untouched: true}
	case errors.Is(cause, errClaudeUsageCanceled), errors.Is(cause, errClaudeUsageRevoked), errors.Is(cause, context.Canceled):
		return claudeUsageOutcome{state: domain.SubscriptionCanceled}
	case errors.Is(cause, context.DeadlineExceeded):
		return claudeUsageOutcome{state: domain.SubscriptionExpired}
	case domain.SafeError(err).Code == domain.Unsupported:
		return claudeUsageOutcome{state: domain.SubscriptionUnsupported, diagnostic: diagnostic}
	}
	return claudeUsageOutcome{state: domain.SubscriptionFailed, diagnostic: diagnostic}
}

// publishClaudeUsageProgress records that the original login is waiting for the
// browser and keeps its validated address in memory only.
func (s *Service) publishClaudeUsageProgress(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID, progress claude.AuthLoginProgress) error {
	address := progress.URL
	// Only the original manual approval-code address is presented; a localhost
	// callback can never be completed by a remote server.
	if claude.ValidateLoginURL(address) != nil || progress.Method != domain.SubscriptionBrowserCode {
		s.logger.WarnContext(ctx, "claude_usage_login_url_rejected", "operation_id", op.ID, "code", domain.Unsupported)
		return domain.Fail(domain.Unsupported, "The original Claude login address is unsupported.", "Keep the original native operation; do not substitute another authentication flow.")
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.waiting", struct{ Account, Operation domain.ID }{run.account, op.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, run.account, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageCurrent(a, op.ID, generation) || subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if err := a.Subscription.ClaudeUsage.MarkWaiting(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err == nil {
		s.claudeUsage.setURL(op.ID, claudeUsageURL{URL: address, Until: op.ExpiresAt})
	}
	return err
}

// takeClaudeUsageCode records durable consumption before releasing the bytes. An
// uncertain step never permits a second delivery of the same approval code.
func (s *Service) takeClaudeUsageCode(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID) ([]byte, bool, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	ready := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, run.account, 0)
		if err != nil {
			return err
		}
		if !claudeUsageCurrent(a, op.ID, generation) || subscriptionActorValid(tx, op.Actor) != nil {
			return subscriptionDenied()
		}
		current := a.Subscription.ClaudeUsage.Operation
		ready = current.State == domain.SubscriptionWaiting && current.CodeSubmissionID != "" && !current.CodeConsumed && s.claudeUsage.hasCode(op.ID)
		return nil
	})
	if err != nil || !ready {
		return nil, false, err
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.take-code", struct{ Account, Operation domain.ID }{run.account, op.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, run.account, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageCurrent(a, op.ID, generation) || subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if err := a.Subscription.ClaudeUsage.ConsumeCode(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		return nil, false, err
	}
	return s.claudeUsage.takeCode(op.ID), true, nil
}

// stageClaudeUsage captures the joined login's credential and identity, stages
// the credential in the claude-usage vault purpose behind a durable cleanup
// intent, removes the native storage and moves the section into identity
// review. Nothing is linked or scheduled.
func (s *Service) stageClaudeUsage(ctx context.Context, run *claudeUsageRun, op domain.ClaudeUsageOperation, generation domain.ID, native claudeUsageNative, version string, logger *slog.Logger) claudeUsageOutcome {
	account := run.account
	failed := func(err error) claudeUsageOutcome {
		return claudeUsageOutcome{state: domain.SubscriptionFailed, diagnostic: claudeDiagnostic(op.ID, domain.NativeSubscriptionStatus, version, err)}
	}
	capture, err := native.Capture(ctx)
	if err != nil {
		logger.Warn("claude_usage_capture_failed", "code", domain.SafeError(err).Code)
		return s.claudeUsageEndOutcome(ctx, claudeDiagnostic(op.ID, domain.NativeSubscriptionStatus, version, err), err)
	}
	defer capture.clear()
	identity := domain.ClaudeUsageIdentity{Email: capture.Identity.Email, Organization: capture.Identity.OrganizationName, Plan: capture.Identity.Plan, Commitment: s.claudeUsageIdentityCommitment(account, capture.Identity.Email, capture.Identity.OrganizationID)}
	if identity.Organization == "" {
		identity.Organization = capture.Identity.OrganizationID
	}
	if identity.Validate() != nil {
		logger.Warn("claude_usage_identity_rejected", "code", domain.InvalidArgument)
		return failed(domain.Fail(domain.InvalidArgument, "The Claude usage identity is invalid.", "Connect usage again."))
	}
	credential := domain.NewID()
	// The sealed bundle is built before any durable step and cleared on return.
	sealed, err := subscription.SealClaudeUsageBundle(s.Identity.ServerID, account, credential, identity.Commitment, capture.Bundle)
	if err != nil {
		return failed(err)
	}
	defer clear(sealed)

	bounded, stop := claudeUsageOwnerContext(ctx)
	defer stop()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return failed(err)
	}
	defer unlock()
	if ctx.Err() != nil {
		return s.claudeUsageEndOutcome(ctx, nil, context.Cause(ctx))
	}
	// The durable steps below use the detached owner context so a cancellation
	// cannot cut one in half, but the run context stays authoritative for the
	// transitions: once its cancellation (shutdown, cancel, revocation) is
	// observed, no transition commits, so shutdown leaves the durable state
	// untouched and the recorded intent keeps the staged reference owed.
	runEnded := func() error {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return nil
	}
	// 1. Durable cleanup intent for the reference about to be written.
	if _, err := s.Store.Mutate(bounded, domain.NewID(), "claude.usage.intent", struct{ Account, Operation, Credential domain.ID }{account, op.ID, credential}, func(tx *store.Tx) (any, error) {
		if err := runEnded(); err != nil {
			return nil, err
		}
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageCurrent(a, op.ID, generation) || subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if err := a.Subscription.ClaudeUsage.RecordCredentialIntent(credential); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	}); err != nil {
		logger.Warn("claude_usage_intent_failed", "code", domain.SafeError(err).Code)
		return s.claudeUsageEndOutcome(ctx, nil, err)
	}
	// 2. Stage the sealed bundle. An uncertain write stays covered by the intent.
	vault, err := s.secrets()
	if err != nil {
		return failed(err)
	}
	if _, err := vault.Put(bounded, claudeUsageRef(account, credential), sealed); err != nil {
		logger.Warn("claude_usage_vault_stage_failed", "code", domain.SafeError(err).Code)
		return failed(err)
	}
	// 3. The native copy is no longer needed once the vault holds the bundle.
	releaseErr := native.Release()
	if releaseErr != nil {
		logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(releaseErr).Code)
	}
	profileCleaned := releaseErr == nil && s.removeUsageStorage(bounded, account) == nil
	// 4. Review state.
	_, err = s.Store.Mutate(bounded, domain.NewID(), "claude.usage.stage", struct{ Account, Operation, Credential domain.ID }{account, op.ID, credential}, func(tx *store.Tx) (any, error) {
		if err := runEnded(); err != nil {
			return nil, err
		}
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		if !claudeUsageCurrent(a, op.ID, generation) || subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if err := a.Subscription.ClaudeUsage.Stage(identity, credential, profileCleaned, time.Now().UTC()); err != nil {
			return nil, err
		}
		if err := a.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if err != nil {
		// An unacknowledged commit may have staged the review. Read before
		// deciding: a section that holds this exact credential keeps it, every
		// other outcome leaves the intent that cleanup deletes.
		if s.claudeUsageStaged(account, op.ID, credential) {
			logger.WarnContext(ctx, "claude_usage_stage_commit_confirmed", "code", domain.SafeError(err).Code)
		} else {
			logger.Warn("claude_usage_stage_failed", "code", domain.SafeError(err).Code)
			return s.claudeUsageEndOutcome(ctx, nil, err)
		}
	}
	logger.InfoContext(ctx, "claude_usage_login_staged", "profile_cleaned", profileCleaned)
	return claudeUsageOutcome{}
}

// claudeUsageStaged reports whether the stored account holds credential in the
// identity review of operation.
func (s *Service) claudeUsageStaged(account, operation, credential domain.ID) bool {
	ctx, cancel := claudeUsageOwnerContext(context.Background())
	defer cancel()
	staged := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		staged = usage != nil && usage.Connection == domain.ClaudeUsageConfirming && usage.CredentialID == credential && usage.Operation != nil && usage.Operation.ID == operation
		return nil
	})
	return err == nil && staged
}

// settleClaudeUsage ends a login that never reached identity review. Owed
// cleanup stays recorded and is completed by the cleanup pass.
func (s *Service) settleClaudeUsage(account domain.ID, op domain.ClaudeUsageOperation, generation domain.ID, state domain.SubscriptionLoginState, diagnostic *domain.NativeSubscriptionDiagnostic) error {
	ctx, cancel := claudeUsageOwnerContext(context.Background())
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer s.claudeUsage.clearInput(op.ID)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.settle", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
	}{account, op.ID, state}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Connection != domain.ClaudeUsageConnecting || usage.Generation != generation || usage.Operation == nil || usage.Operation.ID != op.ID {
			// Already settled by a disconnect or logout that retired the section.
			return struct{}{}, nil
		}
		if err := usage.Settle(state, diagnostic); err != nil {
			return nil, err
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
	// Owed native storage and references are removed right away; failure only
	// leaves them recorded for the scanner.
	if err := s.cleanupClaudeUsageExcept(ctx, account, op.ID); err != nil {
		s.logger.Warn("claude_usage_cleanup_pending", "operation_id", op.ID, "code", domain.SafeError(err).Code)
	}
	return nil
}

// markClaudeUsageRecovery fences a login whose native child could not be proven
// stopped. The login is never relaunched.
func (s *Service) markClaudeUsageRecovery(account domain.ID, op domain.ClaudeUsageOperation, generation domain.ID, diagnostic *domain.NativeSubscriptionDiagnostic) error {
	ctx, cancel := claudeUsageOwnerContext(context.Background())
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer s.claudeUsage.clearInput(op.ID)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.recovery", struct{ Account, Operation domain.ID }{account, op.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, account, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Generation != generation || usage.Operation == nil || usage.Operation.ID != op.ID || !usage.MarkRecovery() {
			return struct{}{}, nil
		}
		operation := *usage.Operation
		operation.Diagnostic = diagnostic
		usage.Operation = &operation
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}
