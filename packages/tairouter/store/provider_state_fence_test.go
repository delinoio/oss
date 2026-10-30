// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProviderStateAccountReplacementDeletionGate(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		name := "replace"
		if deleting {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			_, sc := tenant(t, s)
			l := leaseFor(t, s, sc)
			if e := s.PutPrivate(ctx, sc, "provider", "account", []byte("old")); e != nil {
				t.Fatal(e)
			}
			_, version, e := s.GetPrivateVersion(ctx, sc, "provider", "account")
			if e != nil {
				t.Fatal(e)
			}
			p := l.Principal
			p.AccountID = "account"
			p.CredentialVersion = version
			policy := ProviderStatePolicy{TTL: time.Hour, Entries: 64, MaxEntryBytes: 4096, MaxTotalBytes: 262144}
			if e = s.PutProviderState(ctx, p, "replay", "id", ProviderState{Data: []byte("old-state")}, policy); e != nil {
				t.Fatal(e)
			}
			// Removing explicit ownership cannot read an account-bound row, even knowing its ID.
			if _, e = s.GetProviderState(ctx, l.Principal, "replay", "id"); !errors.Is(e, ErrForbidden) {
				t.Fatal("unbound read", e)
			}
			entered := make(chan struct{})
			resume := make(chan struct{})
			merged := make(chan error, 1)
			go func() {
				merged <- s.UpdateProviderState(ctx, p, "replay", "id", policy, func(old []byte) ([]byte, error) { close(entered); <-resume; return append(old, '!'), nil })
			}()
			<-entered
			changed := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				if deleting {
					changed <- s.DeletePrivate(ctx, sc, "provider", "account")
				} else {
					changed <- s.PutPrivate(ctx, sc, "provider", "account", []byte("new"))
				}
			}()
			<-started
			select {
			case e := <-changed:
				close(resume)
				t.Fatal("mutation bypassed active merge gate", e)
			case <-time.After(50 * time.Millisecond):
			}
			close(resume)
			if e = <-merged; e != nil {
				t.Fatal(e)
			}
			if e = <-changed; e != nil {
				t.Fatal(e)
			}
			if _, e = s.GetProviderState(ctx, p, "replay", "id"); !errors.Is(e, ErrForbidden) {
				t.Fatal("stale read", e)
			}
			if e = s.PutProviderState(ctx, p, "replay", "new", ProviderState{Data: []byte("bad")}, policy); !errors.Is(e, ErrForbidden) {
				t.Fatal("stale write", e)
			}
			if e = s.UpdateProviderState(ctx, p, "replay", "id", policy, func([]byte) ([]byte, error) { t.Error("stale merge callback ran"); return nil, nil }); !errors.Is(e, ErrForbidden) {
				t.Fatal("stale merge", e)
			}
			if !deleting {
				_, p.CredentialVersion, e = s.GetPrivateVersion(ctx, sc, "provider", "account")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.GetProviderState(ctx, p, "replay", "id"); !errors.Is(e, ErrForbidden) {
					t.Fatal("replacement inherited state", e)
				}
			}
		})
	}
}

func TestMarkPrivateReauthenticationVersionFence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(ctx, sc, "provider", "dsh", []byte("old")); e != nil {
		t.Fatal(e)
	}
	_, v, e := s.GetPrivateVersion(ctx, sc, "provider", "dsh")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.MarkPrivateReauthentication(ctx, l, "provider", "dsh", v); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.GetPrivateVersion(ctx, sc, "provider", "dsh"); !errors.Is(e, ErrReauthentication) {
		t.Fatal(e)
	}
	if e = s.WithAccountDispatch(ctx, l, "dsh", v, func(context.Context) error { t.Error("rejected credential sent"); return nil }); !errors.Is(e, ErrReauthentication) {
		t.Fatal(e)
	}
	if _, _, e = s.GetPrivateVersionForManagement(ctx, sc, "provider", "dsh"); e != nil {
		t.Fatal(e)
	}
	if e = s.PutPrivate(ctx, sc, "provider", "dsh", []byte("new")); e != nil {
		t.Fatal(e)
	}
	if e = s.MarkPrivateReauthentication(ctx, l, "provider", "dsh", v); !errors.Is(e, ErrConflict) {
		t.Fatal("late rejection affected replacement", e)
	}
	_, next, e := s.GetPrivateVersion(ctx, sc, "provider", "dsh")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Release(ctx, l); e != nil {
		t.Fatal(e)
	}
	if e = s.MarkPrivateReauthentication(ctx, l, "provider", "dsh", next); !errors.Is(e, ErrForbidden) {
		t.Fatal("released lease authorized", e)
	}
}
