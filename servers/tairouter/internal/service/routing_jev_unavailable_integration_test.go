// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func claimJEVFixtureAccount(t *testing.T, f *jevCooldownFixture, id string, uncertain bool) {
	t.Helper()
	scope := f.a.lease.Principal.Scope
	_, _, claim, err := f.a.s.ClaimScopePrivateRefresh(context.Background(), scope, string(providerKind), id)
	if err != nil {
		t.Fatal(err)
	}
	if uncertain {
		if err := f.a.s.MarkScopePrivateRefreshUncertain(context.Background(), scope, string(providerKind), id, claim); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJEVUnavailablePoolMemberAfterDecision(t *testing.T) {
	for _, modelBackend := range []bool{false, true} {
		for _, uncertain := range []bool{false, true} {
			for _, unavailable := range []string{"first", "sibling"} {
				name := fmt.Sprintf("model=%t/uncertain=%t/%s", modelBackend, uncertain, unavailable)
				t.Run(name, func(t *testing.T) {
					f := newJEVCooldownFixture(t, modelBackend, true, false, false)
					first, err := targetFor(f.first, "first")
					if err != nil {
						t.Fatal(err)
					}
					f.choice = selectionKey(first) + ":none"
					f.onDecision = func() { claimJEVFixtureAccount(t, f, unavailable, uncertain) }
					w := f.request("")
					wantSibling := int32(0)
					if unavailable == "first" {
						wantSibling = 1
					}
					if w.Code != http.StatusOK || f.decisionCalls.Load() != 1 || f.firstSends.Load() != 1 || f.siblingSends.Load() != wantSibling || f.secondSends.Load() != 0 {
						t.Fatalf("HTTP %d body=%s decision=%d sends=%d sibling=%d second=%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.siblingSends.Load(), f.secondSends.Load())
					}
				})
			}
		}
	}
}

func TestJEVUnavailableTargetAfterDecisionFallsBack(t *testing.T) {
	for _, modelBackend := range []bool{false, true} {
		for _, uncertain := range []bool{false, true} {
			t.Run(fmt.Sprintf("model=%t/uncertain=%t", modelBackend, uncertain), func(t *testing.T) {
				f := newJEVCooldownFixture(t, modelBackend, false, false, false)
				first, err := targetFor(f.first, "first")
				if err != nil {
					t.Fatal(err)
				}
				f.choice = selectionKey(first) + ":none"
				f.onDecision = func() { claimJEVFixtureAccount(t, f, "first", uncertain) }
				w := f.request("")
				if w.Code != http.StatusOK || f.decisionCalls.Load() != 1 || f.firstSends.Load() != 0 || f.secondSends.Load() != 1 {
					t.Fatalf("HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
				}
			})
		}
	}
}

func TestJEVUnavailableAllTargetsBeforeDecision(t *testing.T) {
	f := newJEVCooldownFixture(t, false, false, false, false)
	claimJEVFixtureAccount(t, f, "first", false)
	claimJEVFixtureAccount(t, f, "second", true)
	w := f.request("")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no_eligible_credential") || f.firstSends.Load() != 0 || f.secondSends.Load() != 0 {
		t.Fatalf("HTTP %d body=%s sends=%d/%d", w.Code, w.Body.String(), f.firstSends.Load(), f.secondSends.Load())
	}
}

func TestJEVUnavailableMemberDoesNotMaskMalformedProvider(t *testing.T) {
	for _, malformed := range []string{"first", "sibling"} {
		t.Run(malformed, func(t *testing.T) {
			f := newJEVCooldownFixture(t, false, true, false, false)
			first, err := targetFor(f.first, "first")
			if err != nil {
				t.Fatal(err)
			}
			f.choice = selectionKey(first) + ":none"
			f.onDecision = func() {
				if err := f.a.s.PutPrivate(context.Background(), f.a.lease.Principal.Scope, string(providerKind), malformed, []byte("{invalid")); err != nil {
					t.Fatal(err)
				}
			}
			w := f.request("")
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "authorization_unavailable") || f.firstSends.Load() != 0 || f.secondSends.Load() != 0 {
				t.Fatalf("HTTP %d body=%s sends=%d/%d", w.Code, w.Body.String(), f.firstSends.Load(), f.secondSends.Load())
			}
		})
	}
}

func TestJEVUnavailableMalformedDecisionFailOpen(t *testing.T) {
	f := newJEVCooldownFixture(t, false, false, false, false)
	f.choice = "invalid-choice"
	f.onDecision = func() { claimJEVFixtureAccount(t, f, "first", false) }
	w := f.request("")
	if w.Code != http.StatusOK || f.decisionCalls.Load() != 1 || f.firstSends.Load() != 0 || f.secondSends.Load() != 1 {
		t.Fatalf("HTTP %d body=%s decision=%d sends=%d/%d", w.Code, w.Body.String(), f.decisionCalls.Load(), f.firstSends.Load(), f.secondSends.Load())
	}
}
