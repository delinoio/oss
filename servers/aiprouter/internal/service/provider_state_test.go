// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestProviderStateAtomicMergeAndRevocation(t *testing.T) {
	s := serverStore(t)
	ctx := context.Background()
	u, _ := testIdentity(t, s, "state-owner")
	org, e := s.CreateOrganization(ctx, u.ID, "state fixture")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(ctx, org.ID, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	key, _, e := s.IssueKey(ctx, sc, "state fixture", nil)
	if e != nil {
		t.Fatal(e)
	}
	p := inference.Principal{OrganizationID: org.ID, UserID: u.ID, KeyID: key.ID, MembershipGeneration: sc.Generation}
	if e = s.PutPrivate(ctx, sc, "provider", "selected-account", []byte("fixture-credential")); e != nil {
		t.Fatal(e)
	}
	_, version, e := s.GetPrivateVersion(ctx, sc, "provider", "selected-account")
	if e != nil {
		t.Fatal(e)
	}
	p.AccountID, p.CredentialVersion = "selected-account", version
	a := &inferenceAdapter{s: s}
	if _, found, e := a.LookupProviderState(ctx, p, "devin", "missing"); e != nil || found {
		t.Fatalf("absent: %v %v", found, e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- a.UpdateProviderState(ctx, p, "devin", "merge", time.Now().Add(time.Hour), func(current []byte) ([]byte, error) {
				n := 0
				if len(current) > 0 {
					var e error
					n, e = strconv.Atoi(string(current))
					if e != nil {
						return nil, e
					}
				}
				return []byte(strconv.Itoa(n + 1)), nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	value, found, e := a.LookupProviderState(ctx, p, "devin", "merge")
	if e != nil || !found || string(value.Data) != "12" {
		t.Fatalf("lost update: %q %v %v", value.Data, found, e)
	}
	conflict := errors.New("claim occupied")
	if e = a.UpdateProviderState(ctx, p, "devin", "merge", time.Now().Add(time.Hour), func([]byte) ([]byte, error) { return nil, conflict }); !errors.Is(e, conflict) {
		t.Fatalf("callback identity lost: %v", e)
	}
	if e = a.SaveProviderState(ctx, p, "devin", "oversize", inference.ProviderState{Data: make([]byte, 4097), ExpiresAt: time.Now().Add(time.Hour)}); e == nil {
		t.Fatal("oversized state accepted")
	}
	for _, ns := range []string{"openai-chat", "openai-responses", "anthropic", "mimo-free", "tool-reasoning-replay", "google-vertex", "compaction-reasoning"} {
		if e = a.SaveProviderState(ctx, p, ns, "receipt", inference.ProviderState{Data: []byte("bound-digest"), ExpiresAt: time.Now().Add(30 * time.Minute)}); e != nil {
			t.Fatalf("namespace %s save: %v", ns, e)
		}
		value, found, e := a.LookupProviderState(ctx, p, ns, "receipt")
		if e != nil || !found || string(value.Data) != "bound-digest" {
			t.Fatalf("namespace %s roundtrip: %v %v", ns, found, e)
		}
	}
	if e = s.RevokeKey(ctx, sc, key.ID); e != nil {
		t.Fatal(e)
	}
	if _, found, e = a.LookupProviderState(ctx, p, "devin", "merge"); e == nil || found {
		t.Fatalf("revocation treated as absence: %v %v", found, e)
	}
	for _, ns := range []string{"openai-chat", "openai-responses", "anthropic", "mimo-free", "tool-reasoning-replay", "google-vertex", "compaction-reasoning"} {
		if _, found, e := a.LookupProviderState(ctx, p, ns, "receipt"); e == nil || found {
			t.Fatalf("namespace %s revoked read: %v %v", ns, found, e)
		}
	}
	called := false
	if e = a.UpdateProviderState(ctx, p, "devin", "merge", time.Now().Add(time.Hour), func([]byte) ([]byte, error) { called = true; return []byte("resurrected"), nil }); e == nil || called {
		t.Fatalf("revoked merge callback: %v %v", called, e)
	}
}

func TestProviderStateAccountReplacementAndDeletion(t *testing.T) {
	s := serverStore(t)
	ctx := context.Background()
	u, _ := testIdentity(t, s, "state-account-fence")
	org, err := s.CreateOrganization(ctx, u.ID, "account fence")
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err := s.Membership(ctx, org.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.IssueKey(ctx, sc, "state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutPrivate(ctx, sc, "provider", "account", []byte("original")); err != nil {
		t.Fatal(err)
	}
	_, version, err := s.GetPrivateVersion(ctx, sc, "provider", "account")
	if err != nil {
		t.Fatal(err)
	}
	p := inference.Principal{OrganizationID: org.ID, UserID: u.ID, KeyID: key.ID, MembershipGeneration: sc.Generation, AccountID: "account", CredentialVersion: version}
	a := &inferenceAdapter{s: s}
	const ns = "tool-reasoning-replay"
	save := func(p inference.Principal) error {
		return a.SaveProviderState(ctx, p, ns, "response", inference.ProviderState{Data: []byte("opaque-reasoning"), ExpiresAt: time.Now().Add(30 * time.Minute)})
	}
	if err = save(p); err != nil {
		t.Fatal(err)
	}
	if err = s.PutPrivate(ctx, sc, "provider", "account", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	assertDenied := func(p inference.Principal) {
		t.Helper()
		if _, found, err := a.LookupProviderState(ctx, p, ns, "response"); err == nil || found {
			t.Fatalf("invalid account read: found=%v err=%v", found, err)
		}
		called := false
		err := a.UpdateProviderState(ctx, p, ns, "response", time.Now().Add(30*time.Minute), func([]byte) ([]byte, error) { called = true; return []byte("resurrected"), nil })
		if err == nil || called {
			t.Fatalf("invalid account update: called=%v err=%v", called, err)
		}
		if err := save(p); err == nil {
			t.Fatal("stale capture accepted")
		}
	}
	assertDenied(p)
	_, nextVersion, err := s.GetPrivateVersion(ctx, sc, "provider", "account")
	if err != nil {
		t.Fatal(err)
	}
	p.CredentialVersion = nextVersion
	if _, found, err := a.LookupProviderState(ctx, p, ns, "response"); err == nil || found {
		t.Fatalf("replacement read prior binding: found=%v err=%v", found, err)
	}
	if err = s.DeletePrivate(ctx, sc, "provider", "account"); err != nil {
		t.Fatal(err)
	}
	assertDenied(p)
}
