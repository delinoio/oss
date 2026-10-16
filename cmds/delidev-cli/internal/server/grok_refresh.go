// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The server-owned Grok refresh. The server calls the refresh_token grant of the
// fixed xAI profile itself under the account's exclusive server lease, which is
// the claimed ServerOperation (NativeStarted): a Worker lease and a server
// refresh never coexist on one account. One runner owns one operation:
//
//  1. it claims the operation (the lease) and reads the bound bundle;
//  2. it commits the durable refresh claim (ExchangeClaimed) and only then sends
//     the one token request;
//  3. it verifies that the answer names the bound identity and settles through
//     the Finish-equivalent transaction that stages the new generation in the
//     protected vault and publishes it atomically.
//
// A refresh token the provider may have rotated without this server recording
// the successor is unrecoverable, so every outcome after the claim that is not a
// definitive provider rejection (a transport error, a 5xx answer, an unusable
// answer, an identity mismatch, an interruption, a restart) is RECOVERY_REQUIRED
// and the old token is never sent again. A definitive rejection (a 4xx answer,
// `invalid_grant` included) did not consume the token and settles FAILED with the
// account untouched. No token appears in a log, an error, an event or a document.

const (
	// grokRefreshPhase is the phase every structured record of this flow carries.
	grokRefreshPhase = "REFRESH"
	// grokRefreshVerifyTimeout bounds the identity verification that follows a
	// received answer. It is detached from the runner's cancellation, because a
	// received answer may carry the only copy of a rotated refresh token.
	grokRefreshVerifyTimeout = 30 * time.Second
)

// grokRefreshStage names the points of a refresh at which fault injection can
// simulate a process crash. Tests set Service.grokRefreshCrash; production leaves
// it nil.
type grokRefreshStage string

const (
	// grokRefreshAfterClaim is after the durable claim and before the request.
	grokRefreshAfterClaim grokRefreshStage = "after_claim"
	// grokRefreshAfterResponse is after the answer was received and before it was
	// verified or persisted.
	grokRefreshAfterResponse grokRefreshStage = "after_response"
	// grokRefreshAfterPersist is after the account document published the new
	// generation and before the superseded generation was removed.
	grokRefreshAfterPersist grokRefreshStage = "after_persist"
)

func (s *Service) grokRefreshCrashes(stage grokRefreshStage) bool {
	return s.grokRefreshCrash != nil && s.grokRefreshCrash(stage)
}

// grokRefreshCandidate selects a connected Grok account whose server-owned
// refresh was accepted and not yet claimed by a runner.
func grokRefreshCandidate(a domain.Account) bool {
	st := a.Subscription
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService != domain.SubscriptionGrok || st == nil || st.RecoveryRequired || st.Lease != nil || st.ServerOperation == nil || st.Pending == nil || a.Connection == nil || st.Generation == "" {
		return false
	}
	o := st.ServerOperation
	return o.Action == domain.SubscriptionRefresh && o.State == domain.SubscriptionPreparing && !o.NativeStarted && st.Pending.ID == o.ID && st.Pending.Phase == domain.SubscriptionQueued && st.Pending.MachineID == ""
}

// runGrokRefresh owns one server-owned Grok refresh from claim to settlement.
func (s *Service) runGrokRefresh(parent context.Context, id domain.ID) {
	var original domain.ServerSubscriptionOperation
	err := s.Store.Read(parent, func(tx *store.Tx) error {
		_, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return err
		}
		if a.Subscription == nil || a.Subscription.ServerOperation == nil || a.Subscription.Pending == nil {
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
	_, err = s.Store.Mutate(parent, domain.NewID(), "grok.refresh.claim", struct{ Account, Operation domain.ID }{id, original.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || !grokRefreshCandidate(a) || st.ServerOperation.ID != original.ID || st.Pending.ID != original.ID || st.ServerOperation.Epoch != s.subscriptionServerEpoch() {
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
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	unlock()
	if err != nil || !original.NativeStarted {
		return
	}
	s.logger.InfoContext(parent, "grok_refresh_started", "phase", grokRefreshPhase, "account_id", id, "operation_id", original.ID, "correlation_id", original.ID)
	ctx, cancel := context.WithDeadline(parent, original.ExpiresAt)
	out, crashed := s.performGrokRefresh(ctx, id, original)
	cancel()
	defer out.bundle.Clear()
	if crashed {
		// A simulated process death: nothing settles, exactly as after a kill.
		return
	}
	bounded, stop := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), grokLoginSettleTimeout)
	defer stop()
	crashed, err = s.finishGrokRefresh(bounded, id, original, &out, parent.Err() != nil)
	if err != nil && !crashed {
		if e := s.markGrokLoginRecovery(id, original.ID); e != nil {
			s.logger.Warn("grok_refresh_recovery_unconfirmed", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(e).Code)
		}
		s.logger.Warn("grok_refresh_finish_failed", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
	}
}

// grokRefreshUnsent is the outcome of a refresh that stopped before the claim:
// nothing was sent and the account keeps its credential. A passed deadline is an
// expiry and every other stop (a server shutdown, an unusable discovery) a
// failure.
func grokRefreshUnsent(ctx context.Context, original domain.ServerSubscriptionOperation, err error) grokLoginOutcome {
	if !time.Now().Before(original.ExpiresAt) {
		return grokLoginOutcome{state: domain.SubscriptionExpired}
	}
	if err != nil {
		if reason, _ := subscription.GrokOAuthReasonOf(err); reason != "" {
			return grokLoginOutcome{state: domain.SubscriptionFailed, reason: reason, code: grokReasonCode(reason)}
		}
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: subscription.GrokReasonMapping}
		}
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonResponse, code: domain.SafeError(err).Code}
	}
	return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonResponse, code: domain.Unavailable}
}

// performGrokRefresh runs the refresh and returns the decided outcome. crashed
// is true only when fault injection simulated a process death.
func (s *Service) performGrokRefresh(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (out grokLoginOutcome, crashed bool) {
	profile := subscription.DefaultGrokOAuthProfile()
	client, release := s.grokLoginClient()
	defer release()
	endpoints, err := subscription.DiscoverGrokEndpoints(ctx, client, profile)
	if err != nil {
		return grokRefreshUnsent(ctx, original, err), false
	}
	// The keys are read before the claim so a keys failure cannot burn the token.
	keys, err := subscription.FetchGrokJWKS(ctx, client, endpoints)
	if err != nil {
		return grokRefreshUnsent(ctx, original, err), false
	}
	bound, err := s.openGrokBoundBundle(ctx, id, original)
	if err != nil {
		s.logger.WarnContext(ctx, "grok_refresh_bundle_unavailable", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		return grokRefreshUnsent(ctx, original, err), false
	}
	defer bound.Clear()
	if err := s.claimGrokRefreshExchange(ctx, id, original); err != nil {
		s.logger.WarnContext(ctx, "grok_refresh_claim_refused", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
		if ctx.Err() != nil {
			return grokRefreshUnsent(ctx, original, nil), false
		}
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: subscription.GrokReasonResponse, code: domain.Conflict}, false
	}
	s.logger.InfoContext(ctx, "grok_refresh_claimed", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID)
	if s.grokRefreshCrashes(grokRefreshAfterClaim) {
		return grokLoginOutcome{}, true
	}
	// From here on the request may reach the provider: only a definitive
	// rejection proves the refresh token was not consumed.
	tokens, err := subscription.RefreshGrokTokens(ctx, client, profile, endpoints, bound.RefreshToken())
	defer tokens.Clear()
	if err != nil {
		reason, uncertain := subscription.GrokOAuthReasonOf(err)
		if uncertain || reason == "" || ctx.Err() != nil && !definitiveGrokRefresh(reason) {
			if reason == "" {
				reason = subscription.GrokReasonTokenUncertain
			}
			s.logger.WarnContext(ctx, "grok_refresh_outcome_uncertain", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "reason", reason)
			return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: reason}, false
		}
		s.logger.WarnContext(ctx, "grok_refresh_rejected", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "reason", reason)
		return grokLoginOutcome{state: domain.SubscriptionFailed, reason: reason, code: domain.Unauthenticated}, false
	}
	if s.grokRefreshCrashes(grokRefreshAfterResponse) {
		return grokLoginOutcome{}, true
	}
	// The answer may hold the only copy of a rotated refresh token, so its
	// verification is not stopped by a shutdown or a passed deadline.
	verify, stop := context.WithTimeout(context.WithoutCancel(ctx), grokRefreshVerifyTimeout)
	defer stop()
	next, err := subscription.CompleteGrokRefresh(verify, client, profile, endpoints, keys, bound, tokens, time.Now())
	if err != nil {
		reason, _ := subscription.GrokOAuthReasonOf(err)
		if reason == "" {
			reason = subscription.GrokReasonMapping
		}
		s.logger.WarnContext(ctx, "grok_refresh_verification_failed", "phase", grokRefreshPhase, "operation_id", original.ID, "correlation_id", original.ID, "reason", reason)
		return grokLoginOutcome{state: domain.SubscriptionRecovery, reason: reason}, false
	}
	return grokLoginOutcome{state: domain.SubscriptionSucceeded, bundle: next}, false
}

// definitiveGrokRefresh reports whether the reason is a provider rejection that
// proves the refresh token was not consumed.
func definitiveGrokRefresh(reason subscription.GrokOAuthReason) bool {
	return reason == subscription.GrokReasonInvalidGrant || reason == subscription.GrokReasonTokenRejected
}

// openGrokBoundBundle reads and opens the bundle of the generation the claim
// froze, against this server, the account and that generation.
func (s *Service) openGrokBoundBundle(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) (subscription.GrokBundle, error) {
	if o.Generation == "" {
		return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
	}
	vault, err := s.secrets()
	if err != nil {
		return subscription.GrokBundle{}, err
	}
	raw, err := vault.Get(ctx, credentials.Ref{Owner: id, ID: o.Generation, Purpose: credentials.AccountLogin})
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			// The document references a generation whose vault object is gone: the
			// account is unusable for every later execution and refresh, which is a
			// local credential loss that needs the recovery fence, not a retryable
			// provider failure.
			return subscription.GrokBundle{}, subscription.GrokBundleInvalid()
		}
		return subscription.GrokBundle{}, err
	}
	defer clear(raw)
	return subscription.OpenGrokBundle(raw, s.Identity.ServerID, id, o.Generation)
}

// claimGrokRefreshExchange writes the durable refresh claim. The token request
// may be sent only after this commit returned; it is never written again or
// cleared.
func (s *Service) claimGrokRefreshExchange(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	bounded, cancel := context.WithTimeout(ctx, grokLoginClaimTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.refresh.exchange-claim", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		var cur *domain.ServerSubscriptionOperation
		if st != nil {
			cur = st.ServerOperation
		}
		if cur == nil || cur.ID != o.ID || cur.Epoch != o.Epoch || cur.Action != domain.SubscriptionRefresh || cur.State != domain.SubscriptionPreparing || !cur.NativeStarted || cur.ExchangeClaimed || !time.Now().Before(cur.ExpiresAt) ||
			cur.Generation != st.Generation || st.RecoveryRequired || st.Lease != nil || st.Pending == nil || st.Pending.ID != o.ID || st.Pending.Canceled || subscriptionActorValid(tx, cur.Actor) != nil {
			return nil, subscriptionDenied()
		}
		cur.ExchangeClaimed = true
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}

// finishGrokRefresh settles the operation in the Finish-equivalent server
// transaction: it stages the sealed successor bundle under the protected vault,
// then publishes the generation, or records the terminal state. The identity
// commitment and the frozen generation must still match, and a refresh whose
// answer was received is never reported as anything but a publication or
// RECOVERY_REQUIRED. crashed is true only for a simulated process death.
func (s *Service) finishGrokRefresh(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, out *grokLoginOutcome, shuttingDown bool) (crashed bool, err error) {
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	success := out.state == domain.SubscriptionSucceeded
	if success && s.grokIdentityCommitment(out.bundle) == "" {
		success, out.state, out.reason = false, domain.SubscriptionRecovery, subscription.GrokReasonMapping
	}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, a, e := grokSubscriptionAccount(tx, id, 0)
		if e != nil {
			return e
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.ServerOperation.Epoch != o.Epoch || !st.ServerOperation.NativeStarted || st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return subscriptionDenied()
		}
		if success && (st.Generation != o.Generation || s.grokIdentityCommitment(out.bundle) != st.IdentityCommitment) {
			success, out.state, out.reason = false, domain.SubscriptionRecovery, subscription.GrokReasonIdentity
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	var vault accountSecrets
	staged := false
	if success {
		vault, err = s.secrets()
		if err != nil {
			return false, err
		}
		sealed, e := subscription.SealGrokBundle(s.Identity.ServerID, id, o.FinishID, out.bundle)
		if e != nil {
			return false, e
		}
		// Set before the write: a write that reports an error may still have left
		// the staged generation behind.
		staged = true
		_, e = vault.Put(ctx, credentials.Ref{Owner: id, ID: o.FinishID, Purpose: credentials.AccountLogin}, sealed)
		clear(sealed)
		if e != nil {
			s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
			return false, e
		}
	}
	final := out.state
	_, err = s.Store.Mutate(ctx, o.FinishID, "grok.refresh.finish", struct {
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
		// The claim was written before any request, so an answer that could not be
		// published is recovery, never a failure that would invite a resend.
		if state == domain.SubscriptionSucceeded && (!success || st.Generation != o.Generation) {
			state = domain.SubscriptionRecovery
		}
		if op.ExchangeClaimed && state != domain.SubscriptionSucceeded && state != domain.SubscriptionFailed {
			state = domain.SubscriptionRecovery
		}
		final = state
		var diagnostic *domain.GrokDiagnostic
		switch state {
		case domain.SubscriptionRecovery:
			diagnostic = grokLocalDiagnostic("", domain.GrokRefresh, domain.RecoveryRequired)
		case domain.SubscriptionFailed:
			code := out.code
			if code == "" {
				code = grokReasonCode(out.reason)
			}
			diagnostic = grokLocalDiagnostic("", domain.GrokRefresh, code)
		}
		if diagnostic != nil {
			diagnostic.CorrelationID = string(op.ID)
		}
		if state == domain.SubscriptionRecovery {
			st.RecoveryRequired = true
			a.Health = domain.AccountFailed
			op.State = domain.SubscriptionRecovery
			op.GrokDiagnostic = diagnostic
		} else {
			op.NativeStarted = false
			op.State = state
			op.GrokDiagnostic = diagnostic
			st.Pending = nil
			if state == domain.SubscriptionSucceeded {
				op.Generation = o.FinishID
				st.Generation = o.FinishID
				if a.Health != domain.AccountRevoked {
					a.Health = domain.AccountReady
				}
			}
		}
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return accountReceipt{ID: id}, err
	})
	if err != nil {
		if staged {
			s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
		}
		return false, err
	}
	out.state = final
	if staged && final != domain.SubscriptionSucceeded {
		s.retireUnpublishedGrokGeneration(ctx, vault, id, o.ID, o.FinishID)
	}
	if final == domain.SubscriptionSucceeded {
		if s.grokRefreshCrashes(grokRefreshAfterPersist) {
			return true, nil
		}
		// The superseded generation is retained: only the published generation
		// authorizes use, and only explicit logout or deletion removes the rest.
		s.logger.InfoContext(ctx, "server_credential_retained", "account_id", id, "operation_id", o.ID, "action", o.Action, "state", final)
	}
	s.logger.InfoContext(ctx, "grok_refresh_finished", "phase", grokRefreshPhase, "account_id", id, "operation_id", o.ID, "correlation_id", o.ID, "state", final, "reason", out.reason, "shutting_down", shuttingDown)
	return false, nil
}
