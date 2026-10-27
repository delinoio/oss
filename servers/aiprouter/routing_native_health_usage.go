// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

// nativeUsageRequest is one bounded account read. It cannot use another origin,
// redirect, refresh credentials, or create a replacement physical-send budget.
func nativeUsageRequest(ctx context.Context, policy inference.DestinationPolicy, gate inference.SendGate, provider string, c inference.Credential) (nativeQuotaSnapshot, error) {
	if !inference.HasPhysicalSendBudget(ctx) {
		return nativeQuotaSnapshot{}, inference.ErrUnavailable
	}
	endpoint := "https://chatgpt.com/backend-api/wham/usage"
	if provider == "anthropic" || provider == "anthropic2" {
		endpoint = "https://api.anthropic.com/api/oauth/usage"
	} else if provider != "openai" {
		return nativeQuotaSnapshot{}, inference.ErrUnsupported
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, endpoint, nil)
	if e != nil {
		return nativeQuotaSnapshot{}, inference.ErrUnavailable
	}
	req.Header = make(http.Header)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("Accept", "application/json")
	if provider == "openai" {
		if id := c.Headers.Get("ChatGPT-Account-Id"); id != "" {
			req.Header.Set("ChatGPT-Account-Id", id)
		}
	} else {
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "claude-cli/2.1.280 (external, cli)")
		req.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	}
	resp, e := policy.Do(bounded, req, gate, nil)
	if e != nil {
		return nativeQuotaSnapshot{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nativeQuotaSnapshot{}, &inference.Error{Status: 502, Code: "native_quota_unavailable"}
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return nativeQuotaSnapshot{}, inference.ErrUnavailable
	}
	return parseNativeQuotaBody(provider, raw, time.Now())
}
func nativeQuotaProvesRecovery(q nativeQuotaSnapshot, now time.Time) bool {
	measured := false
	for _, w := range q.Windows {
		if w.Window != "5h" && w.Window != "weekly" && w.Window != "monthly" {
			continue
		}
		if !nativeWindowCurrent(w, now) {
			continue
		}
		measured = true
		if w.Percent >= 100 {
			return false
		}
	}
	return measured
}

// Quota probes claim only reset-derived cooldowns. A WHAM read describes shared
// quota and never clears Reserve or an explicit account-wide Retry-After.
func (a *inferenceAdapter) recoverNativeQuotaIfDue(ctx context.Context, p inference.Principal, t inference.Target, provider string, c store.SelectionCandidate) (bool, error) {
	if !nativeProvider(provider, t.Auth) {
		return true, nil
	}
	l, e := a.current(p)
	if e != nil {
		return false, e
	}
	if provider == "openai" {
		for _, scope := range []string{"", nativeQuotaScope(provider, t.Model)} {
			k := nativeHealthKey(c, store.NativeHealthQuota, scope)
			h, e := a.s.GetNativeHealth(ctx, l, k)
			if e != nil {
				return false, inferenceError(e)
			}
			if !h.CooldownUntil.After(h.Now) {
				if h.AvoidUntil.After(h.Now) {
					return false, nil
				}
				continue
			}
			if scope == "reserve" || !h.ProbeEligible || h.NextProbeAt.After(h.Now) || !inference.HasPhysicalSendBudget(ctx) {
				return false, nil
			}
			permit, e := a.s.AcquireNativeHealthPermit(ctx, l, k, true)
			if errors.Is(e, store.ErrBusy) {
				return false, nil
			}
			if e != nil {
				return false, inferenceError(e)
			}
			// The helper owns this permit until all usage I/O has returned. A
			// canceled completion must not orphan the non-expiring quota probe.
			// Failed immediate cleanup remains durable under the admission.
			defer a.releaseTerminalNativeHealthPermit(ctx, l, permit)
			d, v, e := a.providerVersion(ctx, scopeOf(p), c.ID)
			if e != nil || v != c.Version {
				if e != nil {
					return false, inferenceError(e)
				}
				return false, inference.ErrNoCredential
			}
			credential, e := credentialFor(ctx, d, t)
			if e != nil {
				return false, e
			}
			credential.CredentialVersion = v
			sent := false
			gate := func(sendctx context.Context, start func(context.Context) error) error {
				return a.s.WithNativeHealthDispatch(sendctx, l, []store.NativeHealthPermit{permit}, func(locked context.Context) error { sent = true; return start(locked) })
			}
			q, fetchErr := nativeUsageRequest(ctx, inference.RoutingDestinationPolicy(ctx), gate, provider, credential)
			if !sent {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, nil
			}
			recovered := fetchErr == nil && nativeQuotaProvesRecovery(q, time.Now())
			e = a.s.FinishNativeHealthPermit(ctx, l, permit, func(current store.NativeHealth) (store.NativeHealth, error) {
				if recovered {
					current.CooldownUntil = time.Time{}
					current.AvoidUntil = time.Time{}
					current.ProbeEligible = false
				}
				return current, nil
			})
			if errors.Is(e, store.ErrConflict) || errors.Is(e, store.ErrExpired) {
				return false, nil
			}
			if e != nil {
				return false, inferenceError(e)
			}
			if fetchErr == nil {
				if e = a.writeNativeQuota(ctx, p, c.ID, c.Version, q); e != nil {
					return false, e
				}
			}
			if !recovered {
				return false, nil
			}
		}
	}
	if e := a.collectNativeUsage(ctx, p, t, provider, c); e != nil {
		return false, e
	}
	return true, nil
}

func (a *inferenceAdapter) collectNativeUsage(ctx context.Context, p inference.Principal, t inference.Target, provider string, c store.SelectionCandidate) error {
	if !inference.HasPhysicalSendBudget(ctx) {
		return nil
	}
	q, found, e := a.readNativeQuota(ctx, p, c.ID, c.Version)
	if e != nil {
		return e
	}
	newest := time.Time{}
	for _, w := range q.Windows {
		if w.ObservedAt.After(newest) {
			newest = w.ObservedAt
		}
	}
	if found && !newest.IsZero() && time.Since(newest) < 5*time.Minute {
		return nil
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	claim, e := a.s.ClaimQuotaCollection(ctx, l, c.ID, c.Version, "native-usage", 45*time.Second)
	if errors.Is(e, store.ErrBusy) {
		return nil
	}
	if e != nil {
		return inferenceError(e)
	}
	d, version, e := a.providerVersion(ctx, scopeOf(p), c.ID)
	if e != nil {
		return inferenceError(e)
	}
	if version != c.Version {
		return inference.ErrNoCredential
	}
	credential, e := credentialFor(ctx, d, t)
	if e != nil {
		return e
	}
	credential.CredentialVersion = version
	var authorityErr error
	gate := func(cctx context.Context, start func(context.Context) error) error {
		e := a.s.WithAccountDispatch(cctx, l, c.ID, c.Version, start)
		if e != nil {
			authorityErr = e
		}
		return e
	}
	update, fetchErr := nativeUsageRequest(ctx, inference.RoutingDestinationPolicy(ctx), gate, provider, credential)
	if authorityErr != nil {
		return inferenceError(authorityErr)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if fetchErr == nil {
		if e = a.writeNativeQuota(ctx, p, c.ID, c.Version, update); e != nil {
			return e
		}
	}
	return inferenceError(a.s.CompleteQuotaCollection(ctx, l, c.ID, c.Version, "native-usage", claim, 5*time.Minute, nil))
}
