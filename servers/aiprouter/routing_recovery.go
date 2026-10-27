// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Classifiers follow opencodex 88aaa754566d119b6b8fc898f8fb47602387d82e:
// anthropic-account-refusal, anthropic-billing-refusal, key-failover and
// responses/core-codex-account. Payloads remain request-local.
type recoveryHold struct {
	model  string
	delay  time.Duration
	reauth bool
}
type serviceRecoveryPolicy struct {
	adapter  *inferenceAdapter
	delegate inference.HTTPRecoveryPolicy
}

func (a *inferenceAdapter) recoveryPolicies(base map[inference.Wire]inference.HTTPRecoveryPolicy) map[inference.Wire]inference.HTTPRecoveryPolicy {
	out := make(map[inference.Wire]inference.HTTPRecoveryPolicy, len(base)+3)
	for wire, policy := range base {
		out[wire] = serviceRecoveryPolicy{a, policy}
	}
	for _, wire := range []inference.Wire{inference.Chat, inference.Responses, inference.Anthropic} {
		if _, ok := out[wire]; !ok {
			out[wire] = serviceRecoveryPolicy{adapter: a}
		}
	}
	return out
}

func (p serviceRecoveryPolicy) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	if err := ctx.Err(); err != nil {
		return inference.RecoveryDecision{}, err
	}
	managedQuota, err := p.adapter.failNativeHealth(ctx, in, f)
	if err != nil {
		return inference.RecoveryDecision{}, err
	}
	if err := p.adapter.failNative(ctx, in, f); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if err := p.adapter.observeMistral(ctx, in, f); err != nil {
		return inference.RecoveryDecision{}, err
	}
	if in.Target.Wire == "dsh-account" && f.Status == http.StatusUnauthorized {
		lease, err := p.adapter.current(in.Principal)
		if err != nil {
			return inference.RecoveryDecision{}, err
		}
		p.adapter.mu.Lock()
		version := p.adapter.versions[in.Credential.AccountID]
		p.adapter.mu.Unlock()
		if err = p.adapter.s.MarkPrivateReauthentication(ctx, lease, string(providerKind), in.Credential.AccountID, version); err != nil {
			return inference.RecoveryDecision{}, inferenceError(err)
		}
	}
	// Adapter-local recovery has precedence, including an explicit terminal verdict.
	if p.delegate != nil {
		d, err := p.delegate.RecoverHTTP(ctx, in, f)
		if err != nil || d.Action != "" && d.Action != inference.RecoveryDefault {
			return d, err
		}
	}
	p.adapter.mu.Lock()
	selection, ok := p.adapter.selections[selectionKey(in.Target)]
	p.adapter.mu.Unlock()
	if !ok {
		return inference.RecoveryDecision{Action: inference.RecoveryDefault}, nil
	}
	d, hold := pinnedRecovery(selection.provider, in, f, time.Now())
	if in.Target.Auth == inference.APIKey && f.Status == 429 {
		if !in.Target.Retry.RotateOn429 {
			hold = nil
		}
		delay, valid := recoveryRetryAfter(f.Header.Get("Retry-After"), time.Now(), true)
		if f.Attempt <= in.Target.Retry.RateLimitRetries && (!valid || delay <= in.Target.Retry.MaxWait) {
			d.Action = inference.RecoveryDefault
		}
	}
	if selection.provider == "openai" && codexRefusedModel(f, in.Target.Model) {
		lease, err := p.adapter.current(in.Principal)
		if err != nil {
			return inference.RecoveryDecision{}, err
		}
		p.adapter.mu.Lock()
		version := p.adapter.versions[in.Credential.AccountID]
		p.adapter.mu.Unlock()
		if err = p.adapter.s.SetAccountSoftDenial(ctx, lease, in.Credential.AccountID, version, in.Target.Model); err != nil {
			return inference.RecoveryDecision{}, inferenceError(err)
		}
	}
	if hold != nil && (!managedQuota || hold.reauth) {
		if err := p.adapter.recordRecoveryHold(ctx, in, *hold); err != nil {
			return inference.RecoveryDecision{}, err
		}
	}
	return d, nil
}

// The service owns state writes; the classifier and provider delegates remain pure.
// Bind each write to the version captured before this credential's physical send.
func (a *inferenceAdapter) recordRecoveryHold(ctx context.Context, in inference.AdapterInput, hold recoveryHold) error {
	lease, err := a.current(in.Principal)
	if err != nil {
		return err
	}
	a.mu.Lock()
	version := a.versions[in.Credential.AccountID]
	a.mu.Unlock()
	if version == "" {
		return inference.ErrNoCredential
	}
	if hold.reauth {
		raw, current, err := a.s.GetPrivateVersion(ctx, scopeOf(in.Principal), string(providerKind), in.Credential.AccountID)
		if err != nil {
			return inferenceError(err)
		}
		if current != version {
			return inference.ErrNoCredential
		}
		var d providerData
		if json.Unmarshal(raw, &d) != nil || d.Metadata == nil {
			return inference.ErrNoCredential
		}
		d.Metadata.State = v1.ConnectionState_CONNECTION_STATE_AUTHORIZATION_REQUIRED
		raw, err = json.Marshal(d)
		if err != nil {
			return inference.ErrUnavailable
		}
		return inferenceError(a.s.CompareAndSwapPrivate(ctx, lease, string(providerKind), in.Credential.AccountID, version, raw))
	}
	return inferenceError(a.s.SetAccountCooldownVersion(ctx, lease, in.Credential.AccountID, version, hold.model, time.Now().Add(hold.delay)))
}

var (
	creditRefusal       = regexp.MustCompile(`(?i)^Your credit balance is too low to access the Anthropic API\.(?: Please go to Plans & Billing to upgrade or purchase credits\.)?$`)
	subscriptionRefusal = regexp.MustCompile(`(?i)^(?:Your account does not have access to Claude Code|Your (?:Claude )?subscription (?:has expired|is (?:expired|inactive))|Your account does not have an active subscription|(?:Your|This) organization has disabled Claude subscription access for Claude Code(?:\. Use an Anthropic API key instead, or ask your admin to enable access)?)[.!]?$`)
	codexModelRefusal   = regexp.MustCompile(`^the '([^']{1,256})' model is not supported when using codex with a chatgpt account\.$`)
	quotaPrefix         = regexp.MustCompile(`(?i)^(?:Weekly|Monthly) Limit Exhausted\b`)
	quotaDate           = regexp.MustCompile(`(?i)resets?\s+at\s+([0-9]{4}-[0-9]{2}-[0-9]{2}(?:[T ][0-9]{2}:[0-9]{2}(?::[0-9]{2})?(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:?[0-9]{2})?)?)`)
	retryNumber         = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
)

func recoveryEnvelope(body []byte, limit int) (map[string]any, map[string]any) {
	if len(body) > limit || !utf8.Valid(body) {
		return nil, nil
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil, nil
	}
	e, _ := root["error"].(map[string]any)
	return root, e
}
func recoveryString(v any) string { s, _ := v.(string); return s }
func refusalCode(e map[string]any, org bool) bool {
	if e["code"] == nil {
		return true
	}
	switch e["code"] {
	case "subscription_required", "insufficient_quota", "permission_denied":
		return true
	case "oauth_org_not_allowed":
		return org
	}
	return false
}
func anthropicBilling(f inference.HTTPFailure) bool {
	if f.Status != 400 && f.Status != 402 {
		return false
	}
	media := strings.ToLower(strings.TrimSpace(strings.Split(f.Header.Get("Content-Type"), ";")[0]))
	if media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")) {
		return false
	}
	root, e := recoveryEnvelope(f.Body, 8192)
	if root["type"] != "error" || !refusalCode(e, false) {
		return false
	}
	switch e["type"] {
	case "invalid_request_error", "billing_error", "permission_error":
		return creditRefusal.MatchString(strings.TrimSpace(recoveryString(e["message"])))
	}
	return false
}
func anthropicRevoked(f inference.HTTPFailure) bool {
	root, e := recoveryEnvelope(f.Body, 1<<20)
	return f.Status == 401 && root["type"] == "error" && e["type"] == "authentication_error" && e["message"] == "OAuth access token has been revoked." && e["code"] == nil
}
func anthropicAccountRefusal(f inference.HTTPFailure) bool {
	if f.Status != 403 {
		return false
	}
	_, e := recoveryEnvelope(f.Body, 1<<20)
	message, ok := e["message"].(string)
	if !ok || !refusalCode(e, true) {
		return false
	}
	if e["type"] == "oauth_org_not_allowed" || e["code"] == "oauth_org_not_allowed" {
		return true
	}
	message = strings.TrimSpace(message)
	return e["type"] == "permission_error" && subscriptionRefusal.MatchString(message) || (e["type"] == "billing_error" || e["type"] == "permission_error") && creditRefusal.MatchString(message)
}
func codexRefusedModel(f inference.HTTPFailure, model string) bool {
	if f.Status != 400 {
		return false
	}
	root, e := recoveryEnvelope(f.Body, 1<<20)
	_, detail := root["detail"]
	_, nested := root["error"]
	if detail == nested {
		return false
	}
	message := recoveryString(root["detail"])
	if nested {
		for _, key := range []string{"type", "code"} {
			if e[key] != nil {
				if _, ok := e[key].(string); !ok {
					return false
				}
			}
		}
		message = recoveryString(e["message"])
	}
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	match := codexModelRefusal.FindStringSubmatch(normalize(message))
	return len(match) == 2 && match[1] == normalize(model)
}

func pinnedRecovery(provider string, in inference.AdapterInput, f inference.HTTPFailure, now time.Time) (inference.RecoveryDecision, *recoveryHold) {
	d := inference.RecoveryDecision{Action: inference.RecoveryDefault}
	rotate := func(h recoveryHold) (inference.RecoveryDecision, *recoveryHold) {
		return inference.RecoveryDecision{Action: inference.RecoveryRotate}, &h
	}
	anthropic := provider == "anthropic" || provider == "anthropic2"
	if in.Target.Auth == inference.APIKey {
		if anthropic && anthropicBilling(f) {
			return rotate(recoveryHold{delay: time.Hour})
		}
		if f.Status == 401 {
			return rotate(recoveryHold{delay: 10 * time.Minute})
		}
		if f.Status == 429 {
			delay := time.Minute
			if parsed, ok := recoveryRetryAfter(f.Header.Get("Retry-After"), now, true); ok {
				delay = min(parsed, 10*time.Minute)
			}
			if parsed, ok := keyQuotaReset(f.Body, now); ok {
				delay = parsed
			}
			// Keep configured same-key retry/fallback policy in the executor when no sibling exists.
			if in.Target.Retry.RotateOn429 {
				d.Action = inference.RecoveryRotate
			}
			return d, &recoveryHold{delay: delay}
		}
	}
	if in.Target.Auth != inference.OAuth {
		return d, nil
	}
	if anthropic {
		if anthropicRevoked(f) {
			return rotate(recoveryHold{reauth: true})
		}
		if anthropicAccountRefusal(f) {
			delay := 10 * time.Minute
			if parsed, ok := recoveryRetryAfter(f.Header.Get("Retry-After"), now, false); ok {
				delay = parsed
			}
			return rotate(recoveryHold{delay: min(delay, 32*24*time.Hour)})
		}
		if f.Status == 429 {
			shared := false
			for _, w := range []string{"5h", "7d"} {
				shared = shared || strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-"+w+"-status")) == "rejected"
			}
			family := strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-7d_oi-status")) == "rejected"
			shared = shared || !family && strings.TrimSpace(f.Header.Get("anthropic-ratelimit-unified-status")) == "rejected"
			if shared {
				return rotate(recoveryHold{delay: anthropicSharedDelay(f.Header, now)})
			}
			// A family quota or undated request refusal does not establish shared exhaustion.
			return inference.RecoveryDecision{Action: inference.RecoveryStop}, nil
		}
	}
	if provider == "openai" && codexRefusedModel(f, in.Target.Model) {
		// Upstream observed-model-denials is a soft ranking preference: when all
		// accounts are denied, they remain eligible. The cooldown store is a hard
		// exclusion, so it must not persist this evidence. Selection can add a
		// separate six-hour soft-denial store when it supports that fallback.
		return inference.RecoveryDecision{Action: inference.RecoveryRotate}, nil
	}
	return d, nil
}

func recoveryRetryAfter(raw string, now time.Time, immediate bool) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return 0, false
	}
	var delay time.Duration
	if retryNumber.MatchString(raw) {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
			return 0, false
		}
		delay = time.Duration(math.Min(math.Ceil(seconds*1000), float64((32*24*time.Hour)/time.Millisecond))) * time.Millisecond
	} else {
		at, err := http.ParseTime(raw)
		if err != nil {
			at, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			return 0, false
		}
		delay = at.Sub(now)
	}
	if delay <= 0 {
		if !immediate {
			return 0, false
		}
		delay = time.Millisecond
	}
	return min(delay, 32*24*time.Hour), true
}
func keyQuotaReset(body []byte, now time.Time) (time.Duration, bool) {
	_, e := recoveryEnvelope(body, 4096)
	if e["code"] != "rate_limit_error" && e["code"] != float64(429) {
		return 0, false
	}
	message := strings.TrimSpace(recoveryString(e["message"]))
	if !quotaPrefix.MatchString(message) {
		return 0, false
	}
	match := quotaDate.FindStringSubmatch(message)
	if len(match) != 2 {
		return 0, false
	}
	raw := strings.Replace(match[1], " ", "T", 1)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z07:00", "2006-01-02T15:04Z0700", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if at, err := time.Parse(layout, raw); err == nil && at.After(now) {
			return min(at.Sub(now), 32*24*time.Hour), true
		}
	}
	return 0, false
}
func anthropicSharedDelay(h http.Header, now time.Time) time.Duration {
	if delay, ok := recoveryRetryAfter(h.Get("Retry-After"), now, false); ok {
		return delay
	}
	delay := time.Duration(0)
	rejected := false
	for _, window := range []string{"5h", "7d"} {
		if strings.TrimSpace(h.Get("anthropic-ratelimit-unified-"+window+"-status")) != "rejected" {
			continue
		}
		rejected = true
		delay = max(delay, recoveryResetDelay(h.Get("anthropic-ratelimit-unified-"+window+"-reset"), now))
	}
	if !rejected {
		delay = recoveryResetDelay(h.Get("anthropic-ratelimit-unified-reset"), now)
	}
	if delay <= 0 {
		delay = time.Minute
	}
	return min(delay, 32*24*time.Hour)
}
func recoveryResetDelay(raw string, now time.Time) time.Duration {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds <= 0 {
		return 0
	}
	delta := seconds - float64(now.UnixMilli())/1000
	if delta <= 0 {
		return 0
	}
	return time.Duration(math.Min(delta, float64((32*24*time.Hour)/time.Second)) * float64(time.Second))
}
