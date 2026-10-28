// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func TestNativeHealthDurableSamplesReceiptAndGeneration(t *testing.T) {
	a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	l, e := a.current(p)
	if e != nil {
		t.Fatal(e)
	}
	_, version, e := a.s.GetPrivateVersion(ctx, sc, "provider", "a")
	if e != nil {
		t.Fatal(e)
	}
	hk := nativeHealthKey(store.SelectionCandidate{ID: "a", Version: version}, store.NativeHealthTransient, "")
	// Twenty independent physical sends can settle concurrently, including across
	// adapters. Every terminal sample must survive the serialized merge.
	receipts := make([]*nativeHealthReceipt, 20)
	for i := range receipts {
		permit, e := a.s.AcquireNativeHealthPermit(ctx, l, hk, false)
		if e != nil {
			t.Fatal(e)
		}
		receipts[i] = &nativeHealthReceipt{Account: "a", Version: version, Policy: defaultNativeHealthPolicy(), Permits: []store.NativeHealthPermit{permit}}
		if e = a.s.WithNativeHealthDispatch(ctx, l, receipts[i].Permits, func(context.Context) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	for i, r := range receipts {
		wg.Add(1)
		go func(i int, r *nativeHealthReceipt) {
			defer wg.Done()
			other := &inferenceAdapter{s: a.s, lease: l}
			outcome := nativeHealthSuccess
			if i < 5 {
				outcome = nativeHealthTransient
			}
			if e := other.settleNativeHealth(ctx, p, r, outcome, nativeHealthCooldown{}); e != nil {
				t.Error(e)
			}
		}(i, r)
	}
	wg.Wait()
	h, e := a.s.GetNativeHealth(ctx, l, hk)
	if e != nil {
		t.Fatal(e)
	}
	state, e := decodeNativeHealth(h)
	if e != nil || len(state.Samples) != 20 || !state.Degraded {
		t.Fatalf("durable samples %+v %v", state, e)
	}
	permit, e := a.s.AcquireNativeHealthPermit(ctx, l, nativeHealthKey(store.SelectionCandidate{ID: "b", Version: mustNativeVersion(t, a, p, "b")}, store.NativeHealthTransient, ""), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.s.FinishNativeHealthPermit(ctx, l, permit, func(h store.NativeHealth) (store.NativeHealth, error) {
		t.Error("unsent outcome callback ran")
		return h, nil
	}); !errors.Is(e, store.ErrConflict) {
		t.Fatalf("unsent outcome: %v", e)
	}
	d, e := a.provider(ctx, sc, "a")
	if e != nil {
		t.Fatal(e)
	}
	d.Secret = "replacement"
	putRoutingProvider(t, a, sc, d)
	if _, e = a.s.GetNativeHealth(ctx, l, hk); e == nil {
		t.Fatal("retired credential read health")
	}
}
func mustNativeVersion(t *testing.T, a *inferenceAdapter, p inference.Principal, id string) string {
	t.Helper()
	_, v, e := a.s.GetPrivateVersion(context.Background(), scopeOf(p), "provider", id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestNativeHealthHoldDetourAndSingleProbe(t *testing.T) {
	a, _, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	l, _ := a.current(p)
	c := store.SelectionCandidate{ID: "a", Version: mustNativeVersion(t, a, p, "a")}
	b := store.SelectionCandidate{ID: "b", Version: mustNativeVersion(t, a, p, "b")}
	hk := nativeHealthKey(c, store.NativeHealthTransient, "")
	if e := a.s.UpdateNativeHealth(ctx, l, hk, func(h store.NativeHealth) (store.NativeHealth, error) {
		s := nativeHealthState{}
		for i := 0; i < 3; i++ {
			s.note(nativeHealthTransient, defaultNativeHealthPolicy(), h.Now)
		}
		h.Payload, _ = json.Marshal(s)
		h.CooldownUntil = h.Now.Add(5 * time.Minute)
		h.AvoidUntil = h.Now.Add(30 * time.Second)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	affinity, e := a.s.GetNativeAffinity(ctx, l, "native/openai/pool", "v", "session")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.s.FinishNativeAffinity(ctx, l, "native/openai/pool", "v", "session", c.ID, c.Version, affinity.Epoch, true); e != nil {
		t.Fatal(e)
	}
	affinity, e = a.s.GetNativeAffinity(ctx, l, "native/openai/pool", "v", "session")
	if e != nil {
		t.Fatal(e)
	}
	s := nativeSelectionState{Group: "native/openai/pool", Version: "v", Session: "session", Epoch: affinity.Epoch}
	cs, handled, e := a.resolveNativeHealthAffinity(ctx, p, inference.Target{}, "openai", &s, affinity, []store.SelectionCandidate{c, b})
	if e != nil || !handled || len(cs) != 1 || cs[0].ID != "b" || !s.Preserve || !s.HealthDetour {
		t.Fatalf("fresh detour %+v state%+v %v", cs, s, e)
	}
	if e = a.s.UpdateNativeAffinityHold(ctx, l, s.Group, s.Version, s.Session, s.Epoch, s.HealthHoldSince, "b"); e != nil {
		t.Fatal(e)
	}
	affinity, e = a.s.GetNativeAffinity(ctx, l, s.Group, s.Version, s.Session)
	if e != nil {
		t.Fatal(e)
	}
	if affinity.AccountID != "a" || affinity.DetourAccountID != "b" || affinity.HoldSince.IsZero() {
		t.Fatalf("binding lost: %+v", affinity)
	}
	other := &inferenceAdapter{s: a.s, lease: l}
	s2 := s
	s2.HealthDetour = false
	cs, handled, e = other.resolveNativeHealthAffinity(ctx, p, inference.Target{}, "openai", &s2, affinity, []store.SelectionCandidate{c, b})
	if e != nil || !handled || cs[0].ID != "b" || s2.HealthDetour {
		t.Fatalf("remembered detour %+v %v", cs, e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins []store.NativeHealthPermit
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, e := a.s.AcquireNativeHealthPermit(ctx, l, hk, true)
			if e == nil {
				mu.Lock()
				wins = append(wins, permit)
				mu.Unlock()
			} else if !errors.Is(e, store.ErrBusy) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatalf("probe winners %d", len(wins))
	}
	_, handled, e = other.resolveNativeHealthAffinity(ctx, p, inference.Target{}, "openai", &s2, affinity, []store.SelectionCandidate{c})
	var refusal *inference.Error
	if !handled || !errors.As(e, &refusal) || refusal.Code != "native_dispatch_withheld" || refusal.RetryAfter <= 0 {
		t.Fatalf("all held should withhold: %v", e)
	}
	if e = a.s.ReleaseNativeHealthPermit(ctx, l, wins[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = a.s.AcquireNativeHealthPermit(ctx, l, hk, true); !errors.Is(e, store.ErrBusy) {
		t.Fatalf("release bypassed spacing: %v", e)
	}
}
func TestNativeHealthQuotaRevisionCannotClearNewerCooldown(t *testing.T) {
	a, _, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	l, _ := a.current(p)
	c := store.SelectionCandidate{ID: "a", Version: mustNativeVersion(t, a, p, "a")}
	hk := nativeHealthKey(c, store.NativeHealthQuota, "shared")
	set := func(h store.NativeHealth) (store.NativeHealth, error) {
		h.CooldownSince = h.Now.Add(-6 * time.Minute)
		h.CooldownUntil = h.Now.Add(10 * time.Minute)
		h.ProbeEligible = true
		return h, nil
	}
	if e := a.s.UpdateNativeHealth(ctx, l, hk, set); e != nil {
		t.Fatal(e)
	}
	permit, e := a.s.AcquireNativeHealthPermit(ctx, l, hk, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.s.WithNativeHealthDispatch(ctx, l, []store.NativeHealthPermit{permit}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = a.s.UpdateNativeHealth(ctx, l, hk, func(h store.NativeHealth) (store.NativeHealth, error) {
		h.ProbeEligible = false
		h.CooldownUntil = h.Now.Add(time.Hour)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = a.s.FinishNativeHealthPermit(ctx, l, permit, func(h store.NativeHealth) (store.NativeHealth, error) {
		t.Error("stale success callback")
		h.CooldownUntil = time.Time{}
		return h, nil
	}); !errors.Is(e, store.ErrConflict) {
		t.Fatalf("revision fence: %v", e)
	}
	h, e := a.s.GetNativeHealth(ctx, l, hk)
	if e != nil || !h.CooldownUntil.After(h.Now.Add(50*time.Minute)) {
		t.Fatalf("new cooldown lost %+v %v", h, e)
	}
}

func TestNativeHealthUnsentQuotaCannotSuppressLegacyHold(t *testing.T) {
	a, _, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	l, _ := a.current(p)
	c := store.SelectionCandidate{ID: "a", Version: mustNativeVersion(t, a, p, "a")}
	r := &nativeHealthReceipt{Account: c.ID, Version: c.Version, Policy: defaultNativeHealthPolicy()}
	for _, k := range []store.NativeHealthKey{nativeHealthKey(c, store.NativeHealthTransient, ""), nativeHealthKey(c, store.NativeHealthQuota, ""), nativeHealthKey(c, store.NativeHealthQuota, "shared")} {
		permit, e := a.s.AcquireNativeHealthPermit(ctx, l, k, false)
		if e != nil {
			t.Fatal(e)
		}
		r.Permits = append(r.Permits, permit)
	}
	if e := a.settleNativeHealth(ctx, p, r, nativeHealthQuota, nativeHealthQuotaCooldown("60", nil, time.Now())); e != nil {
		t.Fatal(e)
	}
	if r.ManagedQuota {
		t.Fatal("uncommitted quota outcome suppresses legacy hold")
	}
	h, e := a.s.GetNativeHealth(ctx, l, nativeHealthKey(c, store.NativeHealthQuota, ""))
	if e != nil || !h.CooldownUntil.IsZero() {
		t.Fatalf("unsent evidence persisted: %+v %v", h, e)
	}
}

func TestNativeHealthPolicyDisableClearsAdmissionGate(t *testing.T) {
	a, _, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
	p := admitRouting(t, a, key)
	ctx := context.Background()
	l, _ := a.current(p)
	c := store.SelectionCandidate{ID: "a", Version: mustNativeVersion(t, a, p, "a")}
	hk := nativeHealthKey(c, store.NativeHealthTransient, "")
	if e := a.s.UpdateNativeHealth(ctx, l, hk, func(h store.NativeHealth) (store.NativeHealth, error) {
		s := nativeHealthState{}
		for i := 0; i < 3; i++ {
			s.note(nativeHealthTransient, defaultNativeHealthPolicy(), h.Now)
		}
		h = nativeHealthAdmission(h, s, defaultNativeHealthPolicy())
		h.Payload, _ = json.Marshal(s)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.s.AcquireNativeHealthPermit(ctx, l, hk, false); !errors.Is(e, store.ErrBusy) {
		t.Fatalf("healthy permit while held: %v", e)
	}
	disabled := defaultNativeHealthPolicy()
	disabled.Threshold = 0
	if e := a.reconcileNativeHealthPolicy(ctx, l, c, disabled); e != nil {
		t.Fatal(e)
	}
	if _, e := a.s.AcquireNativeHealthPermit(ctx, l, hk, false); e != nil {
		t.Fatalf("disabled failover retained gate: %v", e)
	}
}
