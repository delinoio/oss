// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func nativeFixture(t *testing.T) (*Store, Lease, string) {
	t.Helper()
	s := testStore(t)
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(context.Background(), sc, "provider", "native", []byte("secret")); e != nil {
		t.Fatal(e)
	}
	_, v, e := s.GetPrivateVersion(context.Background(), sc, "provider", "native")
	if e != nil {
		t.Fatal(e)
	}
	return s, l, v
}
func TestNativeQuotaSharedOrderedMergeAndVersionFence(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	other := leaseFor(t, s, l.Principal.Scope)
	if _, e := s.GetNativeQuota(ctx, l, "native", v); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	type evidence struct {
		Observed int `json:"observed"`
		Updates  int `json:"updates"`
	}
	var wg sync.WaitGroup
	for n := 1; n <= 20; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e := s.UpdateNativeQuota(ctx, other, "native", v, func(old []byte) ([]byte, error) {
				var q evidence
				if old != nil {
					if e := json.Unmarshal(old, &q); e != nil {
						return nil, e
					}
				}
				if n > q.Observed {
					q.Observed = n
				}
				q.Updates++
				return json.Marshal(q)
			})
			if e != nil {
				t.Error(e)
			}
		}(n)
	}
	wg.Wait()
	b, e := s.GetNativeQuota(ctx, l, "native", v)
	if e != nil {
		t.Fatal(e)
	}
	var q evidence
	if e = json.Unmarshal(b, &q); e != nil || q.Observed != 20 || q.Updates != 20 {
		t.Fatal(string(b), e)
	}
	sentinel := errors.New("callback failure")
	if e = s.UpdateNativeQuota(ctx, l, "native", v, func([]byte) ([]byte, error) { return nil, sentinel }); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	if e = s.UpdateNativeQuota(ctx, l, "native", v, func([]byte) ([]byte, error) { return []byte(`"` + strings.Repeat("x", 16384) + `"`), nil }); !errors.Is(e, ErrConflict) {
		t.Fatal("size", e)
	}
	if e = s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetNativeQuota(ctx, other, "native", v); e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".native_quotas SET expires_at=clock_timestamp()-interval '1 second'")
	if _, e = s.GetNativeQuota(ctx, l, "native", v); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.UpdateNativeQuota(ctx, l, "native", v, func(old []byte) ([]byte, error) {
		if old != nil {
			t.Error("expired evidence supplied")
		}
		return []byte(`{}`), nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetNativeQuota(ctx, l, "native", v); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.UpdateNativeQuota(ctx, l, "native", v, func([]byte) ([]byte, error) { t.Error("stale callback ran"); return []byte(`{}`), nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestNativeAffinityEpochExpiryAndIsolation(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	get := func(lease Lease) NativeAffinity {
		t.Helper()
		a, e := s.GetNativeAffinity(ctx, lease, "codex:pool", "fingerprint", "session")
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	finish := func(epoch int64, success bool) error {
		return s.FinishNativeAffinity(ctx, l, "codex:pool", "fingerprint", "session", "native", v, epoch, success)
	}
	a := get(l)
	if a.Epoch <= 0 || a.AccountID != "" {
		t.Fatal(a)
	}
	if e := finish(a.Epoch, false); e != nil {
		t.Fatal(e)
	}
	if e := finish(a.Epoch, true); !errors.Is(e, ErrConflict) {
		t.Fatal("failure resurrected", e)
	}
	a = get(l)
	if e := finish(a.Epoch, true); e != nil {
		t.Fatal(e)
	}
	bound := get(l)
	if bound.AccountID != "native" || bound.CredentialVersion != v {
		t.Fatal(bound)
	}
	other := leaseFor(t, s, l.Principal.Scope)
	if a := get(other); a.AccountID != "" || a.Epoch == bound.Epoch {
		t.Fatal("keys share affinity", a)
	}
	if e := finish(bound.Epoch, false); e != nil {
		t.Fatal(e)
	}
	if e := finish(bound.Epoch, true); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	a = get(l)
	execSQL(t, s, "UPDATE "+s.temp+".native_affinities SET expires_at=clock_timestamp()-interval '1 second'")
	if e := finish(a.Epoch, true); !errors.Is(e, ErrConflict) {
		t.Fatal("expired completion recreated", e)
	}
	fresh := get(l)
	if fresh.Epoch == a.Epoch {
		t.Fatal("expired epoch reused")
	}
	if e := finish(fresh.Epoch, true); e != nil {
		t.Fatal(e)
	}
	if e := s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("new")); e != nil {
		t.Fatal(e)
	}
	if a := get(l); a.AccountID != "" || a.Epoch == fresh.Epoch {
		t.Fatal("stale binding retained", a)
	}
	if e := finish(fresh.Epoch, true); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.Release(ctx, l); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetNativeAffinity(ctx, l, "codex:pool", "fingerprint", "session"); !errors.Is(e, ErrForbidden) {
		t.Fatal("released lease", e)
	}
}

func TestNativeAffinityConcurrentFailureCannotResurrect(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	for n := 0; n < 12; n++ {
		a, e := s.GetNativeAffinity(ctx, l, "anthropic", "pool-version", "session")
		if e != nil {
			t.Fatal(e)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, success := range []bool{true, false} {
			go func(success bool) {
				<-start
				results <- s.FinishNativeAffinity(ctx, l, "anthropic", "pool-version", "session", "native", v, a.Epoch, success)
			}(success)
		}
		close(start)
		for i := 0; i < 2; i++ {
			if e := <-results; e != nil && !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
		}
		next, e := s.GetNativeAffinity(ctx, l, "anthropic", "pool-version", "session")
		if e != nil || next.AccountID != "" || next.Epoch == a.Epoch {
			t.Fatal("late success restored failed affinity", next, e)
		}
	}
}

func TestNativeAffinitySuccessCounter(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	a, e := s.GetNativeAffinity(ctx, l, "codex", "pool", "active")
	if e != nil || a.Successes != 0 {
		t.Fatal(a, e)
	}
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.FinishNativeAffinity(ctx, l, "codex", "pool", "active", "native", v, a.Epoch, true); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	counted, e := s.GetNativeAffinity(ctx, l, "codex", "pool", "active")
	if e != nil || counted.Successes != 12 || counted.Epoch != a.Epoch {
		t.Fatal(counted, e)
	}
	if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "second", []byte("secret")); e != nil {
		t.Fatal(e)
	}
	_, second, e := s.GetPrivateVersion(ctx, l.Principal.Scope, "provider", "second")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishNativeAffinity(ctx, l, "codex", "pool", "active", "second", second, a.Epoch, true); e != nil {
		t.Fatal(e)
	}
	counted, e = s.GetNativeAffinity(ctx, l, "codex", "pool", "active")
	if e != nil || counted.AccountID != "second" || counted.Successes != 1 {
		t.Fatal(counted, e)
	}
	if e = s.FinishNativeAffinity(ctx, l, "codex", "pool", "active", "second", second, a.Epoch, false); e != nil {
		t.Fatal(e)
	}
	counted, e = s.GetNativeAffinity(ctx, l, "codex", "pool", "active")
	if e != nil || counted.Successes != 0 || counted.AccountID != "" || counted.Epoch == a.Epoch {
		t.Fatal(counted, e)
	}
	if e = s.FinishNativeAffinity(ctx, l, "codex", "pool", "active", "native", v, a.Epoch, true); !errors.Is(e, ErrConflict) {
		t.Fatal("late completion resurrected counter", e)
	}
}

func TestAccountSoftDeniedExactVersionModelAndExpiry(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	if denied, e := s.AccountSoftDenied(ctx, l, "native", v, "model"); e != nil || denied {
		t.Fatal(denied, e)
	}
	if e := s.SetAccountSoftDenial(ctx, l, "native", v, "model"); e != nil {
		t.Fatal(e)
	}
	if denied, e := s.AccountSoftDenied(ctx, l, "native", v, "model"); e != nil || !denied {
		t.Fatal(denied, e)
	}
	if denied, e := s.AccountSoftDenied(ctx, l, "native", v, "other"); e != nil || denied {
		t.Fatal(denied, e)
	}
	execSQL(t, s, "UPDATE account_soft_denials SET until_at=clock_timestamp()-interval '1 second'")
	if denied, e := s.AccountSoftDenied(ctx, l, "native", v, "model"); e != nil || denied {
		t.Fatal(denied, e)
	}
	if e := s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AccountSoftDenied(ctx, l, "native", v, "model"); !errors.Is(e, ErrForbidden) {
		t.Fatal("stale credential read", e)
	}
	_, next, e := s.GetPrivateVersion(ctx, l.Principal.Scope, "provider", "native")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Release(ctx, l); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AccountSoftDenied(ctx, l, "native", next, "model"); !errors.Is(e, ErrForbidden) {
		t.Fatal("released lease read", e)
	}
}
