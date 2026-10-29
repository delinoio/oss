// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/proto"
)

func quotaOrderEvidence(t1 time.Time, weeklyPercent float64) (nativeQuotaSnapshot, nativeQuotaSnapshot) {
	reset := t1.Add(24 * time.Hour)
	weekly := nativeQuotaSnapshot{Plan: "plus", Windows: []nativeQuotaWindow{{Window: "weekly", Percent: weeklyPercent, ObservedAt: t1, ResetAt: &reset}}}
	t2 := t1.Add(time.Second)
	monthly := nativeQuotaSnapshot{Plan: "plus", MonthlyPrimaryObservedAt: t2, Windows: []nativeQuotaWindow{{Window: "monthly", Percent: 10, ObservedAt: t2, ResetAt: &reset}}}
	return weekly, monthly
}

func assertMonthlyOnlyQuota(t *testing.T, q nativeQuotaSnapshot, t2, selectionTime time.Time) {
	t.Helper()
	if !q.MonthlyPrimaryObservedAt.Equal(t2) || len(q.Windows) != 1 || q.Windows[0].Window != "monthly" || q.Windows[0].Percent != 10 || !q.Windows[0].ObservedAt.Equal(t2) {
		t.Fatalf("superseded weekly evidence returned: %+v", q)
	}
	if score, known := nativeUsageScore("openai", "gpt-6-astra", q, selectionTime); !known || score != 10 {
		t.Fatalf("monthly quota score = %v, known = %t", score, known)
	}
}

func TestNativeQuotaMonthlyPrimaryRejectsOlderWeekly(t *testing.T) {
	t1 := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	t2, t3 := t1.Add(time.Second), t1.Add(2*time.Second)
	weekly, monthly := quotaOrderEvidence(t1, 100)
	for _, tc := range []struct {
		name    string
		updates []nativeQuotaSnapshot
	}{
		{"weekly-then-monthly-then-delayed-weekly", []nativeQuotaSnapshot{weekly, monthly, weekly}},
		{"empty-then-monthly-then-delayed-weekly", []nativeQuotaSnapshot{monthly, weekly}},
		{"weekly-then-monthly", []nativeQuotaSnapshot{weekly, monthly}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q nativeQuotaSnapshot
			for _, update := range tc.updates {
				q = mergeNativeQuota(q, update)
			}
			assertMonthlyOnlyQuota(t, q, t2, t3)
		})
	}
	olderMonthly := weekly
	olderMonthly.MonthlyPrimaryObservedAt = t1
	olderMonthly.Windows = append(olderMonthly.Windows, nativeQuotaWindow{Window: "monthly", Percent: 90, ObservedAt: t1})
	assertMonthlyOnlyQuota(t, mergeNativeQuota(monthly, olderMonthly), t2, t3)
}

func TestNativeQuotaMonthlySecondaryAndNewerWeekly(t *testing.T) {
	t2 := time.Date(2026, 10, 28, 0, 0, 1, 0, time.UTC)
	reset := t2.Add(24 * time.Hour)
	for _, secondary := range []float64{40, 100} {
		headers := http.Header{}
		headers.Set("x-codex-primary-window-minutes", "43200")
		headers.Set("x-codex-primary-used-percent", "10")
		headers.Set("x-codex-primary-reset-at", "1793232000")
		headers.Set("x-codex-secondary-used-percent", nativePercentText(secondary))
		headers.Set("x-codex-secondary-reset-at", "1793232000")
		q := mergeNativeQuota(nativeQuotaSnapshot{}, parseNativeQuota("openai", "gpt-6-astra", 200, headers, t2))
		if len(q.Windows) != 2 || q.Windows[0].Window != "monthly" || q.Windows[1].Window != "weekly" || !q.Windows[1].ObservedAt.Equal(t2) {
			t.Fatalf("same-response secondary lost: %+v", q)
		}
		if score, known := nativeUsageScore("openai", "gpt-6-astra", q, t2.Add(time.Second)); !known || score != secondary {
			t.Fatalf("secondary score = %v, known = %t", score, known)
		}
		if secondary == 100 && (!nativeWindowCurrent(q.Windows[1], t2.Add(time.Second)) || q.Windows[1].ResetAt == nil || !q.Windows[1].ResetAt.After(t2.Add(time.Second))) {
			t.Fatal("valid full weekly window lost its exclusion evidence")
		}
		q = mergeNativeQuota(q, nativeQuotaSnapshot{Windows: []nativeQuotaWindow{{Window: "weekly", Percent: 60, ObservedAt: t2.Add(time.Second), ResetAt: &reset}}})
		if q.Windows[1].Percent != 60 {
			t.Fatalf("newer weekly observation ignored: %+v", q)
		}
	}
}

func nativePercentText(percent float64) string {
	return strconv.FormatFloat(percent, 'f', -1, 64)
}

func TestNativeQuotaSupersededWeeklyPersistedSelection(t *testing.T) {
	for _, stalePercent := range []float64{100, 70} {
		t.Run(nativePercentText(stalePercent), func(t *testing.T) {
			a, sc, key := poolRoutingFixture(t, v1.PoolStrategy_POOL_STRATEGY_PRIORITY)
			p := admitRouting(t, a, key)
			ctx := context.Background()
			target := inference.Target{Provider: "logical", Model: "gpt-6-astra", Auth: inference.OAuth}
			ids := []string{"a"}
			if stalePercent == 70 {
				ids = append(ids, "b")
			}
			req := store.SelectionRequest{GroupID: "pool", Version: "config", Model: target.Model, Strategy: store.SelectionPriority}
			t1 := time.Now().UTC().Add(-time.Minute)
			weekly, monthly := quotaOrderEvidence(t1, stalePercent)
			for _, id := range ids {
				d := providerData{Metadata: &v1.ProviderConnection{Id: id, OrganizationId: sc.OrganizationID, ProviderId: "openai", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: "synthetic-" + id}
				putRoutingProvider(t, a, sc, d)
				_, version, err := a.s.GetPrivateVersion(ctx, sc, "provider", id)
				if err != nil {
					t.Fatal(err)
				}
				req.Candidates = append(req.Candidates, store.SelectionCandidate{ID: id, Version: version})
				if id == "a" {
					for _, update := range []nativeQuotaSnapshot{weekly, monthly, weekly} {
						if err := a.writeNativeQuota(ctx, p, id, version, update); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					if err := a.writeNativeQuota(ctx, p, id, version, nativeTestQuota(t1.Add(time.Second), "weekly", 20)); err != nil {
						t.Fatal(err)
					}
				}
			}
			a.selections = map[string]routeSelection{selectionKey(target): {pool: &v1.Pool{NativeOptions: &v1.NativePoolOptions{Enabled: proto.Bool(true)}}}}
			got, err := a.preselectNative(ctx, p, target, "openai", req)
			if err != nil || len(got.Candidates) == 0 || got.Candidates[0].ID != "a" {
				t.Fatalf("eligible monthly account not selected: %+v, %v", got.Candidates, err)
			}
			q, found, err := a.readNativeQuota(ctx, p, "a", req.Candidates[0].Version)
			if err != nil || !found {
				t.Fatalf("persisted quota missing: %v", err)
			}
			assertMonthlyOnlyQuota(t, q, t1.Add(time.Second), time.Now())
			if stalePercent == 100 {
				reset := time.Now().Add(time.Hour)
				full := nativeQuotaSnapshot{MonthlyPrimaryObservedAt: t1.Add(2 * time.Second), Windows: []nativeQuotaWindow{
					{Window: "monthly", Percent: 10, ObservedAt: t1.Add(2 * time.Second), ResetAt: &reset},
					{Window: "weekly", Percent: 100, ObservedAt: t1.Add(2 * time.Second), ResetAt: &reset},
				}}
				if err := a.writeNativeQuota(ctx, p, "a", req.Candidates[0].Version, full); err != nil {
					t.Fatal(err)
				}
				if _, err := a.preselectNative(ctx, p, target, "openai", req); !errors.Is(err, inference.ErrNoCredential) {
					t.Fatalf("valid full weekly secondary did not exclude account: %v", err)
				}
			}
		})
	}
}
