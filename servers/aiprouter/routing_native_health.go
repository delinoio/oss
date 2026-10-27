// SPDX-License-Identifier: Apache-2.0
package main

import (
	"time"
)

// Policy follows opencodex 88aaa754566d119b6b8fc898f8fb47602387d82e.
// Values are serialized under the credential version in PostgreSQL. This type
// is never a process cache or a substitute for a physical dispatch receipt.
type nativeHealthState struct {
	Failures     int        `json:"f,omitempty"`
	Successes    int        `json:"s,omitempty"`
	LastFailure  int64      `json:"lf,omitempty"`
	AvoidUntil   int64      `json:"a,omitempty"`
	Samples      [][2]int64 `json:"w,omitempty"`
	Degraded     bool       `json:"d,omitempty"`
	HealthySince int64      `json:"hs,omitempty"`
}

type nativeHealthOutcome uint8

const (
	nativeHealthUnknown nativeHealthOutcome = iota
	nativeHealthSuccess
	nativeHealthTransient
	nativeHealthCaller
	nativeHealthNeutral
	nativeHealthCredential
	nativeHealthWorkspace
	nativeHealthQuota
)
const (
	nativeHealthFailureGap        = 5 * time.Minute
	nativeHealthHoldBound         = 10 * time.Minute
	nativeHealthProbeLease        = 30 * time.Second
	nativeHealthProbeSpacing      = 15 * time.Second
	nativeHealthQuotaProbeSpacing = 5 * time.Minute
)

var nativeHealthAvoidSteps = [...]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

type nativeHealthPolicy struct {
	Threshold     int
	FailureWindow bool
	DetourPinned  bool
}

func defaultNativeHealthPolicy() nativeHealthPolicy {
	return nativeHealthPolicy{Threshold: 3, FailureWindow: true}
}
func classifyNativeHealth(status int, transport nativeHealthOutcome, workspaceDenial bool) nativeHealthOutcome {
	if transport == nativeHealthNeutral || transport == nativeHealthTransient {
		return transport
	}
	switch {
	case status >= 200 && status < 300:
		return nativeHealthSuccess
	case status >= 300 && status < 400:
		return nativeHealthNeutral
	case status == 403 && workspaceDenial:
		return nativeHealthWorkspace
	case status == 401 || status == 403:
		return nativeHealthCredential
	case status == 402 || status == 429:
		return nativeHealthQuota
	case status >= 400 && status < 500:
		return nativeHealthCaller
	case status >= 500 && status < 600:
		return nativeHealthTransient
	default:
		return nativeHealthUnknown
	}
}
func (h *nativeHealthState) recompute(now time.Time) {
	cutoff := now.Add(-time.Minute).UnixMilli()
	kept := h.Samples[:0]
	failures := int64(0)
	for _, s := range h.Samples {
		if s[0] > cutoff {
			kept = append(kept, s)
			failures += s[1]
		}
	}
	h.Samples = kept
	n := int64(len(kept))
	if n >= 20 && failures*4 >= n {
		h.Degraded = true
		h.HealthySince = 0
		return
	}
	if !h.Degraded || n < 20 || failures*10 > n {
		h.HealthySince = 0
		return
	}
	if h.HealthySince == 0 {
		h.HealthySince = now.UnixMilli()
	}
	if now.UnixMilli()-h.HealthySince >= int64(30*time.Second/time.Millisecond) {
		h.Degraded = false
		h.HealthySince = 0
	}
}
func (h *nativeHealthState) note(outcome nativeHealthOutcome, policy nativeHealthPolicy, now time.Time) {
	if policy.FailureWindow && (outcome == nativeHealthSuccess || outcome == nativeHealthTransient) {
		failed := int64(0)
		if outcome == nativeHealthTransient {
			failed = 1
		}
		h.Samples = append(h.Samples, [2]int64{now.UnixMilli(), failed})
		if len(h.Samples) > 512 {
			h.Samples = h.Samples[len(h.Samples)-512:]
		}
		h.recompute(now)
	}
	switch outcome {
	case nativeHealthSuccess:
		if policy.Threshold > 0 && h.Failures >= 2 {
			h.Successes++
			if h.Successes < 2 {
				return
			}
		}
		h.Failures = 0
		h.Successes = 0
		h.LastFailure = 0
		h.AvoidUntil = 0
	case nativeHealthTransient:
		if h.LastFailure != 0 && now.UnixMilli()-h.LastFailure > nativeHealthFailureGap.Milliseconds() {
			h.Failures = 0
		}
		h.Failures++
		h.Successes = 0
		h.LastFailure = now.UnixMilli()
		if policy.Threshold > 0 && h.Failures >= policy.Threshold {
			level := min(max(h.Failures-policy.Threshold, 0), len(nativeHealthAvoidSteps)-1)
			h.AvoidUntil = max(h.AvoidUntil, now.Add(nativeHealthAvoidSteps[level]).UnixMilli())
		} else {
			h.AvoidUntil = 0
		}
	case nativeHealthWorkspace:
		// An entitlement refusal is not a credential revocation or window sample.
		h.Failures++
		h.Successes = 0
		h.LastFailure = now.UnixMilli()
		h.AvoidUntil = 0
	}
}
func (h nativeHealthState) avoided(policy nativeHealthPolicy, now time.Time) bool {
	return policy.Threshold > 0 && (h.AvoidUntil > now.UnixMilli() || h.Failures >= policy.Threshold && h.LastFailure != 0 && now.UnixMilli()-h.LastFailure <= nativeHealthFailureGap.Milliseconds())
}
func (h *nativeHealthState) steersNew(policy nativeHealthPolicy, pinned bool, now time.Time) bool {
	if !policy.FailureWindow {
		return false
	}
	h.recompute(now)
	return h.Degraded && (!pinned || policy.DetourPinned)
}
func nativeHealthHoldExpired(since time.Time, now time.Time) bool {
	return !since.IsZero() && now.Sub(since) > nativeHealthHoldBound
}

type nativeHealthCooldownSource string

const (
	nativeHealthCooldownDefault    nativeHealthCooldownSource = "default"
	nativeHealthCooldownRetryAfter nativeHealthCooldownSource = "retry-after"
	nativeHealthCooldownReset      nativeHealthCooldownSource = "reset-derived"
)

type nativeHealthCooldown struct {
	Until, AvoidUntil time.Time
	Source            nativeHealthCooldownSource
}

func nativeHealthQuotaCooldown(retryAfter string, resets []time.Time, now time.Time) nativeHealthCooldown {
	c := nativeHealthCooldown{Until: now.Add(time.Minute), Source: nativeHealthCooldownDefault}
	reset := time.Time{}
	for _, at := range resets {
		if at.After(now) && (reset.IsZero() || at.Before(reset)) {
			reset = at
		}
	}
	if delay, ok := recoveryRetryAfter(retryAfter, now, false); ok {
		c.Until = now.Add(min(delay, 24*time.Hour))
		c.Source = nativeHealthCooldownRetryAfter
	} else if !reset.IsZero() {
		c.Until = now.Add(min(reset.Sub(now), 15*time.Minute))
		c.Source = nativeHealthCooldownReset
	}
	c.AvoidUntil = c.Until
	if !reset.IsZero() {
		at := now.Add(min(reset.Sub(now), 6*time.Hour))
		if at.After(c.AvoidUntil) {
			c.AvoidUntil = at
		}
	}
	return c
}
