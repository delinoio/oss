// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The server-owned Grok device-code login (RFC 8628). It runs inside the same
// runner lifecycle as the browser login (runGrokLogin): the same claim of the
// operation, cancellation watcher, settlement and capability switch. This runner
// requests a device authorization, publishes the verification page and user code
// only through the in-memory progress map, then polls the token endpoint at the
// returned interval. The device code, user code and verification address exist
// only in memory and are never logged, stored in a document, put in an event or
// returned in an error.
//
// Exchange claim. Every token request may consume the device code, and a
// request that is not answered definitively leaves that unknown. The runner
// therefore commits the durable exchange claim before every token request and
// clears it only after a definitive answer that proves the code was not consumed
// (authorization_pending, slow_down, access_denied, expired_token or another
// 4xx). The claim of a successful exchange is never cleared. A cancellation,
// shutdown or restart while the claim stands is recovery-required and nothing
// sends the code again; between two polls no claim stands, so a cancellation
// there settles as CANCELED with no credential.

// grokDeviceClaimTimeout bounds the claim and release commits around a poll.
const grokDeviceClaimTimeout = grokLoginClaimTimeout

func (s *Service) grokDeviceClockOrSystem() subscription.GrokDeviceClock {
	if s.grokDeviceClock != nil {
		return s.grokDeviceClock
	}
	return subscription.SystemGrokDeviceClock()
}

// performGrokDeviceLogin runs the device flow and returns the decided outcome.
func (s *Service) performGrokDeviceLogin(ctx context.Context, id domain.ID, original domain.ServerSubscriptionOperation, cancelSeen *atomic.Bool) grokLoginOutcome {
	profile := subscription.DefaultGrokOAuthProfile()
	client, release := s.grokLoginClient()
	defer release()
	flow := &grokFlowState{ctx: ctx, original: original, cancelSeen: cancelSeen}
	endpoints, err := subscription.DiscoverGrokEndpoints(ctx, client, profile)
	if err != nil {
		return flow.failed(err)
	}
	// The keys are read before the device request and before any claim, so a keys
	// failure can neither orphan a device code nor burn one.
	keys, err := subscription.FetchGrokJWKS(ctx, client, endpoints)
	if err != nil {
		return flow.failed(err)
	}
	authorization, err := subscription.RequestGrokDeviceAuthorization(ctx, client, profile, endpoints)
	if err != nil {
		return flow.failed(err)
	}
	defer authorization.Clear()
	if err := ctx.Err(); err != nil {
		return flow.interrupted()
	}
	clock := s.grokDeviceClockOrSystem()
	deadline := subscription.GrokDeviceDeadline(clock.Now(), authorization.ExpiresIn, original.ExpiresAt)
	if err := s.publishGrokDeviceProgress(ctx, id, original, authorization.VerificationURL, authorization.UserCode); err != nil {
		return flow.failed(err)
	}
	s.logger.InfoContext(ctx, "grok_login_waiting", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "method", subscription.GrokLoginDeviceCode, "interval_ms", authorization.Interval.Milliseconds(), "lifetime_ms", deadline.Sub(clock.Now()).Milliseconds())
	tokens, err := subscription.PollGrokDeviceToken(ctx, authorization, subscription.GrokDevicePolling{
		Client: client, Profile: profile, Endpoints: endpoints, Clock: clock, Deadline: deadline,
		Claim: func(ctx context.Context) error {
			if err := s.claimGrokExchange(ctx, id, original); err != nil {
				s.logger.WarnContext(ctx, "grok_login_claim_refused", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !subscriptionIsDenied(err) {
					// An operational read failure keeps its sanitized code instead of
					// looking like a refused claim.
					return domain.SafeError(err)
				}
				return domain.Fail(domain.Conflict, "The Grok login is no longer active.", "Start a new Grok login.")
			}
			flow.claimed.Store(true)
			return nil
		},
		Release: func(ctx context.Context) error {
			if err := s.releaseGrokExchange(ctx, id, original); err != nil {
				s.logger.WarnContext(ctx, "grok_login_claim_release_failed", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "code", domain.SafeError(err).Code)
				return err
			}
			flow.claimed.Store(false)
			return nil
		},
		Observe: func(p subscription.GrokDevicePoll) {
			s.logger.InfoContext(ctx, "grok_login_device_polled", "phase", grokLoginPhase, "operation_id", original.ID, "correlation_id", original.ID, "poll", p.Number, "outcome", p.Outcome, "interval_ms", p.Interval.Milliseconds())
		},
	})
	defer tokens.Clear()
	if err != nil {
		// A request that was sent and not answered definitively keeps its claim and
		// is uncertain; a stop before any claim is a cancellation or an expiry.
		return flow.failed(err)
	}
	// Both methods build the bundle through the one shared function.
	bundle, err := subscription.CompleteGrokLogin(ctx, client, profile, endpoints, keys, tokens, subscription.GrokLoginDeviceCode, "", time.Now())
	if err != nil {
		return flow.failed(err)
	}
	return grokLoginOutcome{state: domain.SubscriptionSucceeded, bundle: bundle}
}

// publishGrokDeviceProgress moves the operation to WAITING and exposes the
// verification address and user code only through the in-memory progress map.
func (s *Service) publishGrokDeviceProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, address, userCode string) error {
	if !subscription.ValidGrokDeviceVerification(address, userCode) {
		s.logger.WarnContext(ctx, "grok_login_device_presentation_rejected", "phase", grokLoginPhase, "operation_id", o.ID, "correlation_id", o.ID, "code", domain.RecoveryRequired)
		return subscriptionDenied()
	}
	return s.publishGrokPresentation(ctx, id, o, subscriptionProgress{URL: address, UserCode: userCode, Until: o.ExpiresAt})
}

// releaseGrokExchange clears the durable exchange claim after a definitive
// answer proved that the device code was not consumed. It runs even when the
// operation was canceled in the meantime, so that cancellation settles as
// CANCELED; a recovery fence or another operation refuses it.
func (s *Service) releaseGrokExchange(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation) error {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), grokDeviceClaimTimeout)
	defer cancel()
	unlock, err := s.lockAccounts(bounded)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(bounded, domain.NewID(), "grok.login.exchange-release", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		cur := (*domain.ServerSubscriptionOperation)(nil)
		if st != nil {
			cur = st.ServerOperation
		}
		if cur == nil || cur.ID != o.ID || cur.Epoch != o.Epoch || cur.State != domain.SubscriptionWaiting || !cur.NativeStarted || !cur.ExchangeClaimed ||
			st.RecoveryRequired || st.Pending == nil || st.Pending.ID != o.ID {
			return nil, subscriptionDenied()
		}
		cur.ExchangeClaimed = false
		_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return struct{}{}, err
	})
	return err
}
