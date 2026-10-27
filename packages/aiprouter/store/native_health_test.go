// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNativeHealthReceiptsAndConcurrentSamples(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthTransient}
	p, e := s.AcquireNativeHealthPermit(ctx, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	called := false
	merge := func(h NativeHealth) (NativeHealth, error) { called = true; h.Failures++; return h, nil }
	if e = s.FinishNativeHealthPermit(ctx, l, p, merge); !errors.Is(e, ErrConflict) || called {
		t.Fatal("unsent receipt settled", e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { t.Error("receipt replay sent"); return nil }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	q, e := s.AcquireNativeHealthPermit(ctx, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{q}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	// Both sends precede terminal samples: different captured revisions must not lose observations.
	var wg sync.WaitGroup
	for _, receipt := range []NativeHealthPermit{p, q} {
		wg.Add(1)
		go func(p NativeHealthPermit) {
			defer wg.Done()
			if e := s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { h.Failures++; return h, nil }); e != nil {
				t.Error(e)
			}
		}(receipt)
	}
	wg.Wait()
	h, e := s.GetNativeHealth(ctx, l, k)
	if e != nil || h.Failures != 2 {
		t.Fatal(h, e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, merge); !errors.Is(e, ErrConflict) {
		t.Fatal("duplicate sample", e)
	}
	if e = s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) { h.AvoidUntil = h.Now.Add(time.Hour); return h, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermit(ctx, l, k, false); !errors.Is(e, ErrBusy) {
		t.Fatal("avoid ignored", e)
	}
}
func TestNativeHealthTransientReclaimAndQuotaLineage(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	transient := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthTransient}
	p, e := s.AcquireNativeHealthPermit(ctx, l, transient, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermit(ctx, l, transient, true); !errors.Is(e, ErrBusy) {
		t.Fatal("parallel probe", e)
	}
	execSQL(t, s, `UPDATE native_health SET state=jsonb_set(jsonb_set(state,'{ProbeExpiresAt}',to_jsonb(clock_timestamp()-interval '1 second')),'{LastProbeAt}',to_jsonb(clock_timestamp()-interval '31 seconds')) WHERE lane='transient'`)
	newer, e := s.AcquireNativeHealthPermit(ctx, l, transient, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) {
		t.Error("expired probe mutated newer state")
		return h, nil
	}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ReleaseNativeHealthPermit(ctx, l, p); e != nil {
		t.Fatal(e)
	}
	h, e := s.GetNativeHealth(ctx, l, transient)
	if e != nil || h.ProbeID != newer.ID {
		t.Fatal("old release cleared new claim", h, e)
	}
	if e = s.ReleaseNativeHealthPermit(ctx, l, newer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermit(ctx, l, transient, true); !errors.Is(e, ErrBusy) {
		t.Fatal("grant spacing lost", e)
	}
	quota := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthQuota, Model: "shared"}
	if e = s.UpdateNativeHealth(ctx, l, quota, func(h NativeHealth) (NativeHealth, error) {
		h.CooldownSince = h.Now.Add(-6 * time.Minute)
		h.CooldownUntil = h.Now.Add(time.Hour)
		h.ProbeEligible = true
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	qp, e := s.AcquireNativeHealthPermit(ctx, l, quota, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{qp}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, `UPDATE native_health SET state=jsonb_set(state,'{LastProbeAt}',to_jsonb(clock_timestamp()-interval '1 day')) WHERE lane='quota'`)
	if _, e = s.AcquireNativeHealthPermit(ctx, l, quota, true); !errors.Is(e, ErrBusy) {
		t.Fatal("quota timed reclaim", e)
	}
	if e = s.UpdateNativeHealth(ctx, l, quota, func(h NativeHealth) (NativeHealth, error) { h.Failures++; return h, nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, qp, func(h NativeHealth) (NativeHealth, error) {
		t.Error("stale quota cleared newer cooldown")
		return h, nil
	}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ReleaseNativeHealthPermit(ctx, l, qp); e != nil {
		t.Fatal(e)
	}
	h, e = s.GetNativeHealth(ctx, l, quota)
	if e != nil || h.ProbeID != "" || h.NextProbeAt.Before(h.Now.Add(299*time.Second)) {
		t.Fatal("quota release spacing", h, e)
	}
}
func TestNativeRecoveryBudgetAndAtomicProbeGrant(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	for n := 0; n < 3; n++ {
		if _, e := s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryRetry); e != nil {
			t.Fatal(e)
		}
	}
	at, e := s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryProbe)
	if !errors.Is(e, ErrBusy) || !at.After(time.Now()) {
		t.Fatal(at, e)
	}
	for n := 0; n < 20; n++ {
		if _, e = s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryInitial); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryRetry); e != nil {
		t.Fatal("20 initials did not allow fourth recovery", e)
	}
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthTransient}
	if _, e = s.AcquireNativeHealthPermitForRecovery(ctx, l, k, true, "pool", NativeRecoveryProbe); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	h, e := s.GetNativeHealth(ctx, l, k)
	if e != nil || h.ProbeID != "" || h.ProbeEpoch != 0 {
		t.Fatal("refused budget consumed probe", h, e)
	}
	execSQL(t, s, "UPDATE native_recovery_buckets SET bucket_start=bucket_start-interval '11 seconds'")
	p, e := s.AcquireNativeHealthPermitForRecovery(ctx, l, k, true, "pool", NativeRecoveryProbe)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseNativeHealthPermit(ctx, l, p); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 2; n++ {
		if _, e = s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryRetry); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.ChargeNativeRecovery(ctx, l, "pool", NativeRecoveryRetry); !errors.Is(e, ErrBusy) {
		t.Fatal("probe was not counted", e)
	}
}
func TestQuotaCollectionCadenceRetainsClockAndFencesLateFinish(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	claim, e := s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e = s.CompleteNormalQuotaCollection(ctx, l, "native", v, claim, 5*time.Minute, []byte(`{"observed":1}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second); !errors.Is(e, ErrBusy) {
		t.Fatal("cadence ignored", e)
	}
	execSQL(t, s, "UPDATE quota_collection_claims SET next_at=clock_timestamp()-interval '1 second'")
	transient, e := s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteNormalQuotaCollection(ctx, l, "native", v, transient, 5*time.Minute, nil); e != nil {
		t.Fatal(e)
	}
	b, e := s.GetNativeQuota(ctx, l, "native", v)
	if e != nil || !json.Valid(b) || string(b) != `{"observed":1}` {
		t.Fatal(string(b), e)
	}
	execSQL(t, s, "UPDATE quota_collection_claims SET next_at=clock_timestamp()-interval '1 second'")
	old, e := s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE quota_collection_claims SET claim_until=clock_timestamp()-interval '1 second'")
	newer, e := s.ClaimNormalQuotaCollection(ctx, l, "native", v, 45*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteNormalQuotaCollection(ctx, l, "native", v, old, 5*time.Minute, []byte(`{}`)); !errors.Is(e, ErrConflict) {
		t.Fatal("expired claim completed", e)
	}
	if e = s.CompleteNormalQuotaCollection(ctx, l, "native", v, newer, 5*time.Minute, []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
}

func TestNativeHealthFamilyProbeAndHoldCAS(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "sonnet"}
	if e := s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) {
		h.ProbeEligible = true
		h.CooldownUntil = h.Now.Add(time.Minute)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AcquireNativeHealthPermit(ctx, l, k, false); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if _, e := s.AcquireNativeHealthPermit(ctx, l, k, true); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e := s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) { h.CooldownUntil = h.Now.Add(-time.Second); return h, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AcquireNativeHealthPermit(ctx, l, k, false); !errors.Is(e, ErrBusy) {
		t.Fatal("stale family ordinary send", e)
	}
	p, e := s.AcquireNativeHealthPermit(ctx, l, k, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermit(ctx, l, k, true); !errors.Is(e, ErrBusy) {
		t.Fatal("duplicate family probe", e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { h.ProbeEligible = false; return h, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermit(ctx, l, k, false); e != nil {
		t.Fatal(e)
	}
	a, e := s.GetNativeAffinity(ctx, l, "group", "version", "session")
	if e != nil {
		t.Fatal(e)
	}
	hold := time.Now().UTC().Truncate(time.Microsecond)
	if e = s.UpdateNativeAffinityHold(ctx, l, "group", "version", "session", a.Epoch, hold, "native"); e != nil {
		t.Fatal(e)
	}
	a, e = s.GetNativeAffinity(ctx, l, "group", "version", "session")
	if e != nil || !a.HoldSince.Equal(hold) || a.DetourAccountID != "native" {
		t.Fatal(a, e)
	}
	if e = s.UpdateNativeAffinityHold(ctx, l, "group", "version", "session", a.Epoch+1, time.Time{}, ""); !errors.Is(e, ErrConflict) {
		t.Fatal("stale hold CAS", e)
	}
}
func TestNativeRecoveryRetryChargeAtDispatchAndReceiptOwner(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthTransient}
	p, e := s.AcquireNativeHealthPermitForRecovery(ctx, l, k, false, "retry-pool", NativeRecoveryRetry)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if _, e = s.ChargeNativeRecovery(ctx, l, "retry-pool", NativeRecoveryProbe); e != nil {
			t.Fatal(e)
		}
	}
	sent := false
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { sent = true; return nil }); !errors.Is(e, ErrBusy) || sent {
		t.Fatal("retry budget bypassed", e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { t.Error("unsent exhausted retry settled"); return h, nil }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	other := leaseFor(t, s, l.Principal.Scope)
	if e = s.ReleaseNativeHealthPermit(ctx, other, p); !errors.Is(e, ErrConflict) {
		t.Fatal("other admission stole receipt", e)
	}
	if e = s.ReleaseNativeHealthPermit(ctx, l, p); e != nil {
		t.Fatal(e)
	}
}

func TestNativeFamilyNewObservationInvalidatesOlderProbe(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "opus"}
	if e := s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) {
		h.ProbeEligible = true
		h.Payload = []byte(`{"observed":1}`)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	p, e := s.AcquireNativeHealthPermit(ctx, l, k, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) {
		h.CooldownUntil = h.Now.Add(time.Minute)
		h.Payload = []byte(`{"observed":2}`)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	h, e := s.GetNativeHealth(ctx, l, k)
	if e != nil || h.ProbeID != "" {
		t.Fatal("new refusal retained old claim", h, e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) {
		t.Error("older probe cleared newer refusal")
		return h, nil
	}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestNativeHealthReplacementFencesPhysicalSend(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthTransient}
	p, e := s.AcquireNativeHealthPermit(ctx, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("new")); e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { t.Error("old generation physically sent"); return nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { t.Error("old receipt mutated state"); return h, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}

func TestNativeFamilyOrdinaryLateSuccessDoesNotClearRefusal(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "fable"}
	first, e := s.AcquireNativeHealthPermit(ctx, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.AcquireNativeHealthPermit(ctx, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []NativeHealthPermit{first, second} {
		if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.FinishNativeHealthPermit(ctx, l, second, func(h NativeHealth) (NativeHealth, error) {
		h.ProbeEligible = true
		h.CooldownUntil = h.Now.Add(time.Minute)
		return h, nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, first, func(h NativeHealth) (NativeHealth, error) {
		t.Error("late success callback ran")
		h.ProbeEligible = false
		h.CooldownUntil = time.Time{}
		return h, nil
	}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	h, e := s.GetNativeHealth(ctx, l, k)
	if e != nil || !h.ProbeEligible || !h.CooldownUntil.After(h.Now) {
		t.Fatal(h, e)
	}
}
