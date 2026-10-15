// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// Server-owned Grok logout. The Go server revokes the account's refresh token at
// the fixed xAI revocation endpoint itself while it holds the account's
// exclusive server lease (ServerSubscriptionOperation.NativeStarted with a
// claimed pending operation and no Worker lease). The sequence is fixed:
//
//  1. accept: RequestSubscription marks the account logging out (health revoked,
//     queued executions canceled); a Take for new execution is refused with a
//     typed unavailable problem from this point on;
//  2. wait: the operation is claimed only while no Worker lease exists. A Worker
//     lease is released only by a Finish that confirmed the original execution
//     exit and the removal of auth.json and auth.json.lock from the Worker's
//     private home, so a claimed logout implies every Worker home is clean;
//  3. read: the stored generation is opened against this server and account;
//  4. claim: GrokRevocationClaimed is written durably, in its own transaction;
//  5. send: exactly one revocation request, with no retry;
//  6. confirm: a token the provider acknowledged (HTTP 200) is recorded durably as
//     GrokRevocationConfirmed together with SubscriptionState.RevokedGeneration;
//  7. clean: only now every protected generation is deleted from the vault and
//     the account is published as disconnected.
//
// A definite refusal, a server error, a timeout or any crash after step 4 ends in
// RECOVERY_REQUIRED with the protected bundle untouched, and the claimed token is
// never sent again. A cleanup that fails after step 6 keeps the account fenced
// and is retried (the token is never revoked twice): the retry only completes
// step 7.

// Log phases and steps of a Grok logout.
const (
	grokLogPhaseLogout  grokLoginPhase = "LOGOUT"
	grokLogPhaseCleanup grokLoginPhase = "CLEANUP"

	grokStepRevoke grokLoginStep = "revoke"
)

// grokRefreshAfterFailedLogout is the typed problem of a refresh requested while
// the account is revoked by a logout that has not settled.
func grokRefreshAfterFailedLogout() *domain.Error {
	return domain.Fail(domain.Conflict, "This Grok account is signing out.", "Request the sign-out again; a refresh is refused until a sign-out has settled.")
}

// grokLogoutKind classifies an interrupted Grok logout by what it proved.
type grokLogoutKind int

const (
	grokLogoutNone grokLogoutKind = iota
	// grokLogoutUnclaimed: no revocation was ever claimed or sent, so the stored
	// credential is intact and the operation settles as a failed logout.
	grokLogoutUnclaimed
	// grokLogoutRevoked: the provider revoked the token; only the protected
	// cleanup remains and it is safe to repeat.
	grokLogoutRevoked
)

func grokLogoutRecoveryKind(a domain.Account) grokLogoutKind {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || a.Removal != nil || a.Connection == nil || st == nil || st.Generation == "" || !st.RecoveryRequired {
		return grokLogoutNone
	}
	o := st.ServerOperation
	if o == nil || o.Action != domain.SubscriptionLogout || o.State != domain.SubscriptionRecovery || st.Pending == nil || st.Pending.ID != o.ID {
		return grokLogoutNone
	}
	switch o.GrokRevocation {
	case "":
		// An unclaimed logout sent nothing and touches no lease, so it settles even
		// while a Worker lease exists: a restart fences a logout that was still
		// waiting for that lease, and the lease's own Finish is refused while the
		// fence stands, so the fence has to clear first.
		return grokLogoutUnclaimed
	case domain.GrokRevocationConfirmed:
		// The cleanup deletes the bundle, which no lease may still hold.
		if st.RevokedGeneration == st.Generation && st.Lease == nil {
			return grokLogoutRevoked
		}
	}
	return grokLogoutNone
}

// grokLogoutOutcome is the settled result of the revocation steps.
type grokLogoutOutcome struct {
	state domain.SubscriptionLoginState
	code  domain.Code
	step  grokLoginStep
	// claimed reports that the revocation claim was persisted.
	claimed bool
	// revoked reports that the provider revoked the token or had already refused it.
	revoked bool
}

// runGrokLogout owns one server-owned Grok logout from its lease to its settled
// outcome. It never sends a revocation request without a persisted claim and
// never sends one twice.
func (s *Service) runGrokLogout(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil || a.Subscription.ServerOperation.Action != domain.SubscriptionLogout {
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
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.server.logout-lease", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		// A Worker lease keeps the logout waiting: the lease is released only by a
		// Finish that confirmed execution exit and private-home cleanup.
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != original.ID || st.ServerOperation.State != domain.SubscriptionPreparing || st.ServerOperation.NativeStarted || st.Pending == nil || st.Pending.ID != original.ID || st.Pending.Phase != domain.SubscriptionQueued || st.ServerObservationActive() || st.Lease != nil || st.RecoveryRequired || st.ServerOperation.Epoch != s.subscriptionServerEpoch() || st.ServerOperation.Action != domain.SubscriptionLogout || a.Connection == nil || st.Generation == "" || st.IdentityCommitment == "" {
			return nil, subscriptionDenied()
		}
		o := st.ServerOperation
		o.NativeStarted = true
		o.Generation = st.Generation
		// An accepted logout is never abandoned because it waited: its window
		// begins when it is claimed, never while it waits for the original execution.
		o.ExpiresAt = time.Now().UTC().Add(15 * time.Minute)
		st.Pending.Phase = domain.SubscriptionClaimed
		original = *o
		claimed = true
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
		if claimed {
			// The lease transaction ran but its outcome is unknown: the durable
			// operation decides whether it is fenced for the scanner.
			s.fenceUnsettledGrokOperation(parent, id, original)
		}
		return
	}
	if !claimed {
		return
	}
	s.logGrok(parent, slog.LevelInfo, "grok_logout_started", original, grokStepClaim, "account_id", id)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	outcome := s.grokLogoutSteps(ctx, id, original)
	cancel()
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.WithoutCancel(parent), domain.Principal{Type: domain.OwnerDevice}), 2*grokCleanupTimeout)
	defer stop()
	if err := s.finishGrokLogout(bounded, id, original, outcome); err != nil {
		s.logGrok(bounded, slog.LevelWarn, "grok_logout_finish_failed", original, grokStepFinish, "code", domain.SafeError(err).Code)
		s.fenceUnsettledGrokOperation(bounded, id, original)
	}
}

// grokLogoutSteps reads the stored credential, claims the revocation, sends the
// single revocation request and classifies the result.
func (s *Service) grokLogoutSteps(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (outcome grokLogoutOutcome) {
	fail := func(code domain.Code, step grokLoginStep) grokLogoutOutcome {
		return grokLogoutOutcome{state: domain.SubscriptionFailed, code: code, step: step, claimed: outcome.claimed}
	}
	// uncertain settles a claimed revocation that is not proven.
	uncertain := func(step grokLoginStep) grokLogoutOutcome {
		return grokLogoutOutcome{state: domain.SubscriptionRecovery, code: domain.RecoveryRequired, step: step, claimed: true}
	}
	// The vault is opened and read under the account gate, like every other
	// credential path, and the gate is released before any network work.
	stored, err := s.readGrokGeneration(ctx, id, original.Generation)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_logout_vault_read_failed", original, grokStepVault, "code", domain.SafeError(err).Code)
		return fail(domain.Unavailable, grokStepVault)
	}
	current, err := subscription.OpenGrokBundle(stored, s.Identity.ServerID, id, original.Generation)
	clear(stored)
	if err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_logout_bundle_invalid", original, grokStepVault)
		return fail(domain.Internal, grokStepVault)
	}
	defer current.Clear()
	if err := s.claimGrokRevocation(ctx, id, original, s.grokIdentityCommitment(current)); err != nil {
		s.logGrok(ctx, slog.LevelWarn, "grok_logout_claim_failed", original, grokStepClaim, "code", domain.SafeError(err).Code)
		return fail(domain.SafeError(err).Code, grokStepClaim)
	}
	outcome.claimed = true
	s.logGrok(ctx, slog.LevelInfo, "grok_logout_revocation_claimed", original, grokStepClaim)
	_ = s.grokFault(grokFaultAfterRevokeClaim)
	transport, closeTransport := s.grokOAuthRoundTripper()
	defer closeTransport()
	client := subscription.GrokOAuthClient{Transport: transport}
	token := current.RefreshTokenBytes()
	result, err := client.Revoke(ctx, token)
	clear(token)
	if err != nil {
		reason, _ := subscription.GrokOAuthFailure(err)
		// A definite refusal and an unknown outcome both leave the account fenced:
		// the bundle is never deleted without a proven revocation.
		s.logGrok(ctx, slog.LevelWarn, "grok_logout_revocation_unproven", original, grokStepRevoke, "reason", reason)
		return uncertain(grokStepRevoke)
	}
	_ = s.grokFault(grokFaultAfterRevoke)
	s.logGrok(ctx, slog.LevelInfo, "grok_logout_revoked", original, grokStepRevoke, "result", result)
	return grokLogoutOutcome{state: domain.SubscriptionSucceeded, step: grokStepRevoke, claimed: true, revoked: true}
}

// claimGrokRevocation persists the revocation claim before the request is sent.
// commitment must equal the account's stored identity commitment, so a stored
// generation that does not belong to the bound identity is never revoked.
func (s *Service) claimGrokRevocation(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, commitment string) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.logout-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		current := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Lease != nil || current == nil || current.ID != o.ID || current.Epoch != o.Epoch || current.Action != domain.SubscriptionLogout || current.State != domain.SubscriptionPreparing || !current.NativeStarted || current.GrokRevocation != "" || a.Connection == nil || st.Generation != o.Generation {
			return nil, subscriptionDenied()
		}
		if st.IdentityCommitment == "" || commitment == "" || st.IdentityCommitment != commitment {
			return nil, domain.Fail(domain.Conflict, "The stored Grok credential does not match the account identity.", "Sign in to the account again.")
		}
		current.GrokRevocation = domain.GrokRevocationClaimed
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	// A claim whose commit acknowledgement is lost reports an error although the
	// claim is durable; settlement re-reads the claim instead of trusting this
	// error.
	return s.grokFault(grokFaultAfterRevokeClaimCommit)
}

// grokLogoutDiagnostic builds the durable diagnostic of a failed or uncertain
// logout.
func grokLogoutDiagnostic(o domain.ServerSubscriptionOperation, state domain.SubscriptionLoginState, code domain.Code) *domain.GrokDiagnostic {
	if state != domain.SubscriptionFailed && state != domain.SubscriptionRecovery {
		return nil
	}
	return &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticLogout, Code: code, CorrelationID: o.ID}
}

// finishGrokLogout writes the settled outcome. Only a proven revocation proceeds
// to the protected cleanup; everything else keeps the bundle and its references.
func (s *Service) finishGrokLogout(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, outcome grokLogoutOutcome) error {
	if !outcome.revoked {
		return s.settleGrokLogoutWithoutRevocation(ctx, id, o, outcome)
	}
	if err := s.recordGrokRevocation(ctx, id, o); err != nil {
		return err
	}
	_ = s.grokFault(grokFaultAfterRevocationRecorded)
	return s.completeGrokLogoutCleanup(ctx, id, o)
}

// settleGrokLogoutWithoutRevocation settles a logout that did not prove its
// revocation. A claimed revocation stays in recovery with the bundle untouched;
// an unclaimed failure sent nothing, so the account stays connected and a new
// logout may be requested.
func (s *Service) settleGrokLogoutWithoutRevocation(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, outcome grokLogoutOutcome) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// settled is the outcome the transaction wrote. It starts as the caller's and is
	// replaced when the durable claim reconciles a failure into uncertainty, so the
	// record logged below describes what was stored.
	settled := outcome
	_, err = s.Store.Mutate(ctx, o.FinishID, "grok.server.logout-finish", struct {
		Account, Operation domain.ID
		State              domain.SubscriptionLoginState
		Code               domain.Code
	}{id, o.ID, outcome.state, outcome.code}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || !st.ServerOperation.NativeStarted || st.Generation != o.Generation || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		// The durable claim decides, not the error that reported the claim: a claim
		// whose commit acknowledgement was lost is still a claim, and the token may
		// be sent by a later logout once the pending operation is cleared. Such a
		// failure is therefore uncertain, never a plain failed logout.
		settled = outcome
		if st.ServerOperation.GrokRevocation != "" && settled.state != domain.SubscriptionRecovery {
			settled.state, settled.code, settled.claimed = domain.SubscriptionRecovery, domain.RecoveryRequired, true
		}
		st.ServerOperation.GrokDiagnostic = grokLogoutDiagnostic(o, settled.state, settled.code)
		if settled.state == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			st.ServerOperation.State = domain.SubscriptionRecovery
		} else {
			st.ServerOperation.NativeStarted = false
			st.ServerOperation.State = settled.state
			st.Pending = nil
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: id}, nil
	})
	if err != nil {
		return err
	}
	delete(s.subscriptionProgress, o.ID)
	level := slog.LevelWarn
	s.logGrok(ctx, level, "grok_logout_finished", o, grokStepFinish, "state", settled.state, "code", settled.code, "claimed", settled.claimed)
	return nil
}

// recordGrokRevocation durably records that the provider revoked the token. It is
// the single fact that lets the protected cleanup delete the bundle.
func (s *Service) recordGrokRevocation(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.logout-revoked", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		current := st.ServerOperation
		if st.RecoveryRequired || st.Pending == nil || st.Lease != nil || current == nil || current.ID != o.ID || current.Action != domain.SubscriptionLogout || !current.NativeStarted || current.GrokRevocation != domain.GrokRevocationClaimed || st.Generation != o.Generation {
			return nil, subscriptionDenied()
		}
		current.GrokRevocation = domain.GrokRevocationConfirmed
		current.GrokDiagnostic = nil
		st.RevokedGeneration = st.Generation
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

// completeGrokLogoutCleanup deletes every protected generation of a revoked
// account from the vault and then publishes the account as disconnected. It runs
// only for a durably confirmed revocation of the account's current generation,
// holds no Worker lease, and is safe to repeat.
func (s *Service) completeGrokLogoutCleanup(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Action != domain.SubscriptionLogout || st.ServerOperation.GrokRevocation != domain.GrokRevocationConfirmed || st.RevokedGeneration == "" || st.RevokedGeneration != st.Generation || st.Lease != nil || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
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
	// No generation is kept: the credential is revoked, so every reference of the
	// account (current, staged or superseded) is removed before the metadata
	// stops pointing at it.
	cleanup, cancel := grokBookkeepingContext(ctx, grokCleanupTimeout)
	err = cleanupSubscriptionReferences(cleanup, vault, id, "")
	cancel()
	if err != nil {
		s.logGrokCleanup(ctx, slog.LevelWarn, "grok_logout_cleanup_pending", o, "code", domain.SafeError(err).Code)
		return err
	}
	_ = s.grokFault(grokFaultAfterLogoutCleanup)
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.logout-disconnect", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.GrokRevocation != domain.GrokRevocationConfirmed || st.RevokedGeneration != st.Generation || st.Lease != nil || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.NativeStarted = false
		st.ServerOperation.State = domain.SubscriptionSucceeded
		st.ServerOperation.GrokDiagnostic = nil
		st.Pending = nil
		st.RecoveryRequired = false
		st.Generation, st.IdentityCommitment, st.OwnerMachineID, st.RevokedGeneration = "", "", "", ""
		st.AutomaticCreditConsent = nil
		a.Connection = nil
		a.Health = domain.AccountDisconnected
		a.Validation, a.Catalog, a.Quota = nil, nil, nil
		a.ConfirmedExhausted = false
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return accountReceipt{ID: id}, nil
	})
	if err == nil {
		// A disconnect whose commit acknowledgement is lost reports an error although
		// the account is already disconnected; the caller re-reads the operation.
		err = s.grokFault(grokFaultAfterLogoutDisconnect)
	}
	if err != nil {
		s.logGrokCleanup(ctx, slog.LevelWarn, "grok_logout_disconnect_pending", o, "code", domain.SafeError(err).Code)
		return err
	}
	delete(s.subscriptionProgress, o.ID)
	s.logGrokCleanup(ctx, slog.LevelInfo, "grok_logout_cleanup_confirmed", o)
	s.logGrok(ctx, slog.LevelInfo, "grok_logout_finished", o, grokStepFinish, "state", domain.SubscriptionSucceeded, "claimed", true)
	return nil
}

// logGrokCleanup writes one CLEANUP-phase record of a Grok logout.
func (s *Service) logGrokCleanup(ctx context.Context, level slog.Level, message string, o domain.ServerSubscriptionOperation, attrs ...any) {
	base := []any{"phase", grokLogPhaseCleanup, "operation_id", o.ID, "correlation_id", o.ID, "step", grokStepCleanup}
	s.logger.Log(ctx, level, message, append(base, attrs...)...)
}

// recoverGrokLogout settles an interrupted Grok logout. An unclaimed logout sent
// nothing, so it settles as a failed logout with the credential intact; a logout
// whose revocation was confirmed only repeats the protected cleanup. A claimed but
// unproven revocation is never resolved or resent automatically.
func (s *Service) recoverGrokLogout(ctx context.Context, id, operation domain.ID) error {
	bounded, cancel := context.WithTimeout(ctx, 2*grokCleanupTimeout)
	defer cancel()
	var kind grokLogoutKind
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		kind = grokLogoutRecoveryKind(a)
		if kind == grokLogoutNone || a.Subscription.ServerOperation.ID != operation {
			return subscriptionDenied()
		}
		original = *a.Subscription.ServerOperation
		return nil
	})
	if err != nil {
		return err
	}
	if kind == grokLogoutRevoked {
		return s.completeGrokLogoutCleanup(bounded, id, original)
	}
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.server.logout-recover", struct{ Account, Operation domain.ID }{id, operation}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		if grokLogoutRecoveryKind(a) != grokLogoutUnclaimed || a.Subscription.ServerOperation.ID != operation {
			return nil, subscriptionDenied()
		}
		st := a.Subscription
		o := st.ServerOperation
		o.State = domain.SubscriptionFailed
		o.NativeStarted = false
		o.GrokDiagnostic = &domain.GrokDiagnostic{Phase: domain.GrokDiagnosticLogout, Code: domain.Unavailable, CorrelationID: o.ID}
		st.Pending = nil
		// The restart fenced the account before anything was sent. The credential
		// is intact and the accepted logout keeps readiness revoked until a new
		// logout settles.
		st.RecoveryRequired, a.Health = false, domain.AccountRevoked
		if st.Lease != nil && st.Lease.Epoch != s.subscriptionServerEpoch() {
			// A lease granted before the restart is fenced too and stale: only its
			// original Worker's recovery can release it, and that recovery needs the
			// fence to stand. The logout settles, the fence stays for the lease.
			st.RecoveryRequired, a.Health = true, domain.AccountFailed
		}
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		delete(s.subscriptionProgress, operation)
		s.logger.InfoContext(ctx, "grok_logout_recovered", "phase", grokLogPhaseLogout, "operation_id", operation, "correlation_id", operation, "claimed", false)
	}
	return err
}
