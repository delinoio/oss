// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
)

func TestContextRegistrationRequiresUnambiguousObservedAccount(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	_, raw, e := s.IssueKey(ctx, sc, "context", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ConfigureClientContext(ctx, raw, true); e != nil {
		t.Fatal(e)
	}
	l, e := s.Admit(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Release(ctx, l)
	for _, account := range []string{"a", "b"} {
		if e = s.PutPrivate(ctx, sc, "provider", account, []byte("fixture")); e != nil {
			t.Fatal(e)
		}
		_, version, e := s.GetPrivateVersion(ctx, sc, "provider", account)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CaptureClientContext(ctx, l, "ambiguous", account, version, []byte("fixture")); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.BindClientContext(ctx, raw, "ambiguous"); e == nil {
		t.Fatal("ambiguous session was bound")
	}
	if _, e = s.BindClientContext(ctx, raw, "never-seen"); e == nil {
		t.Fatal("unobserved session was bound")
	}
}
