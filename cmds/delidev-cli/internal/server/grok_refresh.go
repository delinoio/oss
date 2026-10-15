// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// Server-owned Grok refresh. The Go server calls the refresh_token grant at the
// fixed xAI token endpoint itself while it holds the account's exclusive
// server lease (ServerSubscriptionOperation.NativeStarted with a claimed
// pending operation and no Worker lease). The sequence is fixed:
//
//  1. lease: the queued operation is claimed (NativeStarted, Pending claimed);
//  2. read: the stored generation is opened against this server and account;
//  3. claim: RefreshClaimed is written durably, in its own transaction;
//  4. send: exactly one token request, with no retry;
//  5. verify: the response identity must equal the bound identity;
//  6. publish: the next generation is sealed, staged in the protected vault and
//     published by one account commit, and only then are superseded generations
//     removed (the same staged-generation contract as Take/Finish).
//
// Any failure before step 3 sent nothing, so it settles as a plain failure and
// the stored credential is untouched. After step 3 the refresh token may have
// been rotated: an outcome that is not proven (transport failure, truncated or
// malformed response, identity mismatch, a crash at any later point) ends in
// RECOVERY_REQUIRED, and the claimed token is never sent again.

// grokLogPhaseRefresh is the structured-log phase of every Grok refresh record.
const grokLogPhaseRefresh grokLoginPhase = "REFRESH"

// Refresh-only log steps.
const (
	grokStepVault    grokLoginStep = "vault"
	grokStepIdentity grokLoginStep = "identity"
	grokStepCleanup  grokLoginStep = "cleanup"
)

// grokFaultPoint names a point of a server-owned Grok operation at which tests
// may inject a crash or a failed write. A hook that panics simulates the process
// ending there: nothing after the point runs and the durable state is whatever
// had been committed.
type grokFaultPoint string

const (
	// grokFaultAfterLease runs after the lease transaction (login and refresh).
	grokFaultAfterLease grokFaultPoint = "after_lease"
	// grokFaultBeforeLoginFinish runs after the login steps returned and before the
	// settlement reads the pending operation: the window in which a cancellation
	// can race the outcome.
	grokFaultBeforeLoginFinish grokFaultPoint = "before_login_finish"
	grokFaultAfterClaim        grokFaultPoint = "after_claim"
	grokFaultAfterResponse     grokFaultPoint = "after_response"
	grokFaultAfterStage        grokFaultPoint = "after_stage"
	grokFaultAfterPersist      grokFaultPoint = "after_persist"

	// Logout fault points.
	grokFaultAfterRevokeClaim        grokFaultPoint = "after_revoke_claim"
	grokFaultAfterRevoke             grokFaultPoint = "after_revoke"
	grokFaultAfterRevocationRecorded grokFaultPoint = "after_revocation_recorded"
	grokFaultAfterLogoutCleanup      grokFaultPoint = "after_logout_cleanup"
	// grokFaultAfterRevokeClaimCommit and grokFaultAfterLogoutDisconnect run after
	// a committed transaction and stand for a lost commit acknowledgement.
	grokFaultAfterRevokeClaimCommit grokFaultPoint = "after_revoke_claim_commit"
	grokFaultAfterLogoutDisconnect  grokFaultPoint = "after_logout_disconnect"
)

// grokFault runs the test seam at point. Only the lease and settlement points
// (after_lease, after_stage, after_persist) act on the returned error: it stands
// for a write whose outcome is reported as failed, and after_lease and
// after_persist stand for a commit whose acknowledgement was lost although the
// transaction committed.
func (s *Service) grokFault(point grokFaultPoint) error {
	if s.grokFaultHook != nil {
		return s.grokFaultHook(point)
	}
	return nil
}

// grokServerAction reports the action of the queued server operation of a Grok
// account, if the account is a server-owned Grok account with one.
func (s *Service) grokServerAction(ctx context.Context, id domain.ID) (domain.SubscriptionAction, bool) {
	var action domain.SubscriptionAction
	matched := false
	_ = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, err := accountFromTx(tx, id, 0)
		if err == nil && a.Type == domain.SubscriptionAccount && a.SubscriptionService == domain.SubscriptionGrok && a.ProviderID == "" {
			matched = true
			if a.Subscription != nil && a.Subscription.ServerOperation != nil {
				action = a.Subscription.ServerOperation.Action
			}
		}
		return nil
	})
	return action, matched
}

// runGrokRefresh owns one server-owned Grok refresh from its lease to its
// settled outcome. It never sends a token request without a persisted claim and
// never sends one twice.
func (s *Service) runGrokRefresh(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil || a.Subscription.ServerOperation.Action != domain.SubscriptionRefresh {
			return subscriptionDenied()
		}
		original = *a.Subscription.ServerOperation
		return nil
	})
	if err != nil {
		return
	}
	unlock, err := s.lockAccounts(parent)
	if err != nil {
		return
	}
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.server.refresh-lease", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.ServerObservationActive() || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() || st.ServerOperation.Action != domain.SubscriptionRefresh || a.Connection == nil || st.Generation == "" || st.IdentityCommitment == "" {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		if subscriptionActorValid(tx, o.Actor) != nil || st.Pending.Canceled || !time.Now().Before(o.ExpiresAt) {
			o.State = domain.SubscriptionCanceled
			if !time.Now().Before(o.ExpiresAt) {
				o.State = domain.SubscriptionExpired
			}
			st.Pending = nil
		} else {
			o.NativeStarted = true
			o.Generation = st.Generation
			st.Pending.Phase = domain.SubscriptionClaimed
			original = *o
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	unlock()
	if err == nil {
		err = s.grokFault(grokFaultAfterLease)
	}
	if err != nil {
		if original.NativeStarted {
			// The lease transaction ran but its outcome is unknown: the durable
			// operation decides whether it is fenced for the scanner.
			s.fenceUnsettledGrokOperation(parent, id, original)
		}
		return
	}
	if !original.NativeStarted {
		return
	}
	s.logGrok(parent, slog.LevelInfo, "grok_refresh_started", original, grokStepClaim, "account_id", id)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	outcome := s.grokRefreshSteps(ctx, parent, id, original)
	cancel()
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 30*time.Second)
	defer stop()
	if err := s.finishGrokRefresh(bounded, id, original, &outcome); err != nil {
		s.logGrok(bounded, slog.LevelWarn, "grok_refresh_finish_failed", original, grokStepFinish, "code", domain.SafeError(err).Code)
		s.fenceUnsettledGrokOperation(bounded, id, original)
	}
	outcome.bundle.Clear()
}

// grokRefreshSteps reads the stored credential, claims the exchange, sends the
// single refresh request and verifies the result.
func (s *Service) grokRefreshSteps(ctx, parent context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (outcome grokOutcome) {
	fail := func(state domain.SubscriptionLoginState, code domain.Code, step grokLoginStep) grokOutcome {
		return grokOutcome{state: state, code: code, step: step, claimed: outcome.claimed}
	}
	// uncertain settles a claimed refresh whose outcome is not proven.
	uncertain := func(step grokLoginStep) grokOutcome {
		return grokOutcome{state: domain.SubscriptionRecovery, code: domain.RecoveryRequired, step: step, claimed: true}
	}
	interrupted := func(step grokLoginStep) grokOutcome {
		if parent.Err() == nil && !time.Now().Before(original.ExpiresAt) {
			return fail(domain.SubscriptionExpired, domain.Canceled, step)
		}
		return fail(domain.SubscriptionCanceled, domain.Canceled, step)
	}
	vault, err := s.secrets()
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_vault_unavailable", original, grokStepVault, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepVault)
	}
	stored, err := vault.Get(ctx, credentials.Ref{Owner: id, ID: original.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepVault)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_vault_read_failed", original, grokStepVault, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepVault)
	}
	current, err := subscription.OpenGrokBundle(stored, s.Identity.ServerID, id, original.Generation)
	clear(stored)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_bundle_invalid", original, grokStepVault)
		return fail(domain.SubscriptionFailed, domain.Internal, grokStepVault)
	}
	defer current.Clear()
	transport, closeTransport := s.grokOAuthRoundTripper()
	defer closeTransport()
	client := subscription.GrokOAuthClient{Transport: transport}
	// Discovery only supplies the JWKS and userinfo endpoints used to verify the
	// response. It runs before the claim, so a failure here sent nothing.
	endpoints, err := client.Discover(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepDiscovery)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_discovery_failed", original, grokStepDiscovery)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepDiscovery)
	}
	if err := s.claimGrokRefresh(ctx, id, original, s.grokIdentityCommitment(current)); err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepClaim)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_claim_failed", original, grokStepClaim, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.SafeError(err).Code, grokStepClaim)
	}
	outcome.claimed = true
	s.logGrok(ctx, slog.LevelInfo, "grok_refresh_exchange_claimed", original, grokStepClaim)
	_ = s.grokFault(grokFaultAfterClaim)
	refreshToken := current.RefreshTokenBytes()
	tokens, err := client.Refresh(ctx, refreshToken)
	clear(refreshToken)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		if reason == subscription.GrokReasonTokenRejected {
			// The provider refused the token, so it did not rotate. The stored
			// credential is unusable and the owner has to sign in again.
			s.logGrok(ctx, slog.LevelWarn, "grok_refresh_rejected", original, grokStepExchange)
			rejected := fail(domain.SubscriptionFailed, domain.Unauthenticated, grokStepExchange)
			rejected.rejected = true
			return rejected
		}
		// The provider may have rotated the token. It is never resent.
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_exchange_uncertain", original, grokStepExchange, "reason", reason)
		return uncertain(grokStepExchange)
	}
	_ = s.grokFault(grokFaultAfterResponse)
	claims, info, err := client.RefreshedClaims(ctx, endpoints, tokens)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_identity_unverified", original, grokStepIdentity, "reason", reason)
		return uncertain(grokStepIdentity)
	}
	next, err := subscription.RefreshGrokBundle(time.Now(), current, tokens, claims, info)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_mapping_failed", original, grokStepMapping, "reason", reason)
		return uncertain(grokStepMapping)
	}
	if err := subscription.GrokRefreshed(current, next); err != nil {
		next.Clear()
		s.logGrok(ctx, slog.LevelWarn, "grok_refresh_not_refreshed", original, grokStepMapping)
		return uncertain(grokStepMapping)
	}
	return grokOutcome{bundle: next, state: domain.SubscriptionSucceeded, step: grokStepMapping, claimed: true}
}

// claimGrokRefresh persists the exchange claim before the token request is
// sent. After it commits, the operation can only ever settle as an outcome of
// that one request; a crash or cancellation leaves recovery, never a resend.
// commitment must equal the account's stored identity commitment, so a stored
// generation that does not belong to the bound identity is never refreshed.
func (s *Service) claimGrokRefresh(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, commitment string) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.refresh-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		current := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Pending.Canceled || st.Lease != nil || current == nil || current.ID != o.ID || current.Epoch != o.Epoch || current.Action != domain.SubscriptionRefresh || current.State != domain.SubscriptionPreparing || !current.NativeStarted || current.RefreshClaimed || !time.Now().Before(current.ExpiresAt) || a.Connection == nil || st.Generation != o.Generation || subscriptionActorValid(tx, current.Actor) != nil {
			return nil, subscriptionDenied()
		}
		if st.IdentityCommitment == "" || commitment == "" || st.IdentityCommitment != commitment {
			return nil, domain.Fail(domain.Conflict, "The stored Grok credential does not match the account identity.", "Sign in to the account again.")
		}
		current.RefreshClaimed = true
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

// grokRefreshDiagnosticFor builds the durable diagnostic of a failed or
// uncertain refresh.
func grokRefreshDiagnosticFor(o domain.ServerSubscriptionOperation, state domain.SubscriptionLoginState, code domain.Code) *domain.GrokDiagnostic {
	if state != domain.SubscriptionFailed && state != domain.SubscriptionRecovery {
		return nil
	}
	return &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticRefresh, Code: code, CorrelationID: o.ID}
}

// finishGrokRefresh writes the settled outcome. A success seals and stages the
// next generation in the protected vault, then publishes the generation in one
// commit; superseded generations are removed only after that commit. A claimed
// refresh is never discarded by a late cancellation or actor change: the token
// may already have rotated, so its result is the only usable credential.
func (s *Service) finishGrokRefresh(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, outcome *grokOutcome) (returned error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	success := outcome.state == domain.SubscriptionSucceeded
	result, code := outcome.state, outcome.code
	commitment := ""
	if success {
		commitment = s.grokIdentityCommitment(outcome.bundle)
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		if success && (commitment == "" || commitment != st.IdentityCommitment) {
			// The verified identity no longer matches the account: the refresh
			// token was spent, so this is uncertain, not a plain failure.
			success, result, code = false, domain.SubscriptionRecovery, domain.RecoveryRequired
		}
		if !outcome.claimed && (st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil) && result != domain.SubscriptionRecovery {
			success, result, code = false, domain.SubscriptionCanceled, domain.Canceled
		}
		return nil
	})
	if err != nil {
		return err
	}
	vault, err := s.secrets()
	if err != nil {
		return err
	}
	generation := o.FinishID
	if success {
		envelope, e := subscription.SealGrokBundle(s.Identity.ServerID, id, generation, outcome.bundle)
		if e != nil {
			return e
		}
		// staged is set before the write begins: a write that reports an error may
		// still have left the generation behind, and an abort retires it unless the
		// account document references it.
		defer func() {
			if returned != nil {
				s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, generation)
			}
		}()
		_, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: generation, Purpose: credentials.AccountLogin}, envelope)
		clear(envelope)
		if e != nil {
			return e
		}
		if e := s.grokFault(grokFaultAfterStage); e != nil {
			return e
		}
	}
	diagnostic := grokRefreshDiagnosticFor(o, result, code)
	rejected := outcome.rejected
	_, err = s.Store.Mutate(ctx, generation, "grok.server.refresh-finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
		Code               domain.Code
	}{id, o.ID, result, code}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		if !outcome.claimed && (st.Pending.Canceled || subscriptionActorValid(tx, o.Actor) != nil) && result != domain.SubscriptionRecovery {
			success, result, code, diagnostic = false, domain.SubscriptionCanceled, domain.Canceled, nil
		}
		st.ServerOperation.GrokDiagnostic = diagnostic
		if result == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			st.ServerOperation.State = domain.SubscriptionRecovery
		} else {
			st.ServerOperation.NativeStarted = false
			st.ServerOperation.State = result
			st.Pending = nil
			switch {
			case success:
				st.Generation = generation
				if a.Health == domain.AccountExpired || a.Health == domain.AccountFailed {
					a.Health = domain.AccountReady
				}
			case rejected:
				// The provider refused the stored refresh token: the credential is
				// unusable until the owner signs in again.
				a.Health = domain.AccountExpired
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: id}, nil
	})
	if err != nil {
		return err
	}
	if err := s.grokFault(grokFaultAfterPersist); err != nil {
		return err
	}
	if result != domain.SubscriptionRecovery {
		keep := o.Generation
		if success {
			keep = generation
		}
		// The commit is final, so a removal that cannot be confirmed does not undo
		// it and does not fence the account: the superseded generation is
		// unreferenced, never granted, and removed by the next cleanup.
		cleanup, cancel := grokBookkeepingContext(ctx, grokCleanupTimeout)
		defer cancel()
		if err := cleanupSubscriptionReferences(cleanup, vault, id, keep); err != nil {
			s.logGrok(ctx, slog.LevelWarn, "grok_refresh_cleanup_pending", o, grokStepCleanup, "code", domain.SafeError(err).Code)
		}
	}
	level := slog.LevelInfo
	if !success {
		level = slog.LevelWarn
	}
	s.logGrok(ctx, level, "grok_refresh_finished", o, grokStepFinish, "state", result, "code", code, "claimed", outcome.claimed)
	return nil
}

// grokDiagnosticPhaseOf maps a Grok server operation action to the phase of its
// durable diagnostic.
func grokDiagnosticPhaseOf(action domain.SubscriptionAction) domain.GrokDiagnosticPhase {
	switch action {
	case domain.SubscriptionRefresh:
		return domain.GrokDiagnosticRefresh
	case domain.SubscriptionLogout:
		return domain.GrokDiagnosticLogout
	}
	return domain.GrokDiagnosticLogin
}

// fenceUnsettledGrokOperation runs after a finish returned an error. The error
// does not say whether the settling transaction committed (a commit
// acknowledgement can be lost after the transaction committed), so the durable
// operation decides: a settled operation is the committed outcome and stays as it
// is, and one that is still pending is fenced recovery-required with its own
// diagnostic. It never rewrites a committed settlement.
func (s *Service) fenceUnsettledGrokOperation(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) {
	fenced, err := s.markGrokRecovery(id, o.ID)
	switch {
	case err != nil:
		s.logGrok(ctx, slog.LevelWarn, "grok_recovery_unconfirmed", o, grokStepFinish, "code", domain.SafeError(err).Code)
	case fenced:
		s.logGrok(ctx, slog.LevelWarn, "grok_operation_fenced", o, grokStepFinish)
	default:
		s.logGrok(ctx, slog.LevelInfo, "grok_settlement_reconciled", o, grokStepFinish)
	}
}

// markGrokRecovery fences a Grok server operation that is still pending, records
// the diagnostic of its phase (code recovery_required, the operation as
// correlation id) and reports whether it fenced anything. An operation of another
// id is refused; one that is no longer the account's pending operation has
// settled and is left untouched.
func (s *Service) markGrokRecovery(id, operation domain.ID) (bool, error) {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	fenced := false
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.recovery", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		fenced = false
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		if st.Pending == nil || st.Pending.ID != operation || !o.NativeStarted {
			// The settling transaction committed.
			return struct{}{}, nil
		}
		fenced = true
		o.State = domain.SubscriptionRecovery
		if o.GrokDiagnostic == nil {
			code := domain.RecoveryRequired
			if o.GrokRevocation == domain.GrokRevocationConfirmed {
				// The provider revoked the token; only the protected cleanup is
				// pending, so the outcome is known and the cleanup is retried.
				code = domain.Unavailable
			}
			o.GrokDiagnostic = &domain.GrokDiagnostic{Phase: grokDiagnosticPhaseOf(o.Action), Code: code, CorrelationID: o.ID}
		}
		st.RecoveryRequired = true
		a.Health = domain.AccountFailed
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil && fenced {
		delete(s.subscriptionProgress, operation)
	}
	return fenced && err == nil, err
}

// grokRefreshNeedsRecovery selects an interrupted Grok refresh whose exchange
// was never claimed: a restart fenced it, but no token request was ever sent, so
// the stored credential is intact and the operation settles as a failed refresh.
// A claimed refresh stays in recovery and is never resolved automatically.
func grokRefreshNeedsRecovery(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.Removal != nil || a.Connection == nil || st == nil || st.Generation == "" || st.Lease != nil || !st.RecoveryRequired {
		return false
	}
	o := st.ServerOperation
	return o != nil && o.Action == domain.SubscriptionRefresh && o.State == domain.SubscriptionRecovery && !o.RefreshClaimed && st.Pending != nil && st.Pending.ID == o.ID
}

// recoverGrokRefresh settles an unclaimed interrupted refresh. It holds the
// account gate, so it cannot race the original goroutine's finish.
func (s *Service) recoverGrokRefresh(ctx context.Context, id, operation domain.ID) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.server.refresh-recover", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if !grokRefreshNeedsRecovery(a) || a.Subscription.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		st := a.Subscription
		o := st.ServerOperation
		o.State = domain.SubscriptionFailed
		o.NativeStarted = false
		o.GrokDiagnostic = &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticRefresh, Code: domain.Unavailable, CorrelationID: o.ID}
		st.Pending = nil
		st.RecoveryRequired = false
		// The restart fenced the account as failed whatever its health was; the
		// credential was never touched, so the health the refresh was admitted
		// under returns (an expired account stays expired, it is not made ready).
		if a.Health == domain.AccountFailed {
			a.Health = domain.AccountReady
			if o.HealthBefore != "" {
				a.Health = o.HealthBefore
			}
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		delete(s.subscriptionProgress, operation)
		s.logger.InfoContext(ctx, "grok_refresh_recovered", "phase", grokLogPhaseRefresh, "operation_id", operation, "correlation_id", operation, "claimed", false)
	}
	return err
}
