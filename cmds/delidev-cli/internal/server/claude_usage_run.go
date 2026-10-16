// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// claudeUsageLoginResult is the outcome one runner reports to its finish step.
type claudeUsageLoginResult struct {
	state    domain.ClaudeLimitLoginState
	failure  domain.ClaudeLimitFailure
	identity *domain.ClaudeLimitIdentity
	// credential and envelope are the staged claude-usage reference and its
	// sealed bundle. They exist only for a confirming result.
	credential domain.ID
	envelope   []byte
	// cleanup is true once the native child is joined and the profile's native
	// credential storage is proven empty.
	cleanup bool
}

// runClaudeUsageLogin owns exactly one usage login from claim to settlement.
func (s *Service) runClaudeUsageLogin(parent context.Context, id domain.ID) {
	var original domain.ClaudeLimitOperation
	claimed := false
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "claude.usage.claim", id, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		if o.State != domain.ClaudeLimitLoginPreparing || o.NativeStarted || o.Epoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		switch {
		case o.Canceled || subscriptionActorValid(tx, o.Actor) != nil:
			o.State = domain.ClaudeLimitLoginCanceled
		case !time.Now().Before(o.ExpiresAt):
			o.State = domain.ClaudeLimitLoginExpired
		default:
			o.NativeStarted = true
			claimed = true
			original = *o
		}
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	unlock()
	if err != nil || !claimed {
		return
	}
	s.logger.InfoContext(parent, "claude_usage_login_started", "account_id", id, "operation_id", original.ID)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	if s.afterClaudeUsageClaim != nil {
		s.afterClaudeUsageClaim()
	}
	run, ok := s.registerClaudeUsageRun(id, cancel)
	if !ok {
		cancel()
		s.settleUnstartedClaudeUsage(id, original)
		return
	}
	defer s.finishClaudeUsageRun(id, run)
	// A disconnect, logout or deletion that committed between the claim and the
	// registration found no run to cancel and may already have removed the
	// profile. Registration now makes every later revocation reach this run, and
	// the claimed operation is checked again before any native resource opens.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil || usage.Operation.ID != original.ID || usage.Operation.Canceled || subscriptionActorValid(tx, original.Actor) != nil {
			return subscriptionDenied()
		}
		return nil
	}); err != nil {
		s.logger.WarnContext(parent, "claude_usage_login_revoked_before_native_work", "account_id", id, "operation_id", original.ID, "code", domain.SafeError(err).Code)
		cancel()
	}
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.Store.Read(ctx, func(tx *store.Tx) error {
				_, a, err := claudeUsageAccountTx(tx, id, 0)
				if err != nil {
					return err
				}
				usage := a.Subscription.ClaudeUsage
				if usage == nil || usage.Operation == nil || usage.Operation.ID != original.ID || usage.Operation.Canceled || subscriptionActorValid(tx, original.Actor) != nil || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired {
					return subscriptionDenied()
				}
				return nil
			})
			if err != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var result claudeUsageLoginResult
	if ctx.Err() == nil {
		result = s.executeClaudeUsageLogin(ctx, id, original)
	}
	expired := !time.Now().Before(original.ExpiresAt)
	interrupted := parent.Err() != nil
	cancel()
	<-checked
	switch {
	case result.state != "":
	case interrupted:
		result.state, result.failure = domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureInterrupted
	case expired:
		result.state = domain.ClaudeLimitLoginExpired
	default:
		result.state = domain.ClaudeLimitLoginCanceled
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishClaudeUsageLogin(bounded, id, original, result); err != nil {
		s.logger.WarnContext(bounded, "claude_usage_finish_failed", "operation_id", original.ID, "code", domain.SafeError(err).Code, "cleanup_confirmed", result.cleanup)
		s.markClaudeUsageRecovery(id, original.ID, result.credential)
	}
	clear(result.envelope)
}

// settleUnstartedClaudeUsage closes an operation whose run could not register
// because another run owns the account. No native work started.
func (s *Service) settleUnstartedClaudeUsage(id domain.ID, original domain.ClaudeLimitOperation) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
	_ = s.finishClaudeUsageLogin(ctx, id, original, claudeUsageLoginResult{state: domain.ClaudeLimitLoginFailed, failure: domain.ClaudeLimitFailureProfileUnsafe, cleanup: true})
}

// executeClaudeUsageLogin drives the original login child. It reports the
// validated identity and a sealed bundle only after the child exited
// successfully and was joined and the profile's native storage was emptied.
func (s *Service) executeClaudeUsageLogin(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation) (result claudeUsageLoginResult) {
	result.cleanup = true
	opener := s.claudeUsageOpen
	if opener == nil {
		opener = openClaudeUsageNative
	}
	native, err := opener(ctx, s.scopeFor(id), op.ID, s.logger)
	if err != nil {
		if claudeUsageLeaseLeaked(err) {
			// The failed open could not release its profile lease: the lease may stay
			// locked, so the operation enters cleanup recovery instead of a plain failure.
			result.cleanup = false
			result.state, result.failure = domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain
			return result
		}
		result.state, result.failure = domain.ClaudeLimitLoginFailed, claudeUsageFailureFor(err, domain.ClaudeLimitFailureInstallation)
		if result.failure == domain.ClaudeLimitFailureInstallation || result.failure == domain.ClaudeLimitFailureLoginUnsupported {
			result.state = domain.ClaudeLimitLoginUnsupported
		}
		if ctx.Err() != nil {
			result.state = ""
		}
		return result
	}
	defer func() {
		if err := native.Close(); err != nil {
			result.cleanup = false
			if result.state == domain.ClaudeLimitLoginConfirming {
				// The profile lease is not proven released, so nothing is offered for
				// confirmation and no credential is staged.
				clear(result.envelope)
				result.envelope, result.identity, result.credential = nil, nil, ""
				result.state, result.failure = domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain
			}
		}
	}()
	storage := native.Storage()
	// A first admission must find empty native storage. Anything present is
	// foreign or pre-existing: it is neither adopted nor deleted.
	if present, err := storage.Present(ctx); err != nil || present {
		result.state, result.failure = domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureProfileUnsafe
		return result
	}
	login, err := native.StartLogin(ctx)
	if err != nil {
		result.state, result.failure = domain.ClaudeLimitLoginFailed, claudeUsageFailureFor(err, domain.ClaudeLimitFailureLoginUnsupported)
		if ctx.Err() != nil {
			result.state = ""
		}
		return result
	}
	runErr := s.driveClaudeUsageLogin(ctx, id, op, login)
	// Join the original child before any state can claim cleanup.
	if err := login.Close(); err != nil {
		result.cleanup = false
		result.state, result.failure = domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain
		return result
	}
	removeStorage := func() {
		bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer stop()
		if err := storage.Remove(bounded); err != nil {
			result.cleanup = false
		}
	}
	if runErr != nil {
		removeStorage()
		if ctx.Err() == nil {
			result.state, result.failure = domain.ClaudeLimitLoginFailed, claudeUsageFailureFor(runErr, domain.ClaudeLimitFailureLoginRejected)
			if result.failure == domain.ClaudeLimitFailureLoginNotInteractive || result.failure == domain.ClaudeLimitFailureLoginUnsupported {
				result.state = domain.ClaudeLimitLoginUnsupported
			}
		}
		return result
	}
	identity, envelope, credential, failure := s.captureClaudeUsage(ctx, id, op, native)
	removeStorage()
	if failure != domain.ClaudeLimitFailureNone || !result.cleanup {
		clear(envelope)
		result.state, result.failure = domain.ClaudeLimitLoginFailed, failure
		if !result.cleanup {
			result.state, result.failure = domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain
		}
		return result
	}
	result.state, result.identity, result.credential, result.envelope = domain.ClaudeLimitLoginConfirming, identity, credential, envelope
	return result
}

// driveClaudeUsageLogin publishes the original address, delivers the one
// approval code and waits for the child to exit. It returns nil only for a
// successful exit.
func (s *Service) driveClaudeUsageLogin(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation, login claudeUsageLoginProcess) error {
	published, delivered := false, false
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		progress, err := login.Progress()
		if err != nil {
			return err
		}
		if progress.URL != "" && !published {
			if err := claude.ValidateUsageLoginURL(progress.URL); err != nil {
				s.logger.WarnContext(ctx, "claude_usage_login_url_rejected", "operation_id", op.ID, "failure", claudeUsageFailureFor(err, domain.ClaudeLimitFailureLoginUnsupported))
				return err
			}
			if progress.Method != domain.SubscriptionBrowserCode {
				return domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureLoginNotInteractive, 0)
			}
			if err := s.publishClaudeUsageWaiting(ctx, id, op, progress.URL); err != nil {
				return err
			}
			published = true
		}
		if published && !delivered {
			code, claimed, err := s.takeClaudeUsageCode(ctx, id, op)
			if err != nil {
				return err
			}
			if claimed {
				if err := login.Submit(code); err != nil {
					return err
				}
				delivered = true
				s.logger.InfoContext(ctx, "claude_usage_code_delivered", "operation_id", op.ID)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-login.Done():
			return login.Wait()
		}
	}
}

func (s *Service) publishClaudeUsageWaiting(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation, url string) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.waiting", struct{ Account, Operation domain.ID }{id, op.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil || usage.Operation.ID != op.ID || usage.Operation.Canceled || subscriptionActorValid(tx, op.Actor) != nil {
			return nil, subscriptionDenied()
		}
		usage.Operation.State = domain.ClaudeLimitLoginWaiting
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err == nil {
		s.setClaudeUsagePresentation(op.ID, claudeUsagePresentation{URL: url, Until: op.ExpiresAt})
	}
	return err
}

// takeClaudeUsageCode records durable consumption before releasing bytes. An
// uncertain step never permits a second delivery of the same approval code.
func (s *Service) takeClaudeUsageCode(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation) ([]byte, bool, error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	var ready bool
	observe := func(tx *store.Tx, consume bool) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil {
			return nil, subscriptionDenied()
		}
		current := usage.Operation
		if current.ID != op.ID || current.Canceled || current.State != domain.ClaudeLimitLoginWaiting || current.CodeConsumed || subscriptionActorValid(tx, current.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if current.CodeSubmissionID == "" {
			return accountReceipt{ID: r.ID}, nil
		}
		unlockMemory := s.claudeUsage.lock()
		have := len(s.claudeUsage.codes[op.ID]) != 0
		unlockMemory()
		if !have {
			return nil, subscriptionDenied()
		}
		ready = true
		if !consume {
			return accountReceipt{ID: r.ID}, nil
		}
		current.CodeConsumed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: r.ID}, err
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, err := observe(tx, false); return err }); err != nil || !ready {
		return nil, false, err
	}
	if _, err := s.Store.Mutate(ctx, domain.NewID(), "claude.usage.take-code", struct{ Account, Operation domain.ID }{id, op.ID}, func(tx *store.Tx) (any, error) { return observe(tx, true) }); err != nil {
		return nil, false, err
	}
	unlockMemory := s.claudeUsage.lock()
	code := slices.Clone(s.claudeUsage.codes[op.ID])
	clear(s.claudeUsage.codes[op.ID])
	delete(s.claudeUsage.codes, op.ID)
	unlockMemory()
	return code, true, nil
}

// captureClaudeUsage reads the validated identity and the native bundle of the
// newly created usage profile and seals it for the vault. It reads no other
// profile. The returned envelope belongs to the caller.
func (s *Service) captureClaudeUsage(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation, native claudeUsageNative) (*domain.ClaudeLimitIdentity, []byte, domain.ID, domain.ClaudeLimitFailure) {
	identity, err := native.Identity(ctx)
	if err != nil {
		return nil, nil, "", claudeUsageFailureFor(err, domain.ClaudeLimitFailureLoginRejected)
	}
	raw, err := native.Storage().Read(ctx)
	if err != nil {
		return nil, nil, "", claudeUsageFailureFor(err, domain.ClaudeLimitFailureCredentials)
	}
	bundle, err := subscription.ParseClaudeUsageNative(raw, time.Now().UTC())
	clear(raw)
	if err != nil {
		return nil, nil, "", domain.ClaudeLimitFailureCredentials
	}
	defer bundle.Clear()
	credential := domain.NewID()
	envelope, err := subscription.SealClaudeUsage(s.Identity.ServerID, id, op.Generation, credential, bundle, time.Now().UTC())
	if err != nil {
		return nil, nil, "", domain.ClaudeLimitFailureCredentials
	}
	validated := &domain.ClaudeLimitIdentity{Email: identity.Email, Organization: identity.Organization, Plan: identity.Plan, Commitment: s.claudeLimitIdentityCommitment(id, identity)}
	if validated.Validate() != nil {
		clear(envelope)
		return nil, nil, "", domain.ClaudeLimitFailureLoginRejected
	}
	return validated, envelope, credential, domain.ClaudeLimitFailureNone
}

// finishClaudeUsageLogin settles one login. The vault write and the account
// commit happen under the account gate, so a cancel or revocation cannot slip
// between the decision to publish a staged credential and its commit.
func (s *Service) finishClaudeUsageLogin(ctx context.Context, id domain.ID, op domain.ClaudeLimitOperation, result claudeUsageLoginResult) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer s.clearClaudeUsageInput(op.ID)
	canceled := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil || usage.Operation.ID != op.ID || usage.Operation.Epoch != op.Epoch || !usage.Operation.Active() {
			return subscriptionDenied()
		}
		canceled = usage.Operation.Canceled || subscriptionActorValid(tx, op.Actor) != nil || a.Subscription.Pending != nil || a.Subscription.RecoveryRequired
		return nil
	})
	if err != nil {
		return err
	}
	if canceled && result.state == domain.ClaudeLimitLoginConfirming {
		clear(result.envelope)
		result.envelope, result.state, result.identity, result.credential = nil, domain.ClaudeLimitLoginCanceled, nil, ""
	}
	if !result.cleanup && result.state == domain.ClaudeLimitLoginConfirming {
		// Unproven native cleanup never offers a credential for confirmation.
		clear(result.envelope)
		result.envelope, result.identity, result.credential = nil, nil, ""
		result.state, result.failure = domain.ClaudeLimitLoginRecovery, domain.ClaudeLimitFailureCleanupUncertain
	}
	staged := false
	if result.state == domain.ClaudeLimitLoginConfirming {
		if err := s.stageClaudeUsageCredential(ctx, id, result.credential, result.envelope); err != nil {
			// The vault may already hold the reference although the write was not
			// acknowledged. Its identifier is the only handle, so it becomes a durable
			// removal obligation before the login is settled as failed.
			if recordErr := s.recordClaudeUsageRetirement(ctx, id, result.credential); recordErr != nil {
				return err
			}
			clear(result.envelope)
			result.envelope, result.identity, result.credential = nil, nil, ""
			result.state, result.failure = domain.ClaudeLimitLoginFailed, domain.ClaudeLimitFailureCredentials
		} else {
			staged = true
		}
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.finish", struct{ Account, Operation domain.ID }{id, op.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil || usage.Operation.ID != op.ID || usage.Operation.Epoch != op.Epoch || !usage.Operation.Active() {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		o.NativeStarted = false
		o.Failure = result.failure
		o.State = result.state
		switch result.state {
		case domain.ClaudeLimitLoginConfirming:
			o.CredentialID, o.Identity = result.credential, result.identity
		case domain.ClaudeLimitLoginCanceled:
			o.Canceled = true
		}
		if !result.cleanup {
			// A recovering operation retains neither a pending identity nor a staged
			// credential: the reference is owed a deletion instead.
			addClaudeUsageRetired(usage, o.CredentialID)
			o.CredentialID, o.Identity = "", nil
			o.State = domain.ClaudeLimitLoginRecovery
			usage.CleanupPending = true
		}
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err != nil {
		if staged {
			// The commit may or may not have happened. A confirming operation
			// that references the staged reference keeps it; otherwise it is
			// removed. An unreadable account deletes nothing.
			if published, e := s.claudeUsageStagedPublished(id, op.ID, result.credential); e == nil && !published {
				if deleteErr := s.deleteClaudeUsageReference(ctx, id, result.credential); deleteErr != nil {
					// Losing this identifier would retain the OAuth credential until the whole
					// account is removed, so the deletion becomes a durable obligation.
					s.logger.WarnContext(ctx, "claude_usage_staging_cleanup_pending", "account_id", id, "operation_id", op.ID, "code", domain.SafeError(deleteErr).Code)
					_ = s.recordClaudeUsageRetirement(ctx, id, result.credential)
				}
			} else if e == nil && published {
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	level := "claude_usage_login_finished"
	s.logger.InfoContext(ctx, level, "account_id", id, "operation_id", op.ID, "state", result.state, "failure", result.failure, "cleanup_confirmed", result.cleanup)
	return nil
}

// claudeUsageStagedPublished reports whether the stored operation references
// the staged credential.
func (s *Service) claudeUsageStagedPublished(id, operation, credential domain.ID) (bool, error) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 10*time.Second)
	defer cancel()
	published := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription != nil && a.Subscription.ClaudeUsage != nil {
			usage := a.Subscription.ClaudeUsage
			published = usage.Operation != nil && usage.Operation.ID == operation && usage.Operation.CredentialID == credential || usage.CredentialID == credential
		}
		return nil
	})
	return published, err
}

func (s *Service) deleteClaudeUsageReference(ctx context.Context, account, credential domain.ID) error {
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	return vault.Delete(ctx, credentials.Ref{Owner: account, ID: credential, Purpose: credentials.ClaudeUsage})
}

// markClaudeUsageRecovery fences an operation whose settlement could not be
// committed. It retains the original operation for the cleanup scanner and never
// relaunches anything.
func (s *Service) markClaudeUsageRecovery(id, operation domain.ID, staged ...domain.ID) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return
	}
	defer unlock()
	s.clearClaudeUsageInput(operation)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "claude.usage.recovery", struct {
		Account, Operation domain.ID
		Staged             []domain.ID
	}{id, operation, staged}, func(tx *store.Tx) (any, error) {
		r, a, err := claudeUsageAccountTx(tx, id, 0)
		if err != nil {
			return nil, err
		}
		usage := a.Subscription.ClaudeUsage
		if usage == nil || usage.Operation == nil || usage.Operation.ID != operation || !usage.Operation.Active() {
			return nil, subscriptionDenied()
		}
		o := usage.Operation
		// A reference this settlement staged or may have staged is owed a deletion
		// unless the connection already publishes it; a recovering operation keeps
		// neither a pending identity nor a staged reference.
		for _, reference := range staged {
			if reference != usage.CredentialID {
				addClaudeUsageRetired(usage, reference)
			}
		}
		addClaudeUsageRetired(usage, o.CredentialID)
		o.CredentialID, o.Identity = "", nil
		o.State = domain.ClaudeLimitLoginRecovery
		o.NativeStarted = false
		o.Failure = domain.ClaudeLimitFailureCleanupUncertain
		usage.CleanupPending = true
		return struct{}{}, putClaudeUsage(tx, r, a)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("claude_usage_recovery_unconfirmed", "operation_id", operation, "code", domain.SafeError(err).Code)
	}
}
