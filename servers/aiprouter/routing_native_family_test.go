// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

func nativeFamilyFixture(t *testing.T) (*inferenceAdapter, inference.Principal, inference.Target, inference.Credential) {
	t.Helper()
	a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	ctx := context.Background()
	d, e := a.provider(ctx, sc, "a")
	if e != nil {
		t.Fatal(e)
	}
	d.Metadata.ProviderId = "anthropic"
	d.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	d.Credential = &providers.Credential{Provider: "anthropic", Scope: sc, AccessToken: "fixture", AccountID: "a"}
	putRoutingProvider(t, a, sc, d)
	p := admitRouting(t, a, key)
	_, version, e := a.s.GetPrivateVersion(ctx, sc, "provider", "a")
	if e != nil {
		t.Fatal(e)
	}
	target := inference.Target{Provider: "logical", Auth: inference.OAuth, Model: "claude-fable-5"}
	c := inference.Credential{AccountID: "a", CredentialVersion: version}
	a.activeTarget = target
	a.activeAccount = "a"
	a.activeVersion = version
	a.connections = map[string]providerData{"a": d}
	route := &v1.Route{Id: "r", ModelId: "model", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "a"}, ModelId: target.Model}}}
	putRoutingRoute(t, a, sc, route)
	a.native = map[string]nativeSelectionState{selectionKey(target): {ConfigGroup: "route/r", Version: selectionVersion(route)}}
	return a, p, target, c
}
func nativeFamilyInstance(a *inferenceAdapter) *inferenceAdapter {
	return &inferenceAdapter{s: a.s, lease: a.lease, connections: a.connections, activeTarget: a.activeTarget, activeAccount: a.activeAccount, activeVersion: a.activeVersion, native: map[string]nativeSelectionState{selectionKey(a.activeTarget): {ConfigGroup: a.native[selectionKey(a.activeTarget)].ConfigGroup, Version: a.native[selectionKey(a.activeTarget)].Version}}}
}
func TestNativeFamilyResetSingleFlightAndSuccess(t *testing.T) {
	a, p, target, c := nativeFamilyFixture(t)
	ctx := context.Background()
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
	h.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10))
	if e := a.observeNativeFamily(ctx, p, target, c, 429, h); e != nil {
		t.Fatal(e)
	}
	selected := store.SelectionCandidate{ID: c.AccountID, Version: c.CredentialVersion}
	if allowed, e := a.nativeFamilyEligible(ctx, p, selected, target.Model); e != nil || !allowed {
		t.Fatalf("expired reset remains held: %v %v", allowed, e)
	}
	sent := 0
	if handled, e := a.dispatchNativeFamily(ctx, p, func(context.Context) error { sent++; return nil }); e != nil || !handled || sent != 1 {
		t.Fatalf("first revalidation %v %v", handled, e)
	}
	other := nativeFamilyInstance(a)
	if _, e := other.dispatchNativeFamily(ctx, p, func(context.Context) error { sent++; return nil }); e == nil || sent != 1 {
		t.Fatal("second instance sent during singleflight")
	}
	if e := a.observeNativeFamily(ctx, p, target, c, 200, http.Header{}); e != nil {
		t.Fatal(e)
	}
	if handled, e := other.dispatchNativeFamily(ctx, p, func(context.Context) error { sent++; return nil }); e != nil || !handled || sent != 2 {
		t.Fatalf("success did not reopen family: %v", e)
	}
	if e := other.observeNativeFamily(ctx, p, target, c, 200, http.Header{}); e != nil {
		t.Fatal(e)
	}
	h.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	if e := a.observeNativeFamily(ctx, p, target, c, 429, h); e != nil {
		t.Fatal(e)
	}
	if allowed, e := a.nativeFamilyEligible(ctx, p, selected, target.Model); e != nil || allowed {
		t.Fatal("fresh refusal did not hold family")
	}
	if allowed, e := a.nativeFamilyEligible(ctx, p, selected, "claude-sonnet-4"); e != nil || !allowed {
		t.Fatal("Fable refusal leaked to Sonnet")
	}
}
func TestNativeFamilyLateSuccessCannotClearNewRefusal(t *testing.T) {
	a, p, target, c := nativeFamilyFixture(t)
	ctx := context.Background()
	other := nativeFamilyInstance(a)
	for _, instance := range []*inferenceAdapter{a, other} {
		if _, e := instance.dispatchNativeFamily(ctx, p, func(context.Context) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
	if e := other.observeNativeFamily(ctx, p, target, c, 429, h); e != nil {
		t.Fatal(e)
	}
	if e := a.observeNativeFamily(ctx, p, target, c, 200, http.Header{}); e != nil {
		t.Fatal(e)
	}
	lease, e := a.current(p)
	if e != nil {
		t.Fatal(e)
	}
	health, e := a.s.GetNativeHealth(ctx, lease, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"))
	if e != nil || !health.ProbeEligible || !health.CooldownUntil.After(health.Now) {
		t.Fatalf("late success erased newer family refusal: %+v %v", health, e)
	}
}

// Exercise the public dispatch entry point, including disabled native pools:
// family admission must not bypass the captured route/pool configuration.
func TestNativeFamilyConfigurationFences(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(strconv.FormatBool(pooled), func(t *testing.T) {
			a, p, target, c := nativeFamilyFixture(t)
			ctx := context.Background()
			route := &v1.Route{Id: "r", ModelId: "model", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: "a"}, ModelId: target.Model}}}
			state := nativeSelectionState{ConfigGroup: "route/r", Version: selectionVersion(route)}
			var pool v1.Pool
			if pooled {
				raw, e := a.s.GetPrivate(ctx, scopeOf(p), string(poolKind), "pool")
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(raw, &pool); e != nil {
					t.Fatal(e)
				}
				route.Targets[0].Target = &v1.RouteTarget_PoolId{PoolId: "pool"}
				state.ConfigGroup += "/pool/pool"
				state.Version = selectionVersion(struct {
					Route *v1.Route
					Pool  *v1.Pool
				}{route, &pool})
			}
			putRoutingRoute(t, a, scopeOf(p), route)
			a.native[selectionKey(target)] = state
			if pooled {
				// Disabled proactive native selection has no affinity state;
				// physical admission still binds the captured routing request.
				a.selections = map[string]routeSelection{selectionKey(target): {request: store.SelectionRequest{GroupID: state.ConfigGroup, Version: state.Version}, pool: &pool}}
				a.native = map[string]nativeSelectionState{}
			}
			change := func() {
				if pooled {
					pool.Members = nil
					raw, _ := json.Marshal(&pool)
					if e := a.s.PutPrivate(ctx, scopeOf(p), string(poolKind), pool.Id, raw); e != nil {
						t.Error(e)
					}
				} else {
					route.Enabled = false
					putRoutingRoute(t, a, scopeOf(p), route)
				}
			}
			// A sent response cannot publish recovery after its captured config changes.
			h := http.Header{}
			h.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
			h.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10))
			if e := a.observeNativeFamily(ctx, p, target, c, 429, h); e != nil {
				t.Fatal(e)
			}
			entered := make(chan struct{})
			proceed := make(chan struct{})
			sent := make(chan error, 1)
			go func() {
				sent <- a.Dispatch(ctx, p, func(context.Context) error { close(entered); <-proceed; return nil })
			}()
			select {
			case <-entered:
			case e := <-sent:
				t.Fatalf("dispatch failed before write: %v", e)
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch did not enter write")
			}
			updated := make(chan struct{})
			go func() { change(); close(updated) }()
			select {
			case <-updated:
				t.Fatal("configuration update crossed physical dispatch transaction")
			case <-time.After(25 * time.Millisecond):
			}
			close(proceed)
			if e := <-sent; e != nil {
				t.Fatal(e)
			}
			<-updated
			if e := a.observeNativeFamily(ctx, p, target, c, 200, http.Header{}); e == nil {
				t.Fatal("changed configuration accepted completion")
			}
			l, e := a.current(p)
			if e != nil {
				t.Fatal(e)
			}
			health, e := a.s.GetNativeHealth(ctx, l, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"))
			if e != nil || !health.ProbeEligible {
				t.Fatalf("stale completion cleared family: %+v %v", health, e)
			}
			h.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			if e := a.observeNativeFamily(ctx, p, target, c, 429, h); e == nil {
				t.Fatal("changed configuration accepted refusal")
			}
			after, e := a.s.GetNativeHealth(ctx, l, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"))
			if e != nil || !after.CooldownUntil.Equal(health.CooldownUntil) {
				t.Fatal("stale refusal changed family cooldown")
			}
			// Remove the completed probe claim so it cannot mask a missing
			// configuration gate on the next physical send.
			if e := a.releaseTerminalNativeHealthPermit(ctx, l, a.native[selectionKey(target)].Family.Permit); e != nil {
				t.Fatal(e)
			}
			count := 0
			if e := a.Dispatch(ctx, p, func(context.Context) error { count++; return nil }); e == nil || count != 0 {
				t.Fatalf("changed configuration dispatched: %d %v", count, e)
			}
		})
	}
}

func TestNativeFamilyTerminalNetworkFailureReleasesProbe(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelRequest), func(t *testing.T) {
			a, p, target, c := nativeFamilyFixture(t)
			ctx := context.Background()
			h := http.Header{}
			h.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
			h.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10))
			if e := a.observeNativeFamily(ctx, p, target, c, 429, h); e != nil {
				t.Fatal(e)
			}
			networkCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			handlerDone := make(chan struct{})
			policy := nativeHealthUsagePolicy(t, func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				if cancelRequest {
					cancel()
					<-r.Context().Done()
					return
				}
				conn, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				_ = conn.Close()
			})
			networkCtx = inference.WithPhysicalSendBudget(networkCtx, func(ctx context.Context, start func(context.Context) error) error { return start(ctx) })
			req, e := http.NewRequestWithContext(networkCtx, http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
			if e != nil {
				t.Fatal(e)
			}
			resp, e := policy.Do(networkCtx, req, func(ctx context.Context, start func(context.Context) error) error { return a.Dispatch(ctx, p, start) }, nil)
			if resp != nil {
				resp.Body.Close()
			}
			if e == nil {
				t.Fatal("expected terminal network failure")
			}
			select {
			case <-handlerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream handler did not terminate")
			}
			l, e := a.current(p)
			if e != nil {
				t.Fatal(e)
			}
			if cancelRequest {
				// The immediate observer must detach from caller cancellation.
				if e := a.ObserveTransportFailure(networkCtx, p, target, c, inference.TransportFailure{Sent: true, Kind: inference.TransportFailureCanceled}); e != nil {
					t.Fatal(e)
				}
				h, e := a.s.GetNativeHealth(ctx, l, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"))
				if e != nil || h.ProbeID != "" || !h.ProbeEligible {
					t.Fatalf("terminal observer failed cleanup: %+v %v", h, e)
				}
			}
			// The no-header branch deliberately skips immediate observation:
			// durable admission release must retain and settle the obligation.
			a.releaseLease(l)
			_, rawKey, e := a.s.IssueKey(ctx, scopeOf(p), "next-family", nil)
			if e != nil {
				t.Fatal(e)
			}
			next, e := a.s.Admit(ctx, rawKey)
			if e != nil {
				t.Fatal(e)
			}
			defer a.s.Release(ctx, next)
			health, e := a.s.GetNativeHealth(ctx, next, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"))
			if e != nil || health.ProbeID != "" || !health.ProbeEligible {
				t.Fatalf("terminal probe orphan or invented recovery: %+v %v", health, e)
			}
			permit, e := a.s.AcquireNativeHealthPermit(ctx, next, nativeFamilyKey(c.AccountID, c.CredentialVersion, "fable"), true)
			if e != nil {
				t.Fatalf("next admission cannot revalidate: %v", e)
			}
			if e = a.s.ReleaseNativeHealthPermit(ctx, next, permit); e != nil {
				t.Fatal(e)
			}
		})
	}
}
