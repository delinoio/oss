// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestCompactionHandoffOwnershipExpiryAndReplacement(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	value := CompactionHandoff{AccountID: "native", CredentialVersion: v, Data: []byte(`{"model":"physical","config":"captured"}`)}
	if e := s.PutCompactionHandoff(ctx, l, "lane", value); e != nil {
		t.Fatal(e)
	}
	other := leaseFor(t, s, l.Principal.Scope)
	if _, e := s.GetCompactionHandoff(ctx, other, "lane"); !errors.Is(e, ErrNotFound) {
		t.Fatal("handoff leaked across keys", e)
	}
	if e := s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetCompactionHandoff(ctx, l, "lane")
	if e != nil || string(got.Data) != string(value.Data) || got.AccountID != value.AccountID {
		t.Fatal(got, e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".compaction_handoffs SET expires_at=clock_timestamp()-interval '1 second'")
	if _, e = s.GetCompactionHandoff(ctx, l, "lane"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.PutCompactionHandoff(ctx, l, "lane", value); e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, l.Principal.Scope, "provider", "native", []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetCompactionHandoff(ctx, l, "lane"); !errors.Is(e, ErrForbidden) {
		t.Fatal("replacement reused old handoff", e)
	}
	if e = s.PutCompactionHandoff(ctx, l, "lane", value); !errors.Is(e, ErrForbidden) {
		t.Fatal("late old success published", e)
	}
	if e = s.ClearCompactionHandoff(ctx, l, "lane"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetCompactionHandoff(ctx, l, "lane"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestCompactionHandoffCapacityAndIdleTouch(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	value := CompactionHandoff{AccountID: "native", CredentialVersion: v, Data: []byte(`{}`)}
	if e := s.PutCompactionHandoff(ctx, l, "lane", value); e != nil {
		t.Fatal(e)
	}
	// Populate eviction candidates without thousands of unrelated authorization transactions.
	execSQL(t, s, "INSERT INTO "+s.temp+".compaction_handoffs(org_id,user_id,key_id,id,generation,value,size,last_used_at,expires_at) SELECT h.org_id,h.user_id,h.key_id,'seed-'||n,h.generation,h.value,h.size,clock_timestamp()-interval '1 hour',h.expires_at FROM "+s.temp+".compaction_handoffs h CROSS JOIN generate_series(1,2048) n WHERE h.id='lane'")
	if e := s.PutCompactionHandoff(ctx, l, "new", value); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM "+s.temp+".compaction_handoffs").Scan(&count)
	}); e != nil || count != 2048 {
		t.Fatal(count, e)
	}
	execSQL(t, s, "UPDATE "+s.temp+".compaction_handoffs SET expires_at=clock_timestamp()+interval '1 minute' WHERE id='new'")
	if _, e := s.GetCompactionHandoff(ctx, l, "new"); e != nil {
		t.Fatal(e)
	}
	var touched bool
	if e := s.tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT expires_at>clock_timestamp()+interval '23 hours' FROM "+s.temp+".compaction_handoffs WHERE id='new'").Scan(&touched)
	}); e != nil || !touched {
		t.Fatal(touched, e)
	}
	value.Data = []byte(strings.Repeat("x", 4097))
	if e := s.PutCompactionHandoff(ctx, l, "oversize", value); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestCompactionDispatchBindingsComposeWithNativePermits(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	sc := l.Principal.Scope
	if e := s.PutPrivate(ctx, sc, "route", "r", []byte("enabled")); e != nil {
		t.Fatal(e)
	}
	_, rv, e := s.GetPrivateVersion(ctx, sc, "route", "r")
	if e != nil {
		t.Fatal(e)
	}
	bindings := []NativeHealthBinding{{Kind: "route", ID: "r", Version: rv}}
	bound := WithDispatchBindings(ctx, bindings)
	// Mutating the caller's slice cannot replace the copied fence.
	bindings[0].Version = "changed-by-caller"
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "sonnet"}
	p, e := s.AcquireNativeHealthPermit(bound, l, k, false)
	if e != nil {
		t.Fatal(e)
	}
	value := CompactionHandoff{AccountID: "native", CredentialVersion: v, Data: []byte(`{}`)}
	if e = s.PutCompactionHandoff(bound, l, "lane", value); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetCompactionHandoff(ctx, l, "lane")
	if e != nil || len(got.Bindings) != 1 || got.Bindings[0].Version != rv {
		t.Fatal(got, e)
	}
	if e = s.WithNativeHealthDispatch(bound, l, []NativeHealthPermit{p}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, sc, "route", "r", []byte("disabled")); e != nil {
		t.Fatal(e)
	}
	// Completion uses persisted context bindings even without the original context.
	if e = s.FinishNativeHealthPermit(ctx, l, p, func(h NativeHealth) (NativeHealth, error) { t.Error("stale bound completion ran"); return h, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	for name, send := range map[string]func() error{
		"account": func() error {
			return s.WithAccountDispatch(bound, l, "native", v, func(context.Context) error { t.Error("stale account send"); return nil })
		},
		"generic": func() error {
			return s.WithDispatch(bound, l, func(context.Context) error { t.Error("stale generic send"); return nil })
		},
		"native": func() error { _, e := s.AcquireNativeHealthPermit(bound, l, k, false); return e },
	} {
		if e = send(); !errors.Is(e, ErrForbidden) {
			t.Fatal(name, e)
		}
	}
	if e = s.PutCompactionHandoff(bound, l, "late", value); !errors.Is(e, ErrForbidden) {
		t.Fatal("late stale config published", e)
	}
	if _, e = s.GetCompactionHandoff(ctx, l, "lane"); !errors.Is(e, ErrForbidden) {
		t.Fatal("stale config lookup", e)
	}
}

func TestDispatchBindingsNestedWrappersCannotWeakenOuterFence(t *testing.T) {
	s, l, v := nativeFixture(t)
	ctx := context.Background()
	sc := l.Principal.Scope
	if e := s.PutPrivate(ctx, sc, "route", "r", []byte("old")); e != nil {
		t.Fatal(e)
	}
	if e := s.PutPrivate(ctx, sc, "pool", "p", []byte("pool")); e != nil {
		t.Fatal(e)
	}
	_, routeVersion, e := s.GetPrivateVersion(ctx, sc, "route", "r")
	if e != nil {
		t.Fatal(e)
	}
	_, poolVersion, e := s.GetPrivateVersion(ctx, sc, "pool", "p")
	if e != nil {
		t.Fatal(e)
	}
	route := NativeHealthBinding{Kind: "route", ID: "r", Version: routeVersion}
	pool := NativeHealthBinding{Kind: "pool", ID: "p", Version: poolVersion}
	outer := WithDispatchBindings(ctx, []NativeHealthBinding{route})
	// Exact duplicate wrappers are harmless, and both current fences authorize one send.
	sends := 0
	if e = s.WithAccountDispatch(WithDispatchBindings(outer, []NativeHealthBinding{route, pool}), l, "native", v, func(context.Context) error { sends++; return nil }); e != nil || sends != 1 {
		t.Fatal(sends, e)
	}
	k := NativeHealthKey{AccountID: "native", CredentialVersion: v, Lane: NativeHealthFamily, Model: "sonnet"}
	permit, e := s.AcquireNativeHealthPermitBound(outer, l, k, false, []NativeHealthBinding{route, pool})
	if e != nil {
		t.Fatal("duplicate native binding", e)
	}
	if e = s.WithNativeHealthDispatch(outer, l, []NativeHealthPermit{permit}, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.PutCompactionHandoff(outer, l, "duplicate", CompactionHandoff{AccountID: "native", CredentialVersion: v, Data: []byte(`{}`), Bindings: []PrivateBinding{route, pool}}); e != nil {
		t.Fatal("duplicate publication binding", e)
	}
	if e = s.PutPrivate(ctx, sc, "route", "r", []byte("new")); e != nil {
		t.Fatal(e)
	}
	for name, inner := range map[string]context.Context{"nil": WithDispatchBindings(outer, nil), "other": WithDispatchBindings(outer, []NativeHealthBinding{pool})} {
		if e = s.WithAccountDispatch(inner, l, "native", v, func(context.Context) error { t.Error("nested wrapper removed outer fence", name); return nil }); !errors.Is(e, ErrForbidden) {
			t.Fatal(name, e)
		}
	}
	_, newVersion, e := s.GetPrivateVersion(ctx, sc, "route", "r")
	if e != nil {
		t.Fatal(e)
	}
	conflict := WithDispatchBindings(outer, []NativeHealthBinding{{Kind: "route", ID: "r", Version: newVersion}})
	if e = s.WithAccountDispatch(conflict, l, "native", v, func(context.Context) error { t.Error("conflicting version sent"); return nil }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
