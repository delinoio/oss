// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/pricing"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"strconv"
	"sync"
	"time"
)

// An adapter is allocated per HTTP request or WebSocket. It never caches authorization.
// WebSocket execution admits one active turn at a time; each turn replaces its lease.
type inferenceAdapter struct {
	contextSession, contextReceipt string
	compactionHandoff              *compactionHandoffDispatch
	native                         map[string]nativeSelectionState
	jev                            *jevRequestPin
	s                              *store.Store
	telemetry                      *observability.Telemetry
	mu                             sync.Mutex
	lease                          store.Lease
	connections                    map[string]providerData
	prices                         *pricing.Snapshot
	selections                     map[string]routeSelection
	versions                       map[string]string
	activeAccount, activeVersion   string
	activeTarget                   inference.Target
	mistral                        map[string]mistralWait
	releaseCompleted               func(store.Lease, func(context.Context))
	attempted                      map[string]map[string]bool
}

func inferenceError(e error) error {
	if e == nil {
		return nil
	}
	var public *inference.Error
	if errors.As(e, &public) {
		return public
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	if errors.Is(e, store.ErrReauthentication) {
		return inference.ErrNoCredential
	}
	var limit *store.LimitError
	if errors.As(e, &limit) {
		return &inference.Error{Status: 429, Code: "rate_limited", RetryAfter: limit.RetryAfter}
	}
	if errors.Is(e, store.ErrForbidden) || errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) {
		return inference.ErrUnauthorized
	}
	return inference.ErrUnavailable
}
func (a *inferenceAdapter) Admit(ctx context.Context, raw string) (inference.Principal, func(), error) {
	l, e := a.s.Admit(ctx, raw)
	if e != nil {
		return inference.Principal{}, nil, inferenceError(e)
	}
	prices, e := currentPricingSnapshot(ctx, a.s, a.prices)
	if e != nil {
		a.releaseLease(l, nil)
		return inference.Principal{}, nil, inferenceError(e)
	}
	a.mu.Lock()
	a.prices = prices
	a.lease = l
	// A socket reuses its adapter, but every admitted turn owns new waiters.
	states := make(map[string]mistralWait)
	a.mistral = states
	a.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			a.releaseLease(l, states)
		})
	}

	return inference.Principal{OrganizationID: l.Principal.Scope.OrganizationID, UserID: l.Principal.Scope.UserID, KeyID: l.Principal.KeyID, MembershipGeneration: l.Principal.Scope.Generation}, release, nil
}
func (a *inferenceAdapter) releaseLease(l store.Lease, states map[string]mistralWait) {
	// Snapshot this admission's tickets before release can run asynchronously.
	// An older callback must never inspect the adapter's next turn.
	a.mu.Lock()
	tickets := make([]string, 0, len(states))
	for _, state := range states {
		tickets = append(tickets, state.ticket)
	}
	a.mu.Unlock()
	cleanup := func(ctx context.Context) { a.leaveMistral(ctx, l, tickets) }
	if a.releaseCompleted != nil {
		a.releaseCompleted(l, cleanup)
		return
	}
	// Standalone adapters used by fixtures have no service shutdown owner.
	// Try the idempotent release once; retain failed work rather than lose it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := a.s.Release(ctx, l)
	cancel()
	if err == nil {
		cleanup(context.Background())
		return
	}
	a.telemetry.Event(context.Background(), "admission_release_retry")
	go func() {
		if a.s.ReleaseUntilRecorded(context.Background(), l) == nil {
			cleanup(context.Background())
		}
	}()
}
func (a *inferenceAdapter) current(p inference.Principal) (store.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.lease
	if l.Principal.KeyID != p.KeyID || l.Principal.Scope.OrganizationID != p.OrganizationID || l.Principal.Scope.UserID != p.UserID || l.Principal.Scope.Generation != p.MembershipGeneration {
		return l, inference.ErrUnauthorized
	}
	return l, nil
}
func (a *inferenceAdapter) BeforeSend(ctx context.Context, p inference.Principal) error {
	l, e := a.current(p)
	if e != nil {
		return e
	}
	return inferenceError(a.s.CheckDispatch(ctx, l))
}

// Dispatch is called by the transport around its first bounded plaintext write.
// DNS/TCP/TLS and response headers are outside the organization transaction gate.
func (a *inferenceAdapter) Dispatch(ctx context.Context, p inference.Principal, start func(context.Context) error) error {
	a.mu.Lock()
	session, receipt := a.contextSession, a.contextReceipt
	a.mu.Unlock()
	if session != "" {
		l, e := a.current(p)
		if e != nil {
			return e
		}
		return contextError(a.s.WithClientContextDispatch(ctx, l, session, receipt, start))
	}
	if e := a.awaitMistral(ctx, p); e != nil {
		return e
	}
	l, e := a.current(p)
	if e != nil {
		return e
	}
	a.mu.Lock()
	account, version := a.activeAccount, a.activeVersion
	a.mu.Unlock()
	if handled, err := a.dispatchCompactionHandoff(ctx, p, start); handled {
		return err
	}
	if handled, err := a.dispatchNativeHealth(ctx, p, start); handled {
		return err
	}
	if handled, err := a.dispatchNativeFamily(ctx, p, start); handled {
		return err
	}
	if account != "" {
		return inferenceError(a.s.WithAccountDispatch(ctx, l, account, version, start))
	}
	return inferenceError(a.s.WithDispatch(ctx, l, start))
}

func scopeOf(p inference.Principal) core.Scope {
	return core.Scope{OrganizationID: p.OrganizationID, UserID: p.UserID, Generation: p.MembershipGeneration}
}
func (a *inferenceAdapter) provider(ctx context.Context, sc core.Scope, id string) (providerData, error) {
	d, _, e := a.providerVersion(ctx, sc, id)
	return d, e
}
func (a *inferenceAdapter) providerVersion(ctx context.Context, sc core.Scope, id string) (providerData, string, error) {
	var d providerData
	b, v, e := a.s.GetPrivateVersion(ctx, sc, string(providerKind), id)
	if e != nil {
		return d, "", e
	}
	if e = json.Unmarshal(b, &d); e != nil {
		return d, "", e
	}
	if d.Metadata == nil || d.Metadata.Id != id || d.Metadata.State != v1.ConnectionState_CONNECTION_STATE_READY {
		return d, "", inference.ErrNoCredential
	}
	if d.Metadata.CooldownUntil != nil && d.Metadata.CooldownUntil.AsTime().After(time.Now()) {
		return d, "", inference.ErrNoCredential
	}
	if d.Credential != nil && d.Credential.Scope != sc {
		return d, "", inference.ErrNoCredential
	}
	return d, v, nil
}
func (a *inferenceAdapter) Load(ctx context.Context, p inference.Principal, id string) (inference.Continuation, error) {
	var c inference.Continuation
	b, e := a.s.GetState(ctx, scopeOf(p), core.Responses, id)
	if errors.Is(e, store.ErrContinuation) {
		return c, inference.ErrContinuation
	}
	if e != nil {
		return c, inferenceError(e)
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func (a *inferenceAdapter) Save(ctx context.Context, p inference.Principal, id string, c inference.Continuation) error {
	if e := a.BeforeSend(ctx, p); e != nil {
		return e
	}
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	return a.s.PutState(ctx, scopeOf(p), core.Responses, id, b)
}
func (a *inferenceAdapter) Record(ctx context.Context, p inference.Principal, r inference.UsageRecord) error {
	l, e := a.current(p)
	if e != nil {
		return e
	}
	provider := r.Provider
	a.mu.Lock()
	account := r.AccountID
	if account == "" {
		account = provider
	}
	if d, ok := a.connections[account]; ok {
		provider = d.Metadata.ProviderId
	}
	prices := a.prices
	a.mu.Unlock()
	code := ""
	if r.Canceled {
		code = "canceled"
	} else if r.Status >= 400 {
		code = "upstream_error"
	}
	u := store.Usage{Model: r.Model, Provider: provider, ErrorCode: code, InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	switch r.Outcome {
	case inference.OutcomeSuccess:
		u.Outcome = store.OutcomeSucceeded
	case inference.OutcomeUpstreamError:
		u.Outcome = store.OutcomeUpstreamError
	case inference.OutcomeGatewayError:
		u.Outcome = store.OutcomeGatewayError
	case inference.OutcomeRateLimited:
		u.Outcome = store.OutcomeRateLimited
	case inference.OutcomeCanceled:
		u.Outcome = store.OutcomeCanceled
	default:
		switch {
		case r.Canceled:
			u.Outcome = store.OutcomeCanceled
		case r.Status == 429:
			u.Outcome = store.OutcomeRateLimited
		case r.Status >= 200 && r.Status < 300:
			u.Outcome = store.OutcomeSucceeded
		default:
			u.Outcome = store.OutcomeUnknown
		}
	}

	switch u.Outcome {
	case store.OutcomeUpstreamError, store.OutcomeGatewayError, store.OutcomeRateLimited, store.OutcomeCanceled:
		u.ErrorCode = string(u.Outcome)
	case store.OutcomeUnknown:
		if r.Status >= 400 {
			u.ErrorCode = "unknown"
		}
	}
	if r.Usage.InputTokens == nil || r.Usage.OutputTokens == nil {
		a.telemetry.Event(ctx, "usage_unknown")
	}
	if prices != nil {
		tier := pricing.Tier{}
		if r.ServiceTier != "" {
			tier.Response = &r.ServiceTier
		}
		estimate := prices.Estimate(pricing.Request{Provider: provider, Model: r.Model, Tier: tier, Status: pricing.Reported, Usage: &pricing.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, CachedInputTokens: r.Usage.CacheReadTokens, CacheCreationInputTokens: r.Usage.CacheWriteTokens, CacheCreation1hInputTokens: r.Usage.CacheWrite1hTokens}}, time.Now())
		u.Price, _ = json.Marshal(estimate)
		u.Provenance = estimate.SnapshotID
		if estimate.Known && r.UnmeasuredAttempts == 0 {
			cost := strconv.FormatFloat(estimate.Cost.Total, 'f', 12, 64)
			u.Cost = &cost
			u.PriceKnown = true
		} else {
			a.telemetry.Event(ctx, "cost_unknown")
		}
	}
	a.telemetry.Logger().InfoContext(ctx, "usage processing", "request_id", observability.RequestID(ctx), "inference_request_id", r.RequestID, "usage_id", l.ID)
	healthErr := a.finishNativeHealth(ctx, p, r)
	if healthErr != nil {
		a.telemetry.Event(ctx, "native_health_update_failed")
	}
	nativeErr := a.finishNative(ctx, p, r)
	if nativeErr != nil {
		a.telemetry.Event(ctx, "native_affinity_update_failed")
	}
	pinErr := a.finishJEV(ctx, r)
	if pinErr != nil {
		a.telemetry.Event(ctx, "jev_pin_update_failed")
	}
	e = a.s.RecordUsage(ctx, l, u)
	return errors.Join(e, pinErr, nativeErr, healthErr)
}

var _ inference.Dispatcher = (*inferenceAdapter)(nil)
