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

// Server-owned Grok device login (RFC 8628). RequestSubscription with
// device_code=true runs this driver instead of the loopback browser flow; the
// two methods never substitute for each other.
//
// The flow: discovery, one device authorization request, transient publication
// of the verification URL and user code through progress, then a poll loop on
// the token endpoint. The poll honors the provider's interval, adds
// GrokDeviceSlowDownStep on every slow_down, continues on authorization_pending
// and ends on tokens, access_denied, expired_token or the local deadline
// (the minimum of expires_in and the 15-minute operation window).
//
// Claim rule: the exchange claim is persisted once, immediately before the first
// token poll is sent. Every poll can redeem the device code, so from the claim
// on a poll whose outcome is unknown (transport error, cancellation or shutdown
// during the request, timeout, unreadable body, unexpected status or malformed
// success) ends in RECOVERY_REQUIRED and the device code is never sent again. A
// definitive pending, slow_down, denied, expired or rejected answer is not
// uncertain. The device code, user code and verification URL live only in
// memory and in the transient progress response; a restart cannot resume
// polling, so a claimed login stays in recovery and an unclaimed one settles as
// a failed login (recoverGrokLogin).

// grokClock is the time source of the device poll loop. Tests replace it with
// a virtual clock so interval and deadline behavior needs no real waiting.
type grokClock interface {
	Now() time.Time
	// Wait blocks for d or until ctx ends, returning the context error then.
	Wait(ctx context.Context, d time.Duration) error
}

type realGrokClock struct{}

func (realGrokClock) Now() time.Time { return time.Now() }

func (realGrokClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) grokDeviceClock() grokClock {
	if s.grokClock != nil {
		return s.grokClock
	}
	return realGrokClock{}
}

// grokDeviceLoginSteps runs discovery, the device authorization, the poll loop
// and the identity checks of one device login.
func (s *Service) grokDeviceLoginSteps(ctx, parent context.Context, id domain.ID, original domain.ServerSubscriptionOperation) (outcome grokOutcome) {
	fail := func(state domain.SubscriptionLoginState, code domain.Code, step grokLoginStep) grokOutcome {
		return grokOutcome{state: state, code: code, step: step, claimed: outcome.claimed}
	}
	// interrupted classifies the end of a wait: server shutdown and explicit
	// cancellation are canceled, the operation window is expired.
	interrupted := func(step grokLoginStep) grokOutcome {
		if parent.Err() == nil && !time.Now().Before(original.ExpiresAt) {
			return fail(domain.SubscriptionExpired, domain.Canceled, step)
		}
		return fail(domain.SubscriptionCanceled, domain.Canceled, step)
	}
	transport, closeTransport := s.grokOAuthRoundTripper()
	defer closeTransport()
	client := subscription.GrokOAuthClient{Transport: transport}
	endpoints, err := client.Discover(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepDiscovery)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_login_discovery_failed", original, grokStepDiscovery)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepDiscovery)
	}
	grant, err := client.RequestDeviceAuthorization(ctx, endpoints)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepDevice)
		}
		reason, _ := subscription.GrokOAuthFailure(err)
		s.logGrok(ctx, slog.LevelWarn, "grok_device_authorization_failed", original, grokStepDevice, "reason", reason)
		return fail(domain.SubscriptionFailed, domain.Unavailable, grokStepDevice)
	}
	defer grant.Clear()
	clock := s.grokDeviceClock()
	// The effective window is the shorter of the provider's expires_in and what
	// remains of the operation window, measured on the poll clock.
	window := min(grant.ExpiresIn, time.Until(original.ExpiresAt))
	deadline := clock.Now().Add(window)
	if err := s.publishGrokDeviceProgress(ctx, id, original, grant); err != nil {
		if ctx.Err() != nil {
			return interrupted(grokStepPublish)
		}
		s.logGrok(ctx, slog.LevelWarn, "grok_login_publish_failed", original, grokStepPublish, "code", domain.SafeError(err).Code)
		return fail(domain.SubscriptionFailed, domain.Internal, grokStepPublish)
	}
	interval := grant.Interval
	polls := 0
	for {
		// Never poll faster than the effective interval, and never poll once the
		// device code can no longer be valid.
		remaining := deadline.Sub(clock.Now())
		if remaining <= 0 || interval >= remaining {
			if remaining > 0 {
				if err := clock.Wait(ctx, remaining); err != nil {
					return interrupted(grokStepPoll)
				}
			}
			s.logGrok(ctx, slog.LevelInfo, "grok_device_expired", original, grokStepPoll, "reason", "local_deadline", "polls", polls)
			return fail(domain.SubscriptionExpired, domain.Canceled, grokStepPoll)
		}
		if err := clock.Wait(ctx, interval); err != nil {
			return interrupted(grokStepPoll)
		}
		if !outcome.claimed {
			if err := s.claimGrokExchange(ctx, id, original, domain.SubscriptionDeviceCode); err != nil {
				if ctx.Err() != nil {
					return interrupted(grokStepExchange)
				}
				s.logGrok(ctx, slog.LevelWarn, "grok_login_claim_failed", original, grokStepExchange, "code", domain.SafeError(err).Code)
				return fail(domain.SubscriptionFailed, domain.SafeError(err).Code, grokStepExchange)
			}
			outcome.claimed = true
			s.logGrok(ctx, slog.LevelInfo, "grok_login_exchange_claimed", original, grokStepExchange)
		}
		polls++
		tokens, result, err := client.PollDeviceToken(ctx, endpoints, grant.DeviceCode)
		if err != nil {
			reason, _ := subscription.GrokOAuthFailure(err)
			if reason == subscription.GrokReasonTokenRejected {
				s.logGrok(ctx, slog.LevelWarn, "grok_device_poll_rejected", original, grokStepPoll, "polls", polls)
				return fail(domain.SubscriptionFailed, domain.PermissionDenied, grokStepPoll)
			}
			// An uncertain poll may have redeemed the device code. It is never resent.
			s.logGrok(ctx, slog.LevelWarn, "grok_device_poll_uncertain", original, grokStepPoll, "reason", reason, "polls", polls)
			return fail(domain.SubscriptionRecovery, domain.RecoveryRequired, grokStepPoll)
		}
		switch result {
		case subscription.GrokDevicePending:
			s.logGrok(ctx, slog.LevelDebug, "grok_device_poll_pending", original, grokStepPoll, "polls", polls, "interval_ms", interval.Milliseconds())
		case subscription.GrokDeviceSlowDown:
			interval += subscription.GrokDeviceSlowDownStep
			s.logGrok(ctx, slog.LevelInfo, "grok_device_poll_slow_down", original, grokStepPoll, "polls", polls, "interval_ms", interval.Milliseconds())
		case subscription.GrokDeviceDenied:
			s.logGrok(ctx, slog.LevelInfo, "grok_login_denied", original, grokStepPoll, "polls", polls)
			return fail(domain.SubscriptionFailed, domain.PermissionDenied, grokStepPoll)
		case subscription.GrokDeviceExpired:
			s.logGrok(ctx, slog.LevelInfo, "grok_device_expired", original, grokStepPoll, "reason", "expired_token", "polls", polls)
			return fail(domain.SubscriptionExpired, domain.Canceled, grokStepPoll)
		case subscription.GrokDeviceTokens:
			s.logGrok(ctx, slog.LevelInfo, "grok_device_poll_tokens", original, grokStepPoll, "polls", polls)
			return s.grokBundleOutcome(ctx, client, endpoints, tokens, "", true, original)
		default:
			return fail(domain.SubscriptionRecovery, domain.RecoveryRequired, grokStepPoll)
		}
	}
}

// publishGrokDeviceProgress validates the verification URL and user code against
// the Grok profile, then makes the login visible as waiting. Both values stay in
// memory: the account document records only the closed login method.
func (s *Service) publishGrokDeviceProgress(ctx context.Context, id domain.ID, o domain.ServerSubscriptionOperation, grant subscription.GrokDeviceGrant) error {
	if !validServerLoginProgress(domain.SubscriptionGrok, grant.VerificationURL, grant.UserCode) {
		s.logGrok(ctx, slog.LevelWarn, "grok_device_progress_rejected", o, grokStepPublish, "code", domain.RecoveryRequired)
		return subscriptionDenied()
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = s.Store.Mutate(ctx, domain.NewID(), "grok.server.waiting", struct{ Account, Operation domain.ID }{id, o.ID}, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, id, 0)
		if err != nil {
			return nil, err
		}
		st := a.Subscription
		if st == nil || st.ServerOperation == nil || st.ServerOperation.ID != o.ID || st.Pending == nil || !st.Pending.DeviceCode || st.Pending.Canceled || st.RecoveryRequired || subscriptionActorValid(tx, o.Actor) != nil {
			return nil, subscriptionDenied()
		}
		st.ServerOperation.State = domain.SubscriptionWaiting
		st.ServerOperation.LoginMethod = domain.SubscriptionDeviceCode
		if _, err := tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	if err == nil {
		if s.subscriptionProgress == nil {
			s.subscriptionProgress = map[domain.ID]subscriptionProgress{}
		}
		s.subscriptionProgress[o.ID] = subscriptionProgress{URL: grant.VerificationURL, UserCode: grant.UserCode, Until: o.ExpiresAt}
		s.logGrok(ctx, slog.LevelInfo, "grok_device_waiting", o, grokStepPublish, "interval_ms", grant.Interval.Milliseconds(), "expires_in_s", int64(grant.ExpiresIn/time.Second))
	}
	return err
}
