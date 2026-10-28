// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Receipts are request-local references to PostgreSQL permits, never authority.
type nativeHealthReceipt struct {
	ConfigGroup, ConfigVersion string
	Policy                     nativeHealthPolicy
	Account, Version           string
	Permits                    []store.NativeHealthPermit
	Status                     int
	Headers                    http.Header
	Finished, ManagedQuota     bool
}

func nativeHealthKey(c store.SelectionCandidate, lane store.NativeHealthLane, model string) store.NativeHealthKey {
	return store.NativeHealthKey{AccountID: c.ID, CredentialVersion: c.Version, Lane: lane, Model: model}
}
func decodeNativeHealth(h store.NativeHealth) (nativeHealthState, error) {
	var state nativeHealthState
	if len(h.Payload) > 0 && json.Unmarshal(h.Payload, &state) != nil {
		return state, inference.ErrUnavailable
	}
	return state, nil
}
func nativeHealthWithheld(now, retry time.Time) error {
	delay := retry.Sub(now)
	if delay <= 0 {
		delay = time.Second
	}
	return &inference.Error{Status: 503, Code: "native_dispatch_withheld", RetryAfter: delay}
}
func (a *inferenceAdapter) filterNativeHealthNew(ctx context.Context, p inference.Principal, t inference.Target, provider string, cs []store.SelectionCandidate, pinned string) ([]store.SelectionCandidate, error) {
	if provider != "openai" {
		return cs, nil
	}
	l, e := a.current(p)
	if e != nil {
		return nil, e
	}
	out := make([]store.SelectionCandidate, 0, len(cs))
	policy := a.nativeHealthPolicy(t)
	for _, c := range cs {
		h, e := a.s.GetNativeHealth(ctx, l, nativeHealthKey(c, store.NativeHealthTransient, ""))
		if e != nil {
			return nil, inferenceError(e)
		}
		s, e := decodeNativeHealth(h)
		if e != nil {
			return nil, e
		}
		if policy.FailureWindow {
			beforeHealthy, beforeDegraded, beforeCount := s.HealthySince, s.Degraded, len(s.Samples)
			s.recompute(h.Now)
			if s.HealthySince != beforeHealthy || s.Degraded != beforeDegraded || len(s.Samples) != beforeCount {
				e = a.s.UpdateNativeHealth(ctx, l, nativeHealthKey(c, store.NativeHealthTransient, ""), func(current store.NativeHealth) (store.NativeHealth, error) {
					state, err := decodeNativeHealth(current)
					if err != nil {
						return current, err
					}
					state.recompute(current.Now)
					s = state
					current.Payload, err = json.Marshal(state)
					return current, err
				})
				if e != nil {
					return nil, inferenceError(e)
				}
			}
		}
		if !s.avoided(policy, h.Now) && !s.steersNew(policy, c.ID == pinned, h.Now) {
			out = append(out, c)
		}
	}
	return out, nil
}
func (a *inferenceAdapter) resolveNativeHealthAffinity(ctx context.Context, p inference.Principal, t inference.Target, provider string, state *nativeSelectionState, affinity store.NativeAffinity, cs []store.SelectionCandidate) ([]store.SelectionCandidate, bool, error) {
	if provider != "openai" || affinity.AccountID == "" {
		return nil, false, nil
	}
	var bound *store.SelectionCandidate
	for i := range cs {
		if cs[i].ID == affinity.AccountID && cs[i].Version == affinity.CredentialVersion {
			bound = &cs[i]
			break
		}
	}
	// Other eligibility gates win: quota, removed credentials, and disabled accounts
	// cannot be converted into transient holds.
	if bound == nil {
		return nil, false, nil
	}
	l, e := a.current(p)
	if e != nil {
		return nil, false, e
	}
	h, e := a.s.GetNativeHealth(ctx, l, nativeHealthKey(*bound, store.NativeHealthTransient, ""))
	if e != nil {
		return nil, false, inferenceError(e)
	}
	hs, e := decodeNativeHealth(h)
	if e != nil {
		return nil, false, e
	}
	policy := a.nativeHealthPolicy(t)
	if !hs.avoided(policy, h.Now) {
		if !affinity.HoldSince.IsZero() {
			e = a.s.UpdateNativeAffinityHold(ctx, l, state.Group, state.Version, state.Session, affinity.Epoch, time.Time{}, "")
		}
		return nil, false, inferenceError(e)
	}
	healthy := make([]store.SelectionCandidate, 0, len(cs))
	for _, c := range cs {
		if c.ID != bound.ID {
			healthy = append(healthy, c)
		}
	}
	healthy, e = a.filterNativeHealthNew(ctx, p, t, provider, healthy, "")
	if e != nil {
		return nil, false, e
	}
	// A held thread uses a healthy sibling before it spends a recovery probe.
	var detour *store.SelectionCandidate
	for i := range healthy {
		if healthy[i].ID == affinity.DetourAccountID {
			detour = &healthy[i]
			break
		}
	}

	if nativeHealthHoldExpired(affinity.HoldSince, h.Now) {
		if detour != nil {
			state.Preserve = false
			state.Reused = false
			return []store.SelectionCandidate{*detour}, true, nil
		}
		state.Preserve = false
		state.Reused = false
		state.HealthDetour = true
		if len(healthy) == 0 {
			return nil, true, inference.ErrNoCredential
		}
		return healthy, true, nil
	}
	since := affinity.HoldSince
	if since.IsZero() {
		since = h.Now
	}
	remembered := affinity.DetourAccountID
	if detour != nil {
		remembered = detour.ID
	}
	if e = a.s.UpdateNativeAffinityHold(ctx, l, state.Group, state.Version, state.Session, affinity.Epoch, since, remembered); e != nil {
		return nil, true, inferenceError(e)
	}
	state.Preserve = true
	state.Reused = true
	state.HealthHoldSince = since
	if detour != nil {
		return []store.SelectionCandidate{*detour}, true, nil
	}
	if len(healthy) > 0 {
		state.HealthDetour = true
		return healthy, true, nil
	}
	retry := h.NextProbeAt
	if h.LastProbeAt.Add(nativeHealthProbeSpacing).After(retry) {
		retry = h.LastProbeAt.Add(nativeHealthProbeSpacing)
	}
	if h.ProbeID != "" && h.ProbeExpiresAt.After(retry) {
		retry = h.ProbeExpiresAt
	}
	if retry.After(h.Now) {
		return nil, true, nativeHealthWithheld(h.Now, retry)
	}
	state.HealthProbeAccount = bound.ID
	return []store.SelectionCandidate{*bound}, true, nil
}

func (a *inferenceAdapter) dispatchNativeHealth(ctx context.Context, p inference.Principal, start func(context.Context) error) (bool, error) {
	if inference.ModelDispatchAttempt(ctx) == 0 {
		return false, nil
	}
	a.mu.Lock()
	t := a.activeTarget
	id, version := a.activeAccount, a.activeVersion
	d, ok := a.connections[id]
	s := a.native[selectionKey(t)]
	a.mu.Unlock()
	if !ok || d.Metadata == nil || d.Metadata.ProviderId != "openai" || t.Auth != inference.OAuth {
		return false, nil
	}
	l, e := a.current(p)
	if e != nil {
		return true, e
	}
	c := store.SelectionCandidate{ID: id, Version: version}
	if s.ConfigGroup != "" {
		live, err := a.nativeConfigurationCurrent(ctx, p, s)
		if err != nil {
			return true, err
		}
		if !live {
			return true, inference.ErrNoCredential
		}
	}
	receipt := &nativeHealthReceipt{Account: id, Version: version, Policy: a.nativeHealthPolicy(t), ConfigGroup: s.ConfigGroup, ConfigVersion: s.Version}
	// A policy edit can disable or change the streak threshold without replacing
	// credentials. Reconcile admission deadlines from the current captured policy.
	if e = a.reconcileNativeHealthPolicy(ctx, l, c, receipt.Policy); e != nil {
		return true, e
	}

	keys := []store.NativeHealthKey{nativeHealthKey(c, store.NativeHealthTransient, ""), nativeHealthKey(c, store.NativeHealthQuota, ""), nativeHealthKey(c, store.NativeHealthQuota, nativeQuotaScope("openai", t.Model))}
	for i, k := range keys {
		var permit store.NativeHealthPermit
		var e error
		if i == 0 {
			pool := s.Group + "/" + s.Version
			if s.Group == "" {
				pool = "native/openai/" + t.Provider
			}
			probe := s.HealthProbeAccount == id
			permit, e = a.s.AcquireNativeHealthPermitForRecovery(ctx, l, k, probe, pool, nativeRecoveryClass(ctx, probe))
		} else {
			permit, e = a.s.AcquireNativeHealthPermit(ctx, l, k, false)
		}
		if e != nil {
			for _, owned := range receipt.Permits {
				_ = a.s.ReleaseNativeHealthPermit(ctx, l, owned)
			}
			var limit *store.NativeRecoveryLimitError
			if errors.As(e, &limit) {
				return true, nativeHealthWithheld(time.Now(), limit.RetryAt)
			}
			if errors.Is(e, store.ErrBusy) {
				return true, nativeHealthWithheld(time.Now(), time.Now().Add(time.Second))
			}
			return true, inferenceError(e)
		}
		receipt.Permits = append(receipt.Permits, permit)
	}
	s.Health = receipt
	s.Model = t.Model
	s.TargetProvider = t.Provider
	a.mu.Lock()
	if a.native == nil {
		a.native = map[string]nativeSelectionState{}
	}
	a.native[selectionKey(t)] = s
	a.mu.Unlock()
	e = a.s.WithNativeHealthDispatch(ctx, l, receipt.Permits, start)
	return true, inferenceError(e)
}
func (a *inferenceAdapter) healthReceipt(t inference.Target, c inference.Credential) *nativeHealthReceipt {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.native[selectionKey(t)].Health
	if r == nil || r.Account != c.AccountID || r.Version != c.CredentialVersion {
		return nil
	}
	return r
}
func (a *inferenceAdapter) observeNativeHealthResponse(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int, h http.Header) error {
	r := a.healthReceipt(t, c)
	if r == nil {
		return nil
	}
	a.mu.Lock()
	r.Status = status
	r.Headers = h.Clone()
	a.mu.Unlock()
	if status >= 300 && status < 400 {
		return a.settleNativeHealth(ctx, p, r, nativeHealthNeutral, nativeHealthCooldown{})
	}
	return nil
}
func (a *inferenceAdapter) settleNativeHealth(ctx context.Context, p inference.Principal, r *nativeHealthReceipt, outcome nativeHealthOutcome, cooldown nativeHealthCooldown) error {
	if r == nil {
		return nil
	}
	a.mu.Lock()
	finished := r.Finished
	a.mu.Unlock()
	if finished {
		return nil
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	if r.ConfigGroup != "" {
		live, err := a.nativeConfigurationCurrent(ctx, p, nativeSelectionState{ConfigGroup: r.ConfigGroup, Version: r.ConfigVersion})
		if err != nil {
			return err
		}
		if !live {
			return nil
		}
	}
	quotaApplied := false
	for i, permit := range r.Permits {
		if outcome == nativeHealthNeutral || outcome == nativeHealthCaller || outcome == nativeHealthUnknown {
			e = a.s.ReleaseNativeHealthPermit(ctx, l, permit)
		} else {
			e = a.s.FinishNativeHealthPermit(ctx, l, permit, func(h store.NativeHealth) (store.NativeHealth, error) {
				if i == 0 {
					state, e := decodeNativeHealth(h)
					if e != nil {
						return h, e
					}
					state.note(outcome, r.Policy, h.Now)
					if outcome == nativeHealthQuota && cooldown.Source != nativeHealthCooldownReset {
						state.Failures = 0
						state.Successes = 0
						state.LastFailure = 0
						state.AvoidUntil = 0
					}
					h.Failures = state.Failures
					h.WindowStartedAt = time.UnixMilli(state.LastFailure)
					h.AvoidUntil = time.Time{}
					h.CooldownUntil = time.Time{}
					if state.AvoidUntil != 0 {
						h.AvoidUntil = time.UnixMilli(state.AvoidUntil)
					}
					if r.Policy.Threshold > 0 && state.Failures >= r.Policy.Threshold && state.LastFailure != 0 {
						h.CooldownUntil = time.UnixMilli(state.LastFailure).Add(nativeHealthFailureGap + time.Millisecond)
					}
					h.Payload, e = json.Marshal(state)
					return h, e
				}
				if outcome == nativeHealthSuccess && !h.CooldownUntil.After(h.Now) {
					h.AvoidUntil = time.Time{}
				}
				if outcome == nativeHealthQuota && (i == 1 && cooldown.Source != nativeHealthCooldownReset || i == 2 && cooldown.Source == nativeHealthCooldownReset) {
					h.CooldownSince = h.Now
					h.CooldownUntil = cooldown.Until
					h.AvoidUntil = cooldown.AvoidUntil
					h.ProbeEligible = cooldown.Source == nativeHealthCooldownReset
					if h.NextProbeAt.Before(h.Now.Add(nativeHealthQuotaProbeSpacing)) {
						h.NextProbeAt = h.Now.Add(nativeHealthQuotaProbeSpacing)
					}
				}
				return h, nil
			})
		}
		if e != nil && !errors.Is(e, store.ErrConflict) && !errors.Is(e, store.ErrExpired) {
			return inferenceError(e)
		}
		if e == nil && outcome == nativeHealthQuota && (i == 1 && cooldown.Source != nativeHealthCooldownReset || i == 2 && cooldown.Source == nativeHealthCooldownReset) {
			quotaApplied = true
		}
	}
	a.mu.Lock()
	r.Finished = true
	r.ManagedQuota = quotaApplied
	a.mu.Unlock()
	return nil
}
func (a *inferenceAdapter) failNativeHealth(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (bool, error) {
	r := a.healthReceipt(in.Target, in.Credential)
	if r == nil {
		return false, nil
	}
	outcome := classifyNativeHealth(f.Status, nativeHealthUnknown, nativeHealthWorkspaceDenied(f))
	now := time.Now()
	q := parseNativeQuota("openai", in.Target.Model, f.Status, f.Header, now)
	resets := []time.Time{}
	for _, w := range q.Windows {
		if w.ResetAt != nil {
			resets = append(resets, *w.ResetAt)
		}
	}
	// Error reset evidence uses the structured field, never text matching.
	var body struct {
		Error struct {
			ResetsAt float64 `json:"resets_at"`
		} `json:"error"`
	}
	if len(f.Body) <= 1<<20 && json.Unmarshal(f.Body, &body) == nil && body.Error.ResetsAt > 0 {
		n := body.Error.ResetsAt
		if n < 1e12 {
			n *= 1000
		}
		resets = append(resets, time.UnixMilli(int64(n)))
	}
	e := a.settleNativeHealth(ctx, in.Principal, r, outcome, nativeHealthQuotaCooldown(f.Header.Get("Retry-After"), resets, now))
	a.mu.Lock()
	managed := r.ManagedQuota
	a.mu.Unlock()
	return managed && e == nil, e
}
func (a *inferenceAdapter) nativeHealthOwnsQuota(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int) (bool, error) {
	r := a.healthReceipt(t, c)
	if r == nil {
		return false, nil
	}
	a.mu.Lock()
	managed := r.ManagedQuota
	a.mu.Unlock()
	return (status == 402 || status == 429) && managed, nil
}
func (a *inferenceAdapter) finishNativeHealth(ctx context.Context, p inference.Principal, r inference.UsageRecord) error {
	a.mu.Lock()
	var receipts []*nativeHealthReceipt
	for _, s := range a.native {
		if s.Model == r.Model && s.TargetProvider == r.Provider && s.Health != nil && s.Health.Account == r.AccountID {
			receipts = append(receipts, s.Health)
		}
	}
	a.mu.Unlock()
	outcome := nativeHealthNeutral
	if !r.Canceled && r.Outcome == inference.OutcomeSuccess {
		outcome = nativeHealthSuccess
	}
	for _, receipt := range receipts {
		if e := a.settleNativeHealth(ctx, p, receipt, outcome, nativeHealthCooldown{}); e != nil {
			return e
		}
	}
	return nil
}
func (a *inferenceAdapter) ObserveTransportFailure(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, f inference.TransportFailure) error {
	outcome := nativeHealthNeutral
	if f.Sent && (f.Kind == inference.TransportFailureTimeout || f.Kind == inference.TransportFailureConnection) {
		outcome = nativeHealthTransient
	}
	return errors.Join(
		a.settleNativeHealth(ctx, p, a.healthReceipt(t, c), outcome, nativeHealthCooldown{}),
		a.finishNativeFamilyTransport(ctx, p, t, c),
	)
}

func (a *inferenceAdapter) nativeHealthPolicy(t inference.Target) nativeHealthPolicy {
	policy := defaultNativeHealthPolicy()
	a.mu.Lock()
	selection := a.selections[selectionKey(t)]
	a.mu.Unlock()
	if selection.pool == nil || selection.pool.NativeOptions == nil {
		return policy
	}
	o := selection.pool.NativeOptions
	if o.UpstreamFailoverThreshold != nil {
		policy.Threshold = int(*o.UpstreamFailoverThreshold)
	}
	if o.FailureWindow != nil {
		policy.FailureWindow = *o.FailureWindow
	}
	policy.DetourPinned = o.GetPinnedTransientPolicy() == v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_DETOUR_NEW_THREADS
	return policy
}

// Called after the selector actually chooses a fresh detour, before dispatch.
func (a *inferenceAdapter) captureNativeHealthDetour(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential) error {
	a.mu.Lock()
	s := a.native[selectionKey(t)]
	a.mu.Unlock()
	if !s.HealthDetour || !s.Preserve {
		return nil
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	return inferenceError(a.s.UpdateNativeAffinityHold(ctx, l, s.Group, s.Version, s.Session, s.Epoch, s.HealthHoldSince, c.AccountID))
}

func nativeHealthAdmission(h store.NativeHealth, state nativeHealthState, p nativeHealthPolicy) store.NativeHealth {
	h.Failures = state.Failures
	h.WindowStartedAt = time.Time{}
	h.CooldownUntil = time.Time{}
	h.AvoidUntil = time.Time{}
	if state.LastFailure != 0 {
		h.WindowStartedAt = time.UnixMilli(state.LastFailure)
	}
	if p.Threshold > 0 {
		if state.AvoidUntil != 0 {
			h.AvoidUntil = time.UnixMilli(state.AvoidUntil)
		}
		if state.Failures >= p.Threshold && state.LastFailure != 0 {
			h.CooldownUntil = time.UnixMilli(state.LastFailure).Add(nativeHealthFailureGap + time.Millisecond)
		}
	}
	return h
}
func (a *inferenceAdapter) reconcileNativeHealthPolicy(ctx context.Context, l store.Lease, c store.SelectionCandidate, p nativeHealthPolicy) error {
	key := nativeHealthKey(c, store.NativeHealthTransient, "")
	h, e := a.s.GetNativeHealth(ctx, l, key)
	if e != nil {
		return inferenceError(e)
	}
	state, e := decodeNativeHealth(h)
	if e != nil {
		return e
	}
	next := nativeHealthAdmission(h, state, p)
	if next.CooldownUntil.Equal(h.CooldownUntil) && next.AvoidUntil.Equal(h.AvoidUntil) {
		return nil
	}
	return inferenceError(a.s.UpdateNativeHealth(ctx, l, key, func(h store.NativeHealth) (store.NativeHealth, error) {
		state, e := decodeNativeHealth(h)
		if e != nil {
			return h, e
		}
		return nativeHealthAdmission(h, state, p), nil
	}))
}

func nativeHealthWorkspaceDenied(f inference.HTTPFailure) bool {
	if f.Status != 403 || len(f.Body) > 1<<20 {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(f.Body, &root) != nil {
		return false
	}
	code := ""
	_ = json.Unmarshal(root["code"], &code)
	for _, field := range []string{"error", "detail"} {
		if code != "" {
			break
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(root[field], &nested) == nil {
			_ = json.Unmarshal(nested["code"], &code)
		}
	}
	switch code {
	case "codex_workspace_access_denied", "workspace_access_denied", "codex_entitlement_missing", "entitlement_missing":
		return true
	}
	return false
}
