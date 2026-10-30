// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

type mistralWait struct{ ticket, claim, account, version, digest, model string }

func canonicalMistral(t inference.Target) bool {
	return t.Auth == inference.APIKey && t.Wire == inference.Chat && strings.TrimRight(t.BaseURL, "/") == "https://api.mistral.ai/v1"
}
func mistralCount(h http.Header, key string) (uint64, bool) {
	raw := strings.TrimSpace(h.Get(key))
	if raw == "" {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, e := strconv.ParseUint(raw, 10, 53)
	return n, e == nil
}
func mistralMinutePressure(h http.Header) bool {
	_, month := mistralCount(h, "x-ratelimit-limit-tokens-month")
	remaining, known := mistralCount(h, "x-ratelimit-remaining-tokens-month")
	if month && known && remaining == 0 {
		return false
	}
	limit, hasLimit := mistralCount(h, "x-ratelimit-limit-tokens-minute")
	left, hasLeft := mistralCount(h, "x-ratelimit-remaining-tokens-minute")
	cost, hasCost := mistralCount(h, "x-ratelimit-tokens-query-cost")
	if hasLimit && limit > 0 && hasCost && cost > limit {
		return false
	}
	if hasLimit && limit > 0 && hasLeft && left <= limit && (left == 0 || hasCost && cost > left) {
		return true
	}
	requests, hasRequests := mistralCount(h, "x-ratelimit-limit-req-minute")
	requestLeft, hasRequestLeft := mistralCount(h, "x-ratelimit-remaining-req-minute")
	return hasRequests && requests > 0 && hasRequestLeft && requestLeft == 0
}
func mistralHoldDelay(f inference.HTTPFailure, now time.Time) (time.Duration, bool) {
	tokens, tk := mistralCount(f.Header, "x-ratelimit-remaining-tokens-minute")
	requests, rk := mistralCount(f.Header, "x-ratelimit-remaining-req-minute")
	if f.Status != 429 && !(tk && tokens == 0) && !(rk && requests == 0) {
		return 0, false
	}
	if raw := strings.TrimSpace(f.Header.Get("Retry-After")); raw != "" {
		if retryNumber.MatchString(raw) {
			seconds, e := strconv.ParseFloat(raw, 64)
			if e == nil {
				if seconds <= 0 {
					return 0, false
				}
				if seconds > float64((32*24*time.Hour)/time.Second) {
					seconds = float64((32 * 24 * time.Hour) / time.Second)
				}
				return time.Duration(seconds * float64(time.Second)), true
			}
		}
		if at, e := http.ParseTime(raw); e == nil {
			delay := at.Sub(now)
			if delay <= 0 {
				return 0, false
			}
			return min(delay, 32*24*time.Hour), true
		}
	}
	if f.Status == 429 && mistralMinutePressure(f.Header) {
		return time.Minute, true
	}
	return 0, false
}

// Pacing waits outside the database dispatch transaction. The store owns the
// cross-instance hold, waiter bound and exclusive probe; this struct only carries
// the current request's opaque ticket. A canceled/ambiguous probe is not reclaimed.
func (a *inferenceAdapter) awaitMistral(ctx context.Context, p inference.Principal) error {
	a.mu.Lock()
	target, id, version := a.activeTarget, a.activeAccount, a.activeVersion
	d := a.connections[id]
	a.mu.Unlock()
	if !canonicalMistral(target) {
		return nil
	}
	lease, e := a.current(p)
	if e != nil {
		return e
	}
	hash := sha256.Sum256([]byte(d.Secret))
	digest := hex.EncodeToString(hash[:])
	key := selectionVersion([]string{id, version, target.Model})
	a.mu.Lock()
	state, exists := a.mistral[key]
	a.mu.Unlock()
	if !exists {
		ticket, e := a.s.JoinMistralHold(ctx, lease, id, version, digest, target.Model)
		if e != nil {
			return inferenceError(e)
		}
		state = mistralWait{ticket: ticket, account: id, version: version, digest: digest, model: target.Model}
		a.mu.Lock()
		if a.mistral == nil {
			a.mistral = map[string]mistralWait{}
		}
		a.mistral[key] = state
		a.mu.Unlock()
	}
	maxWait := target.Retry.MaxWait
	if maxWait <= 0 {
		maxWait = time.Minute
	}
	deadline := time.Now().Add(maxWait)
	for {
		if !time.Now().Before(deadline) {
			return &inference.Error{Status: 429, Code: "upstream_rate_limited", RetryAfter: time.Second}
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		probe, e := a.s.TryMistralProbe(ctx, lease, state.ticket)
		if e != nil {
			return inferenceError(e)
		}
		if probe.Ready {
			state.claim = probe.Claim
			a.mu.Lock()
			a.mistral[key] = state
			a.mu.Unlock()
			return nil
		}
		wait := time.Until(probe.RetryAt)
		if wait > time.Until(deadline) {
			return &inference.Error{Status: 429, Code: "upstream_rate_limited", RetryAfter: wait}
		}
		if wait <= 0 {
			wait = 100 * time.Millisecond
		}
		if wait > time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (a *inferenceAdapter) observeMistral(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) error {
	if !canonicalMistral(in.Target) {
		return nil
	}
	lease, e := a.current(in.Principal)
	if e != nil {
		return e
	}
	a.mu.Lock()
	version := a.versions[in.Credential.AccountID]
	key := selectionVersion([]string{in.Credential.AccountID, version, in.Target.Model})
	state, exists := a.mistral[key]
	a.mu.Unlock()
	if !exists {
		return nil
	}
	delay, hold := mistralHoldDelay(f, time.Now())
	if hold {
		until := time.Now().Add(delay)
		if state.claim != "" {
			e = a.s.FinishMistralProbe(ctx, lease, state.ticket, state.claim, &until)
		} else {
			e = a.s.SetMistralHold(ctx, lease, state.account, state.version, state.digest, state.model, until)
		}
	} else if state.claim != "" {
		if f.Status >= 200 && f.Status < 300 {
			e = a.s.FinishMistralProbe(ctx, lease, state.ticket, state.claim, nil)
		} else {
			// A confirmed non-success ends this probe but must not clear the recovery
			// generation as a success. Another waiter may probe without inventing delay.
			now := time.Now()
			e = a.s.FinishMistralProbe(ctx, lease, state.ticket, state.claim, &now)
		}
	}
	if e != nil {
		return inferenceError(e)
	}
	state.claim = ""
	a.mu.Lock()
	a.mistral[key] = state
	a.mu.Unlock()
	return nil
}
func (a *inferenceAdapter) leaveMistral(ctx context.Context, lease store.Lease) {
	a.mu.Lock()
	states := a.mistral
	a.mistral = nil
	a.mu.Unlock()
	for _, state := range states {
		if e := a.s.LeaveMistralHold(ctx, lease, state.ticket); e != nil {
			a.telemetry.Event(ctx, "mistral_waiter_release_failed")
		}
	}
}
