// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNativeConfigBoundDispatchAndCompletion(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	sc := l.Principal.Scope
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "sonnet"}
	if e := s.PutPrivate(ctx, sc, "route", "r", []byte("enabled")); e != nil {
		t.Fatal(e)
	}
	_, rv, e := s.GetPrivateVersion(ctx, sc, "route", "r")
	if e != nil {
		t.Fatal(e)
	}
	bindings := []NativeHealthBinding{{Kind: "route", ID: "r", Version: rv}}
	p, e := s.AcquireNativeHealthPermitBound(ctx, l, k, false, bindings)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	sent := make(chan error, 1)
	go func() {
		sent <- s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { close(entered); <-resume; return nil })
	}()
	<-entered
	changed := make(chan error, 1)
	go func() { changed <- s.PutPrivate(ctx, sc, "route", "r", []byte("disabled")) }()
	select {
	case e := <-changed:
		close(resume)
		t.Fatal("configuration changed inside physical gate", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(resume)
	if e = <-sent; e != nil {
		t.Fatal(e)
	}
	if e = <-changed; e != nil {
		t.Fatal(e)
	}
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { t.Error("stale completion callback ran"); return h, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.UpdateNativeHealthBound(ctx, l, k, bindings, func(h NativeHealth) (NativeHealth, error) { t.Error("stale refusal fallback ran"); return h, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.AcquireNativeHealthPermitBound(ctx, l, k, false, bindings); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	_, rv, e = s.GetPrivateVersion(ctx, sc, "route", "r")
	if e != nil {
		t.Fatal(e)
	}
	bindings[0].Version = rv
	p, e = s.AcquireNativeHealthPermitBound(ctx, l, k, false, bindings)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeletePrivate(ctx, sc, "route", "r"); e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { t.Error("deleted route physically sent"); return nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestTerminalAdmissionReleasesHealthWithoutCurrentAuthority(t *testing.T) {
	for _, lane := range []NativeHealthLane{NativeHealthFamily, NativeHealthQuota} {
		t.Run(string(lane), func(t *testing.T) {
			s, l, v := nativeFixture(t)
			ctx := context.Background()
			model := "fable"
			if lane == NativeHealthQuota {
				model = "shared"
			}
			k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: lane, Model: model}
			if e := s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) {
				h.ProbeEligible = true
				if lane == NativeHealthQuota {
					h.CooldownSince = h.Now.Add(-6 * time.Minute)
					h.CooldownUntil = h.Now.Add(time.Hour)
				}
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
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if e = s.FinishNativeHealthPermit(canceled, l, p, func(h NativeHealth) (NativeHealth, error) { return h, nil }); e == nil {
				t.Fatal("canceled completion accepted")
			}
			// Neither replacement nor revoked keys can strand a caller-confirmed terminal receipt.
			if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("replacement")); e != nil {
				t.Fatal(e)
			}
			execSQL(t, s, "UPDATE api_keys SET revoked=true WHERE id=$1", l.Principal.KeyID)
			if e = s.ReleaseUntilRecorded(ctx, l); e != nil {
				t.Fatal(e)
			}
			if e = s.ReleaseTerminalNativeHealthPermit(ctx, l, p); e != nil {
				t.Fatal("terminal cleanup not idempotent after release", e)
			}
			e = s.tx(ctx, func(tx *sql.Tx) error {
				h, e := s.readHealth(ctx, tx, l, k)
				if e != nil {
					return e
				}
				if h.ProbeID != "" || !h.ProbeEligible {
					t.Fatal("cleanup lost refusal or kept claim", h)
				}
				if lane == NativeHealthQuota && (!h.CooldownUntil.After(h.Now) || h.NextProbeAt.Before(h.Now.Add(299*time.Second))) {
					t.Fatal("quota cleanup lost cooldown/spacing", h)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			other := l
			other.ID = "wrong-admission"
			if e = s.ReleaseTerminalNativeHealthPermit(ctx, other, p); !errors.Is(e, ErrForbidden) {
				t.Fatal("forged terminal owner", e)
			}
		})
	}
}
func TestStoppedInstanceReconcilesProbeClaims(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "opus"}
	if e := s.UpdateNativeHealth(ctx, l, k, func(h NativeHealth) (NativeHealth, error) { h.ProbeEligible = true; return h, nil }); e != nil {
		t.Fatal(e)
	}
	p, e := s.AcquireNativeHealthPermit(ctx, l, k, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithNativeHealthDispatch(ctx, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	proof, e := s.DrainAdmissionInstance(ctx, s.InstanceID())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReconcileTerminatedInstance(ctx, s.InstanceID(), proof, true); e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseTerminalNativeHealthPermit(ctx, l, p); e != nil {
		t.Fatal(e)
	}
	if e = s.tx(ctx, func(tx *sql.Tx) error {
		h, e := s.readHealth(ctx, tx, l, k)
		if e == nil && h.ProbeID != "" {
			t.Fatal("stopped boot kept probe", h)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestVertexProjectClaimCacheAndVersionFence(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	project, claim, e := s.ClaimVertexProject(ctx, l, "native", v)
	if e != nil || project != "" || claim == "" {
		t.Fatal(project, claim, e)
	}
	other := leaseFor(t, s, l.Principal.Scope)
	if _, _, e = s.ClaimVertexProject(ctx, other, "native", v); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e = s.CompleteVertexProject(ctx, l, "native", v, claim, "123456789"); e != nil {
		t.Fatal(e)
	}
	project, claim, e = s.ClaimVertexProject(ctx, other, "native", v)
	if e != nil || project != "123456789" || claim != "" {
		t.Fatal(project, claim, e)
	}
	if e = s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	if project, e = s.GetVertexProject(ctx, other, "native", v); e != nil || project != "123456789" {
		t.Fatal(project, e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".vertex_projects SET expires_at=clock_timestamp()-interval '1 second'")
	_, old, e := s.ClaimVertexProject(ctx, l, "native", v)
	if e != nil {
		t.Fatal(e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".vertex_projects SET claim_until=clock_timestamp()-interval '1 second'")
	_, newer, e := s.ClaimVertexProject(ctx, other, "native", v)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteVertexProject(ctx, l, "native", v, old, "1"); !errors.Is(e, ErrConflict) {
		t.Fatal("old claim cached", e)
	}
	if e = s.CompleteVertexProject(ctx, other, "native", v, newer, ""); e != nil {
		t.Fatal(e)
	}
	_, claim, e = s.ClaimVertexProject(ctx, l, "native", v)
	if e != nil {
		t.Fatal("failure negatively cached", e)
	}
	if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("new")); e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteVertexProject(ctx, l, "native", v, claim, "1"); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
func TestVertexProjectOwnerCacheBound(t *testing.T) {
	s, l, _ := nativeFixture(t)
	ctx := context.Background()
	sc := l.Principal.Scope
	for i := 0; i < 65; i++ {
		id := fmt.Sprint("v", i)
		if e := s.PutPrivate(ctx, sc, "provider", id, []byte("key")); e != nil {
			t.Fatal(e)
		}
		_, v, e := s.GetPrivateVersion(ctx, sc, "provider", id)
		if e != nil {
			t.Fatal(e)
		}
		_, claim, e := s.ClaimVertexProject(ctx, l, id, v)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CompleteVertexProject(ctx, l, id, v, claim, "1"); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM "+s.temp+".vertex_projects").Scan(&n)
	}); e != nil || n != 64 {
		t.Fatal(n, e)
	}
}

func TestCompactionStateRequiresExplicitAccountBinding(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	policy := ProviderStatePolicy{TTL: 24 * time.Hour, Entries: 256, MaxEntryBytes: 32 << 20, MaxTotalBytes: 512 << 20}
	if e := s.PutProviderState(ctx, l.Principal, "compaction-reasoning", "id", ProviderState{Data: []byte("reasoning")}, policy); !errors.Is(e, ErrForbidden) {
		t.Fatal("unbound compaction write", e)
	}
	p := l.Principal
	p.AccountID = "native"
	p.CredentialVersion = v
	if e := s.PutProviderState(ctx, p, "compaction-reasoning", "id", ProviderState{Data: []byte("reasoning")}, policy); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetProviderState(ctx, l.Principal, "compaction-reasoning", "id"); !errors.Is(e, ErrForbidden) {
		t.Fatal("unbound compaction read", e)
	}
	if e := s.DeletePrivate(ctx, l.Principal.Scope, "provider", "native"); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateProviderState(ctx, p, "compaction-reasoning", "id", policy, func([]byte) ([]byte, error) { t.Error("deleted account compaction merge ran"); return nil, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}
