// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/google/uuid"
)

// The server-owned Grok logout. The server revokes the refresh token itself at
// the discovered revocation endpoint and removes the protected credential; no
// native Grok process takes part. One runner owns one operation:
//
//  1. Acceptance marks the account logging out (health revoked, new execution
//     refused with a typed unavailable problem) and queues the operation. The
//     scanner starts the runner only while no Worker lease exists, so the logout
//     waits for the original execution exit; a Worker lease whose private home
//     cleanup was not confirmed keeps the account leased and the logout queued.
//  2. The runner claims the operation (the exclusive account lease, the claimed
//     ServerOperation), freezes the credential generation and reads the bound
//     bundle. A failure before anything is sent leaves the credential in place and
//     the account usable (FAILED).
//  3. The durable revocation claim is committed and only then the one revocation
//     request is sent. A confirmed revocation (success or an already invalid
//     token) is the only outcome that lets the credential be removed. A definite
//     rejection or any uncertain outcome (transport error, timeout, 5xx,
//     interruption, restart) leaves the account RECOVERY_REQUIRED with the bundle
//     kept: the credential is never deleted without confirmed revocation or a
//     recovery state.
//  4. After a confirmed revocation one transaction publishes the disconnected
//     account (no connection, generation or identity), requiring that no Worker
//     lease exists (every Worker home was confirmed clean by its original owner
//     before its lease was released); only afterwards are the vault generations
//     removed. A failed removal leaves the retained logout-cleanup recovery shape
//     that explicit account deletion consumes.
//
// No token, path or identity appears in a log, an error, an event or a document.

const (
	// grokLogoutPhase and grokCleanupPhase are the phases of the structured
	// records of this flow.
	grokLogoutPhase  = "LOGOUT"
	grokCleanupPhase = "CLEANUP"
	// grokLogoutWindow is the acceptance window of a logout. A logout may wait for
	// the original execution exit for as long as the execution runs, so it never
	// expires on the short login window.
	grokLogoutWindow = 30 * 24 * time.Hour
	// grokLogoutRunTimeout bounds one runner's discovery and revocation requests.
	grokLogoutRunTimeout = 2 * time.Minute
	// grokLogoutDeleteWait bounds how long account deletion waits for a logout it
	// did not run itself.
	grokLogoutDeleteWait = 30 * time.Second
)

// grokLogoutStage names the points of a logout at which fault injection can
// simulate a process crash. Tests set Service.grokLogoutCrash; production leaves
// it nil.
type grokLogoutStage string

const (
	// grokLogoutAfterClaim is after the durable revocation claim and before the
	// request.
	grokLogoutAfterClaim grokLogoutStage = "after_claim"
	// grokLogoutAfterRevoke is after the revocation was confirmed and before the
	// account was settled.
	grokLogoutAfterRevoke grokLogoutStage = "after_revoke"
	// grokLogoutAfterPublish is after the disconnected account was published and
	// before the vault generations were removed.
	grokLogoutAfterPublish grokLogoutStage = "after_publish"
)

func (s *Service) grokLogoutCrashes(stage grokLogoutStage) bool {
	return s.grokLogoutCrash != nil && s.grokLogoutCrash(stage)
}

// grokLoggingOutProblem is the typed refusal of new work on an account whose
// logout was accepted.
func grokLoggingOutProblem() *domain.Error {
	return domain.Fail(domain.Unavailable, "The Grok account is logging out.", "Wait for the logout to finish, then connect the account again before starting new work.")
}

// grokAccountLoggingOut reports whether a server-owned logout holds the account:
// accepted, running, or recovering. New Grok execution must be refused with
// grokLoggingOutProblem while it is true.
func grokAccountLoggingOut(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.Pending == nil || st.ServerOperation == nil {
		return false
	}
	return st.Pending.Action == domain.SubscriptionLogout && st.Pending.MachineID == "" && st.ServerOperation.ID == st.Pending.ID && st.ServerOperation.Action == domain.SubscriptionLogout
}

// grokLogoutCandidate selects a connected Grok account whose server-owned logout
// was accepted and not yet claimed, and which no Worker lease holds: the logout
// starts only after the original execution (or lifecycle) lease was released
// with confirmed cleanup.
func grokLogoutCandidate(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.RecoveryRequired || st.Lease != nil || st.ServerOperation == nil || st.Pending == nil || a.Connection == nil || st.Generation == "" {
		return false
	}
	o := st.ServerOperation
	return o.Action == domain.SubscriptionLogout && o.State == domain.SubscriptionPreparing && !o.NativeStarted && st.Pending.ID == o.ID && st.Pending.Phase == domain.SubscriptionQueued && st.Pending.MachineID == ""
}

// grokLogoutRetryable reports whether a new explicit LOGOUT request may replace
// the account's unfinished logout: the original actor retries a logout whose
// revocation was rejected or uncertain, or that a restart interrupted, while the
// credential is still stored. Revocation is idempotent, so a repeated request is
// safe. A different actor, a Worker lease or any other recovery never qualifies.
func grokLogoutRetryable(a domain.Account, actor domain.Principal) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || st == nil || !st.RecoveryRequired || st.Lease != nil || a.Connection == nil || st.Generation == "" || a.Removal != nil {
		return false
	}
	o := st.ServerOperation
	if o == nil || o.Action != domain.SubscriptionLogout || o.State != domain.SubscriptionRecovery || o.Generation != st.Generation || o.Actor != actor {
		return false
	}
	return st.Pending == nil || st.Pending.ID == o.ID && st.Pending.MachineID == ""
}

// grokLogoutRetryableBy extends grokLogoutRetryable for a logout whose original
// actor can no longer act, because its client device was revoked while the logout
// stayed queued: the owner takes over the retry, since nobody else could finish
// the unfinished logout. A different client never qualifies.
func grokLogoutRetryableBy(tx *store.Tx, a domain.Account, actor domain.Principal) bool {
	if grokLogoutRetryable(a, actor) {
		return true
	}
	st := a.Subscription
	if actor.Type != domain.OwnerDevice || st == nil || st.ServerOperation == nil {
		return false
	}
	original := st.ServerOperation.Actor
	return original.Type == domain.ClientDevice && subscriptionActorValid(tx, original) != nil && grokLogoutRetryable(a, original)
}

// grokLogoutOutcome is the decided result of the revocation step.
type grokLogoutOutcome struct {
	// revoked is true only after a confirmed revocation.
	revoked    bool
	revocation subscription.GrokRevocation
	state      domain.SubscriptionLoginState
	reason     subscription.GrokOAuthReason
	code       domain.Code
}

// runGrokLogout owns one server-owned Grok logout from claim to settlement.
func (s *Service) runGrokLogout(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if !grokLogoutCandidate(a) {
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
	claimed := false
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.logout.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		claimed = false
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if !grokLogoutCandidate(a) || st.ServerOperation.ID != original.ID || st.ServerOperation.Epoch != s.subscriptionServerEpoch() {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		o.NativeStarted = true
		o.Generation = st.Generation
		st.Pending.Phase = domain.SubscriptionClaimed
		original = *o
		claimed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	unlock()
	if err != nil || !claimed {
		return
	}
	s.logger.InfoContext(parent, "grok_logout_started", "phase", grokLogoutPhase, "account_id", id, "operation_id", original.ID, "correlation_id", original.ID)
	ctx, cancel := context.WithTimeout(parent, grokLogoutRunTimeout)
	out, crashed := s.performGrokLogout(ctx, id, original)
	cancel()
	if crashed {
		// A simulated process death: nothing settles, exactly as after a kill.
		return
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), grokLoginSettleTimeout)
	defer stop()
	crashed, err = s.finishGrokLogout(bounded, id, original, out, parent.Err() != nil)
	if err != nil && !crashed {
		if e := s.markGrokLoginRecovery(id, original.ID); e != nil {
			s.logger.Warn("grok_logout_recovery_unconfirmed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(e).Code)
		}
		s.logger.Warn("grok_logout_finish_failed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
	}
}

// grokLogoutUnsent is the outcome of a logout that stopped before the
// revocation claim: nothing was sent, the credential stays stored and the
// account becomes usable again. An unusable stored bundle is recovery instead,
// because the account then holds a credential this server cannot revoke.
func grokLogoutUnsent(err error) grokLogoutOutcome {
	if reason, _ := subscription.GrokOAuthReasonOf(err); reason != "" {
		return grokLogoutOutcome{state: domain.SubscriptionFailed, reason: reason, code: domain.Unavailable}
	}
	if domain.SafeError(err).Code == domain.RecoveryRequired {
		return grokLogoutOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonMapping}
	}
	return grokLogoutOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonResponse, code: domain.Unavailable}
}

// performGrokLogout reads the bound bundle, claims the revocation and sends the
// one revocation request. crashed is true only when fault injection simulated a
// process death.
func (s *Service) performGrokLogout(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (out grokLogoutOutcome, crashed bool) {
	profile := subscription.DefaultGrokOAuthProfile()
	client, release := s.grokLoginClient()
	defer release()
	bound, err := s.openGrokBoundBundle(ctx, id, original)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_logout_bundle_unavailable", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		return grokLogoutUnsent(err), false
	}
	defer bound.Clear()
	endpoints, err := subscription.DiscoverGrokEndpoints(ctx, client, profile)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_logout_discovery_failed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID)
		return grokLogoutUnsent(err), false
	}
	if endpoints.Revocation == "" {
		s.logger.WarnContext(ctx, "grok_logout_revocation_endpoint_missing", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID)
		return grokLogoutUnsent(subscription.RevocationEndpointMissing()), false
	}
	if err := s.claimGrokRevocation(ctx, id, original); err != nil {
		s.logger.WarnContext(ctx, "grok_logout_claim_refused", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		return grokLogoutOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonResponse, code: domain.Conflict}, false
	}
	s.logger.InfoContext(ctx, "grok_logout_revocation_claimed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID)
	if s.grokLogoutCrashes(grokLogoutAfterClaim) {
		return grokLogoutOutcome{}, true
	}
	// From here on the request may reach the provider: only a confirmed
	// revocation lets the credential be removed.
	revocation, err := subscription.RevokeGrokRefreshToken(ctx, client, profile, endpoints, bound.RefreshToken())
	if err != nil {
		reason, uncertain := subscription.GrokOAuthReasonOf(err)
		if reason == "" {
			reason = subscription.GrokReasonRevocationUncertain
		}
		s.logger.WarnContext(ctx, "grok_logout_revocation_unconfirmed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "reason", reason, "uncertain", uncertain || reason == subscription.GrokReasonRevocationUncertain)
		return grokLogoutOutcome{state: domain.SubscriptionRecovery, reason: reason}, false
	}
	s.logger.InfoContext(ctx, "grok_logout_revocation_confirmed", "phase", grokLogoutPhase, "operation_id", original.ID, "correlation_id", original.ID, "revocation", revocation)
	if s.grokLogoutCrashes(grokLogoutAfterRevoke) {
		return grokLogoutOutcome{}, true
	}
	return grokLogoutOutcome{revoked: true, revocation: revocation, state: domain.SubscriptionSucceeded}, false
}

// claimGrokRevocation writes the durable revocation claim before the request is
// sent. After a crash the claim proves that the request may have been sent, so a
// restart settles RECOVERY_REQUIRED instead of assuming nothing happened.
func (s *Service) claimGrokRevocation(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	bounded, cancel := context.WithTimeout(ctx, grokLoginClaimTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.logout.revocation-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		var cur *domain.ServerSubscriptionOperation
		if st != nil {
			cur = st.ServerOperation
		}
		if cur == nil || cur.ID != o.ID || cur.Epoch != o.Epoch || cur.Action != domain.SubscriptionLogout || cur.State != domain.SubscriptionPreparing || !cur.NativeStarted || cur.ExchangeClaimed ||
			cur.Generation != st.Generation || st.RecoveryRequired || st.Lease != nil || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		cur.ExchangeClaimed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// finishGrokLogout settles the operation. A confirmed revocation publishes the
// disconnected account in one transaction and only afterwards removes the vault
// generations; every other outcome keeps the stored credential. crashed is true
// only for a simulated process death.
func (s *Service) finishGrokLogout(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, out grokLogoutOutcome, shuttingDown bool) (crashed bool, err error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	defer delete(s.subscriptionProgress, o.ID)
	final := out.state
	_, err = s.Store.Mutate(ctx, o.FinishID, "grok.logout.finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
	}{id, o.ID, out.state}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		op := st.ServerOperation
		state := out.state
		// A confirmed revocation settles only when the frozen generation is still
		// the account's and no Worker lease holds a private credential copy: a
		// Worker lease is released only after its owner confirmed that its home
		// holds neither auth.json nor auth.json.lock.
		if state == domain.SubscriptionSucceeded && (!out.revoked || st.Generation != o.Generation || st.Lease != nil || len(st.Members) != 0) {
			state = domain.SubscriptionRecovery
		}
		// The claim was written before any request, so an unconfirmed revocation is
		// recovery, never a failure that would imply nothing was sent.
		if op.ExchangeClaimed && state != domain.SubscriptionSucceeded {
			state = domain.SubscriptionRecovery
		}
		final = state
		var diagnostic *domain.GrokDiagnostic
		switch state {
		case domain.SubscriptionRecovery:
			diagnostic = grokLocalDiagnostic("", domain.GrokLogout, domain.RecoveryRequired)
		case domain.SubscriptionFailed:
			code := out.code
			if code == "" {
				code = domain.Unavailable
			}
			diagnostic = grokLocalDiagnostic("", domain.GrokLogout, code)
		}
		if diagnostic != nil {
			diagnostic.CorrelationID = string(op.ID)
		}
		switch state {
		case domain.SubscriptionRecovery:
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			op.State = domain.SubscriptionRecovery
			op.GrokDiagnostic = diagnostic
		case domain.SubscriptionSucceeded:
			op.NativeStarted = false
			op.State = state
			op.GrokDiagnostic = nil
			st.Pending = nil
			st.AutomaticCreditConsent = nil
			st.Generation = ""
			st.IdentityCommitment = ""
			st.OwnerMachineID = ""
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Validation, a.Catalog, a.Quota = nil, nil, nil
			a.ConfirmedExhausted = false
		default:
			// Nothing was sent: the credential is intact and the account usable.
			op.NativeStarted = false
			op.State = state
			op.GrokDiagnostic = diagnostic
			st.Pending = nil
			if a.Health == domain.AccountRevoked {
				a.Health = domain.AccountReady
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: id}, err
	})
	if err != nil {
		return false, err
	}
	if final != domain.SubscriptionSucceeded {
		s.logger.InfoContext(ctx, "grok_logout_finished", "phase", grokLogoutPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID, "state", final, "reason", out.reason, "shutting_down", shuttingDown)
		return false, nil
	}
	if s.grokLogoutCrashes(grokLogoutAfterPublish) {
		return true, nil
	}
	if err := s.cleanupGrokLogoutCredentials(ctx, id, o); err != nil {
		return false, nil
	}
	s.logger.InfoContext(ctx, "grok_logout_finished", "phase", grokLogoutPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID, "state", final, "reason", out.reason, "shutting_down", shuttingDown)
	return false, nil
}

// cleanupGrokLogoutCredentials removes every protected vault generation of the
// account after the disconnected account was published. A failed removal is not
// an error of the logout: it leaves the retained logout-cleanup recovery shape
// (published disconnected metadata plus RECOVERY_REQUIRED and the original
// operation), which explicit account deletion consumes. The caller holds the
// account gate.
func (s *Service) cleanupGrokLogoutCredentials(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	s.logger.InfoContext(ctx, "grok_logout_cleanup_started", "phase", grokCleanupPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID)
	vault, err := s.secrets()
	if err == nil {
		cleanupCtx, cancel := grokBookkeepingContext(ctx, grokCleanupTimeout)
		err = cleanupSubscriptionReferences(cleanupCtx, vault, id, "")
		cancel()
	}
	if err == nil {
		s.logger.InfoContext(ctx, "grok_logout_cleanup_confirmed", "phase", grokCleanupPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID)
		return nil
	}
	s.logger.WarnContext(ctx, "grok_logout_cleanup_pending", "phase", grokCleanupPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID, "code", domain.SafeError(err).Code)
	bookkeeping, cancel := grokBookkeepingContext(ctx, grokBookkeepingTimeout)
	defer cancel()
	_, markErr := s.Store.Mutate(bookkeeping, domain.NewID(), "grok.logout.cleanup-recovery", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.State != domain.SubscriptionSucceeded || st.Generation != "" || a.Connection != nil {
			return nil, subscriptionDenied()
		}
		op := st.ServerOperation
		op.State = domain.SubscriptionRecovery
		d := grokLocalDiagnostic("", domain.GrokCleanup, domain.RecoveryRequired)
		d.CorrelationID = string(op.ID)
		op.GrokDiagnostic = d
		st.RecoveryRequired = true
		st.Pending = operationPlaceholder(op)
		a.Health = domain.AccountFailed
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	if markErr != nil {
		s.logger.Warn("grok_logout_cleanup_recovery_unconfirmed", "phase", grokCleanupPhase, "operation_id", o.ID, "correlation_id", o.ID, "code", domain.SafeError(markErr).Code)
	}
	return err
}

// grokDeleteLogoutID derives the operation id of the logout that one deletion
// request performs. It is deterministic so a retry of the same deletion request
// recognizes the logout its earlier attempt already ran, even when that attempt
// stopped before the deletion receipt was committed. The value is a canonical
// UUID v7 shaped identifier.
func grokDeleteLogoutID(request domain.ID) domain.ID {
	sum := sha256.Sum256([]byte("delidev.grok.delete-logout\x00" + string(request)))
	var raw uuid.UUID
	copy(raw[:], sum[:16])
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	return domain.ID(raw.String())
}

// logoutGrokBeforeDelete performs the logout that account deletion requires for
// a connected Grok account, and returns the revision to delete. An account with
// no stored credential is returned unchanged, except that a retry of the same
// deletion request continues at the current revision when its own earlier
// logout already advanced it. Deletion waits for the logout and proceeds only
// after it succeeded: a logout that cannot start (a Worker lease still holds the
// account), failed, or ended uncertain blocks the deletion and leaves the
// account as the logout left it.
func (s *Service) logoutGrokBeforeDelete(ctx context.Context, id domain.ID, revision uint64, request domain.ID) (uint64, error) {
	logoutID := grokDeleteLogoutID(request)
	var account domain.Account
	var current uint64
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, a, err := accountFromTx(tx, id, 0)
		if err != nil {
			return err
		}
		account, current = a, r.Revision
		return nil
	})
	if err != nil || account.Type != domain.SubscriptionAccount || account.SubscriptionService != domain.SubscriptionGrok {
		// A missing account and every other service keep the ordinary deletion path.
		return revision, nil
	}
	st := account.Subscription
	if account.Connection == nil && (st == nil || st.Generation == "") {
		if st != nil && st.ServerOperation != nil && st.ServerOperation.ID == logoutID && st.ServerOperation.Action == domain.SubscriptionLogout {
			// The logout of an earlier attempt of this very request published the
			// disconnected account and advanced its revision; continue at the
			// current revision instead of conflicting with the request's old one.
			s.logger.InfoContext(ctx, "grok_delete_logout_resumed", "phase", grokLogoutPhase, "account_id", id, "operation_id", logoutID, "correlation_id", logoutID)
			return current, nil
		}
		return revision, nil
	}
	if current != revision {
		return revision, domain.Fail(domain.Conflict, "The account revision changed.", "Read current account status before confirming deletion again.")
	}
	if st == nil || st.RecoveryRequired || st.Pending != nil || account.Removal != nil {
		s.logger.WarnContext(ctx, "grok_delete_logout_blocked", "phase", grokLogoutPhase, "account_id", id, "reason", "account_busy")
		return revision, protectedAccountDeletion
	}
	// The logout revokes the token and removes the bundle irreversibly, so a
	// deletion the configuration would refuse anyway must be refused first. Only
	// the non-credential references are checked here; the complete deletion
	// validation still runs after the logout to cover races.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return validateDeletionReferences(tx, domain.AccountKind, id) }); err != nil {
		s.logger.WarnContext(ctx, "grok_delete_logout_blocked", "phase", grokLogoutPhase, "account_id", id, "reason", "account_referenced", "code", domain.SafeError(err).Code)
		return revision, err
	}
	req := connect.NewRequest(&pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(logoutID), Id: string(id), ExpectedRevision: current}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT})
	if _, err := s.requestGrokSubscription(ctx, req); err != nil {
		return revision, err
	}
	s.logger.InfoContext(ctx, "grok_delete_logout_started", "phase", grokLogoutPhase, "account_id", id, "operation_id", logoutID, "correlation_id", logoutID)
	runner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	s.runGrokLogout(runner, id)
	deadline := time.Now().Add(grokLogoutDeleteWait)
	for {
		var settled domain.Account
		var next uint64
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			r, a, err := accountFromTx(tx, id, 0)
			settled, next = a, r.Revision
			return err
		})
		if err != nil {
			return revision, err
		}
		o := (*domain.ServerSubscriptionOperation)(nil)
		if settled.Subscription != nil {
			o = settled.Subscription.ServerOperation
		}
		if o == nil || o.ID != logoutID {
			return revision, protectedAccountDeletion
		}
		switch {
		case settled.Subscription.RecoveryRequired && settled.Connection == nil:
			// Published but the credential removal is unconfirmed: the retained
			// logout-cleanup recovery shape is consumed by the deletion itself.
			return next, nil
		case settled.Subscription.RecoveryRequired || o.State == domain.SubscriptionRecovery:
			s.logger.WarnContext(ctx, "grok_delete_logout_blocked", "phase", grokLogoutPhase, "account_id", id, "operation_id", logoutID, "reason", "logout_uncertain")
			return revision, domain.Fail(domain.RecoveryRequired, "The Grok logout could not be confirmed.", "Retry the logout; the account is deleted only after its credential was revoked and removed.")
		case o.State == domain.SubscriptionSucceeded && settled.Connection == nil:
			s.logger.InfoContext(ctx, "grok_delete_logout_completed", "phase", grokLogoutPhase, "account_id", id, "operation_id", logoutID, "correlation_id", logoutID)
			return next, nil
		case o.State == domain.SubscriptionFailed || o.State == domain.SubscriptionCanceled || o.State == domain.SubscriptionExpired:
			return revision, domain.Fail(domain.Unavailable, "The Grok logout did not complete.", "Retry the deletion once the logout can reach the provider.")
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			s.logger.WarnContext(ctx, "grok_delete_logout_blocked", "phase", grokLogoutPhase, "account_id", id, "operation_id", logoutID, "reason", "logout_pending")
			return revision, domain.Fail(domain.Unavailable, "The Grok logout is waiting for the account's execution to exit.", "Retry the deletion after the logout finished.")
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
}
