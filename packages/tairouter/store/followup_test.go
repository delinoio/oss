// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/tairouter/artifacts"
	"github.com/delinoio/oss/packages/tairouter/core"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func leaseFor(t *testing.T, s *Store, sc core.Scope) Lease {
	t.Helper()
	_, raw, e := s.IssueKey(context.Background(), sc, "work", nil)
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Admit(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func TestProviderFlowClaimTargetFenceAndReceipt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	if e := s.PutPrivate(ctx, sc, "provider", "p", []byte("old")); e != nil {
		t.Fatal(e)
	}
	_, version, e := s.GetPrivateVersion(ctx, sc, "provider", "p")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveProviderFlowBound(ctx, sc, "f", "provider", "p", version, []byte("oauth secret"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	var winners atomic.Int32
	var claim string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, c, e := s.ClaimProviderFlow(ctx, sc, "f")
			if e == nil {
				winners.Add(1)
				mu.Lock()
				claim = c
				mu.Unlock()
			} else if !errors.Is(e, ErrBusy) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	if e = s.PutPrivate(ctx, sc, "provider", "p", []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	if e = s.WithProviderFlowDispatch(ctx, sc, "f", claim, func(context.Context) error { t.Fatal("stale reconnect dispatched"); return nil }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.CompleteProviderFlow(ctx, sc, "f", claim, "p", []byte("stale")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.CancelProviderFlow(ctx, sc, "f"); e != nil {
		t.Fatal(e)
	}
	r, e := s.ProviderFlowReceipt(ctx, sc, "f")
	if e != nil || r.Status != "canceled" {
		t.Fatal(r, e)
	}
	if _, e = s.GetProviderFlow(ctx, sc, "f"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.SaveProviderFlow(ctx, sc, "new", []byte("new-secret"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	_, claim, e = s.ClaimProviderFlow(ctx, sc, "new")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteProviderFlow(ctx, sc, "new", claim, "new-provider", []byte("credential")); e != nil {
		t.Fatal(e)
	}
	r, e = s.ProviderFlowReceipt(ctx, sc, "new")
	if e != nil || r.Status != "completed" {
		t.Fatal(r, e)
	}
	if _, _, e = s.ClaimProviderFlow(ctx, sc, "new"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestRefreshExclusiveUncertainAndBrowserRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(ctx, sc, "provider", "p", []byte("rotate")); e != nil {
		t.Fatal(e)
	}
	var winners atomic.Int32
	var claim, version string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, v, c, e := s.ClaimPrivateRefresh(ctx, l, "provider", "p")
			if e == nil {
				winners.Add(1)
				mu.Lock()
				claim = c
				version = v
				mu.Unlock()
			} else if !errors.Is(e, ErrBusy) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	if e := s.WithAccountDispatch(ctx, l, "p", version, func(context.Context) error { t.Fatal("old credential dispatched during refresh"); return nil }); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if e := s.MarkPrivateRefreshUncertain(ctx, l, "provider", "p", claim); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := s.ClaimScopePrivateRefresh(ctx, sc, "provider", "p"); !errors.Is(e, ErrReauthentication) {
		t.Fatal(e)
	}
	if _, e := s.GetPrivate(ctx, sc, "provider", "p"); !errors.Is(e, ErrReauthentication) {
		t.Fatal(e)
	}
	if _, _, e := s.GetPrivateVersionForManagement(ctx, sc, "provider", "p"); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveProviderFlowBound(ctx, sc, "reconnect", "provider", "p", version, []byte("flow"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	_, c, e := s.ClaimProviderFlow(ctx, sc, "reconnect")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteProviderFlow(ctx, sc, "reconnect", c, "p", []byte("fresh")); e != nil {
		t.Fatal(e)
	}
	value, v, c, e := s.ClaimScopePrivateRefresh(ctx, sc, "provider", "p")
	if e != nil || string(value) != "fresh" {
		t.Fatal(string(value), e)
	}
	if e = s.CompleteScopePrivateRefresh(ctx, sc, "provider", "p", v, c, []byte("renewed")); e != nil {
		t.Fatal(e)
	}
	if e = s.WithAccountDispatch(ctx, l, "p", version, func(context.Context) error { t.Fatal("stale version send"); return nil }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestSettingsCASAndAtomicPrices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	i := core.Identity{Issuer: "issuer", Subject: "operator"}
	v, e := s.Settings(ctx)
	if e != nil || v.Revision < 1 {
		t.Fatal(v, e)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, e := s.PutPriceOverride(ctx, i, v.Revision, fmt.Sprint(n), json.RawMessage(`{"input":"1"}`))
			if e == nil {
				winners.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}(n)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	next, e := s.Settings(ctx)
	if e != nil || next.Revision != v.Revision+1 {
		t.Fatal(next, e)
	}
	if _, e = s.UpdateSettingsCAS(ctx, i, v.Revision, v); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	rows, _, e := s.QueryInstanceAudits(ctx, i, AuditFilter{Action: "price.override_set", Outcome: AuditSucceeded})
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}
func TestDurableSelectionAffinityAndVersionCooldown(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	var candidates []SelectionCandidate
	for n := range 2 {
		id := fmt.Sprint(n)
		if e := s.PutPrivate(ctx, sc, "provider", id, []byte("credential")); e != nil {
			t.Fatal(e)
		}
		_, v, e := s.GetPrivateVersion(ctx, sc, "provider", id)
		if e != nil {
			t.Fatal(e)
		}
		candidates = append(candidates, SelectionCandidate{ID: id, Version: v, Weight: n + 1})
	}
	r := SelectionRequest{GroupID: "pool", Version: "v1", Model: "m", Strategy: SelectionWeighted, Candidates: candidates}
	counts := map[string]int{}
	for range 30 {
		receipt, e := s.SelectAccount(ctx, l, r)
		if e != nil {
			t.Fatal(e)
		}
		counts[receipt.CandidateID]++
	}
	if counts["0"] != 10 || counts["1"] != 20 {
		t.Fatal(counts)
	}
	r.Strategy = SelectionRoundRobin
	r.AffinityKey = "conversation"
	first, e := s.SelectAccount(ctx, l, r)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SelectAccount(ctx, l, r)
	if e != nil || again.CandidateID != first.CandidateID {
		t.Fatal(again, e)
	}
	idx := 0
	if first.CandidateID == "1" {
		idx = 1
	}
	if e = s.SetAccountCooldownVersion(ctx, l, first.CandidateID, candidates[idx].Version, "m", time.Now().Add(31*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	again, e = s.SelectAccount(ctx, l, r)
	if e != nil || again.CandidateID == first.CandidateID {
		t.Fatal("affinity ignored cooldown", again, e)
	}
	if e = s.PutPrivate(ctx, sc, "provider", first.CandidateID, []byte("new")); e != nil {
		t.Fatal(e)
	}
	if e = s.SetAccountCooldownVersion(ctx, l, first.CandidateID, candidates[idx].Version, "m", time.Now().Add(time.Hour)); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	r.Candidates = []SelectionCandidate{candidates[idx]}
	if _, e = s.SelectAccount(ctx, l, r); !errors.Is(e, ErrNotFound) {
		t.Fatal("stale candidate selected", e)
	}
}
func TestUsageExactFiltersDailyPagesAndAuditIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	zero := int64(0)
	cost := "0"
	start := time.Now().Add(-time.Minute)
	for n := range 3 {
		l := leaseFor(t, s, sc)
		outcome := OutcomeSucceeded
		if n == 2 {
			outcome = OutcomeUpstreamError
		}
		if e := s.RecordUsage(ctx, l, Usage{Model: fmt.Sprint(n), Provider: "provider", Outcome: outcome, InputTokens: &zero, PriceKnown: true, Cost: &cost}); e != nil {
			t.Fatal(e)
		}
	}
	end := time.Now().Add(time.Minute)
	a, e := s.QueryAggregate(ctx, sc, UsageFilter{UserID: sc.UserID, Start: &start, End: &end, Provider: "provider"})
	if e != nil || a.Requests != 3 || a.FailedRequests != 1 || a.KnownInput != 3 || a.KnownOutput != 0 || a.MissingPrice != 0 {
		t.Fatal(a, e)
	}
	rows, next, e := s.QueryUsage(ctx, sc, UsageFilter{UserID: sc.UserID, Provider: "provider", Limit: 1})
	if e != nil || len(rows) != 1 || next == "" {
		t.Fatal(rows, next, e)
	}
	if _, _, e = s.QueryUsage(ctx, sc, UsageFilter{UserID: sc.UserID, Provider: "wrong", Limit: 1, After: next}); !errors.Is(e, ErrConflict) {
		t.Fatal("cursor filter rebinding", e)
	}
	days, next, e := s.QueryDailyUsagePage(ctx, sc, UsageFilter{UserID: sc.UserID, Limit: 1})
	if e != nil || len(days) != 1 || next == "" {
		t.Fatal(days, next, e)
	}
	days, _, e = s.QueryDailyUsagePage(ctx, sc, UsageFilter{UserID: sc.UserID, Limit: 1, After: next})
	if e != nil || len(days) != 1 {
		t.Fatal(days, e)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	if _, e = s.QueryAggregate(ctx, sc, UsageFilter{UserID: sc.UserID, Start: &old}); !errors.Is(e, ErrRangeUnavailable) {
		t.Fatal(e)
	}
	i := core.Identity{Issuer: "issuer", Subject: "operator"}
	if _, e = s.QueryInstanceAggregate(ctx, i, UsageFilter{UserID: sc.UserID}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	audits, _, e := s.QueryInstanceAudits(ctx, i, AuditFilter{})
	if e != nil || len(audits) != 0 {
		t.Fatal("organization audit leaked", audits, e)
	}
	h, e := s.OperationalHealth(ctx, i)
	if e != nil || h.UsageRecording.LastSuccess == nil {
		t.Fatal(h, e)
	}
}
func TestProviderStateAtomicMergeKeyFenceAndExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	policy := ProviderStatePolicy{time.Hour, 256, 2 << 20, 64 << 20}
	var wg sync.WaitGroup
	for n := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e := s.UpdateProviderState(ctx, l.Principal, "google", "session", policy, func(old []byte) ([]byte, error) {
				v := map[string]bool{}
				if old != nil {
					if e := json.Unmarshal(old, &v); e != nil {
						return nil, e
					}
				}
				v[fmt.Sprint(n)] = true
				return json.Marshal(v)
			})
			if e != nil {
				t.Error(e)
			}
		}(n)
	}
	wg.Wait()
	got, e := s.GetProviderState(ctx, l.Principal, "google", "session")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]bool
	if e = json.Unmarshal(got.Data, &v); e != nil || len(v) != 20 {
		t.Fatal(v, e)
	}
	other := leaseFor(t, s, sc)
	if _, e = s.GetProviderState(ctx, other.Principal, "google", "session"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.RevokeKey(ctx, sc, l.Principal.KeyID); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateProviderState(ctx, l.Principal, "google", "session", policy, func([]byte) ([]byte, error) { t.Fatal("merge ran after revocation"); return nil, nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.GetProviderState(ctx, l.Principal, "google", "session"); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}

func TestSelfRevocationIdempotenceDeliveryAndNewStateRotation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	k, raw, e := s.IssueKey(ctx, sc, "self", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeOwnKey(ctx, raw, k.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeOwnKey(ctx, raw, k.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeOwnKey(ctx, raw, "other"); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	i, e := s.Invite(ctx, sc, "delivery@example.com")
	if e != nil || i.OrganizationName == "" || i.DeliveryState != "queued" {
		t.Fatal(i, e)
	}
	m, e := s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WithMailAttempt(ctx, m, func(context.Context, Mail) MailOutcome { return MailSuccess }); e != nil {
		t.Fatal(e)
	}
	list, e := s.ListInvitations(ctx, sc)
	if e != nil || len(list) != 1 || list[0].DeliveryState != "sent" {
		t.Fatal(list, e)
	}
	execSQL(t, s, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 minutes'")
	if _, e = s.ResendInvitation(ctx, sc, i.ID); e != nil {
		t.Fatal(e)
	}
	audits, _, e := s.QueryAudits(ctx, sc, AuditFilter{Action: "invitation.resent", Outcome: AuditSucceeded})
	if e != nil || len(audits) != 1 {
		t.Fatal(audits, e)
	}
	l := leaseFor(t, s, sc)
	policy := ProviderStatePolicy{time.Hour, 4, 4096, 8192}
	if e = s.PutProviderState(ctx, l.Principal, "wire", "state", ProviderState{Data: []byte("secret")}, policy); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveProviderFlow(ctx, sc, "rotate", []byte("flow-secret"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	key[0] = 8
	s.keys, _ = core.NewEnvelope("new", map[string][]byte{"v1": make([]byte, 32), "new": key})
	if e = s.RotateEncryption(ctx); e != nil {
		t.Fatal(e)
	}
	s.keys, _ = core.NewEnvelope("new", map[string][]byte{"new": key})
	if p, e := s.GetProviderState(ctx, l.Principal, "wire", "state"); e != nil || string(p.Data) != "secret" {
		t.Fatal(p, e)
	}
	if p, e := s.GetProviderFlow(ctx, sc, "rotate"); e != nil || string(p) != "flow-secret" {
		t.Fatal(string(p), e)
	}
}
func TestProviderFlowRemovalAndScopeDispatchFence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, owner := tenant(t, s)
	u := user(t, s, "oauth-member")
	sc := addMember(t, s, owner.OrganizationID, u)
	if e := s.SaveProviderFlow(ctx, sc, "f", []byte("pending"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	_, claim, e := s.ClaimProviderFlow(ctx, sc, "f")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveMember(ctx, owner, u.ID); e != nil {
		t.Fatal(e)
	}
	addMember(t, s, owner.OrganizationID, u)
	if e = s.WithScopeDispatch(ctx, sc, func(context.Context) error { t.Fatal("removed scope dispatch"); return nil }); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e = s.CompleteProviderFlow(ctx, sc, "f", claim, "new", []byte("late")); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}

func TestOperationUsageIndependentIdempotentAndDeleted(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	u := Usage{Model: "helper", Provider: "helper-account", Outcome: OutcomeUnknown}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.RecordOperationUsage(ctx, l, "send1", u); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if e := s.RecordOperationUsage(ctx, l, "send2", u); e != nil {
		t.Fatal(e)
	}
	if e := s.RecordUsage(ctx, l, Usage{Model: "parent", Outcome: OutcomeCanceled}); e != nil {
		t.Fatal(e)
	}
	a, e := s.QueryAggregate(ctx, sc, UsageFilter{UserID: sc.UserID})
	if e != nil || a.Requests != 3 || a.MissingUsage != 3 {
		t.Fatal(a, e)
	}
	if e = s.DeleteOrganization(ctx, sc); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordOperationUsage(ctx, l, "late", u); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}

func TestOperationUsageReceiptRetriesAfterTransactionRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+s.schema+".usage ADD CONSTRAINT reject_operation_fixture CHECK (model <> 'reject-operation')"); err != nil {
		t.Fatal(err)
	}
	u := Usage{Model: "reject-operation", Provider: "custom", Outcome: OutcomeUnknown}
	if err := s.RecordOperationUsage(ctx, l, "decision-retry", u); err == nil {
		t.Fatal("expected transaction failure")
	}
	u.Model = "decision-model"
	for range 2 {
		if err := s.RecordOperationUsage(ctx, l, "decision-retry", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordUsage(ctx, l, Usage{Model: "target-model", Provider: "custom", Outcome: OutcomeSucceeded}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.QueryUsage(ctx, sc, UsageFilter{UserID: sc.UserID})
	if err != nil || len(rows) != 2 {
		t.Fatalf("usage rows: %v, %v", rows, err)
	}
	aggregate, err := s.QueryAggregate(ctx, sc, UsageFilter{UserID: sc.UserID})
	if err != nil || aggregate.Requests != 2 || aggregate.MissingUsage != 2 {
		t.Fatalf("aggregate: %+v, %v", aggregate, err)
	}
}
func TestSoftDenialsFallbackAndPinned(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	r := SelectionRequest{GroupID: "g", Version: "v", Model: "m", Strategy: SelectionPriority}
	for _, id := range []string{"a", "b"} {
		if e := s.PutPrivate(ctx, sc, "provider", id, []byte("key")); e != nil {
			t.Fatal(e)
		}
		_, v, e := s.GetPrivateVersion(ctx, sc, "provider", id)
		if e != nil {
			t.Fatal(e)
		}
		r.Candidates = append(r.Candidates, SelectionCandidate{ID: id, Version: v})
	}
	if e := s.SetAccountSoftDenial(ctx, l, "a", r.Candidates[0].Version, "m"); e != nil {
		t.Fatal(e)
	}
	got, e := s.SelectAccount(ctx, l, r)
	if e != nil || got.CandidateID != "b" {
		t.Fatal(got, e)
	}
	r.PinnedID = "a"
	got, e = s.SelectAccount(ctx, l, r)
	if e != nil || got.CandidateID != "a" {
		t.Fatal(got, e)
	}
	r.PinnedID = ""
	if e = s.SetAccountSoftDenial(ctx, l, "b", r.Candidates[1].Version, "m"); e != nil {
		t.Fatal(e)
	}
	got, e = s.SelectAccount(ctx, l, r)
	if e != nil || got.CandidateID != "a" {
		t.Fatal("all-denied fallback", got, e)
	}
}
func TestMistralProbeSingleWinnerNoTimedReclaim(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(ctx, sc, "provider", "m", []byte("credential")); e != nil {
		t.Fatal(e)
	}
	_, v, e := s.GetPrivateVersion(ctx, sc, "provider", "m")
	if e != nil {
		t.Fatal(e)
	}
	initial, e := s.JoinMistralHold(ctx, l, "m", v, "digest", "model")
	if e != nil {
		t.Fatal(e)
	}
	open, e := s.TryMistralProbe(ctx, l, initial)
	if e != nil || !open.Ready || open.Claim != "" {
		t.Fatal("ordinary traffic was gated", open, e)
	}
	if e = s.SetMistralHold(ctx, l, "m", v, "digest", "model", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	ticket, e := s.JoinMistralHold(ctx, l, "m", v, "digest", "model")
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.TryMistralProbe(ctx, l, ticket)
	if e != nil || p.Ready {
		t.Fatal(p, e)
	}
	execSQL(t, s, "UPDATE mistral_holds SET hold_until=clock_timestamp()-interval '1 second'")
	var wins atomic.Int32
	var winner MistralProbe
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := s.TryMistralProbe(ctx, l, ticket)
			if e != nil {
				t.Error(e)
			}
			if p.Ready {
				wins.Add(1)
				mu.Lock()
				winner = p
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal(wins.Load())
	}
	execSQL(t, s, "UPDATE mistral_holds SET hold_until=clock_timestamp()-interval '30 days'")
	if p, e = s.TryMistralProbe(ctx, l, ticket); e != nil || p.Ready {
		t.Fatal("probe reclaimed", p, e)
	}
	if e = s.SetMistralHold(ctx, l, "m", v, "digest", "model", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishMistralProbe(ctx, l, ticket, winner.Claim, nil); e != nil {
		t.Fatal(e)
	}
	if p, e = s.TryMistralProbe(ctx, l, ticket); e != nil || p.Ready {
		t.Fatal("late success cleared newer RetryAfter", p, e)
	}
	execSQL(t, s, "UPDATE mistral_holds SET hold_until=clock_timestamp()-interval '1 second'")
	p, e = s.TryMistralProbe(ctx, l, ticket)
	if e != nil || !p.Ready {
		t.Fatal(p, e)
	}
	if e = s.FinishMistralProbe(ctx, l, ticket, p.Claim, nil); e != nil {
		t.Fatal(e)
	}
	if p, e = s.TryMistralProbe(ctx, l, ticket); e != nil || !p.Ready || p.Claim != "" {
		t.Fatal(p, e)
	}
	if e = s.LeaveMistralHold(ctx, l, ticket); e != nil {
		t.Fatal(e)
	}
}

func TestMistralIdentityQuotaAndTenantIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	l := leaseFor(t, s, sc)
	if e := s.PutPrivate(ctx, sc, "provider", "m", []byte("key")); e != nil {
		t.Fatal(e)
	}
	_, v, e := s.GetPrivateVersion(ctx, sc, "provider", "m")
	if e != nil {
		t.Fatal(e)
	}
	for n := range 128 {
		if e = s.SetMistralHold(ctx, l, "m", v, "digest", fmt.Sprint(n), time.Now().Add(time.Hour)); e != nil {
			t.Fatal(n, e)
		}
	}
	var limited *LimitError
	if e = s.SetMistralHold(ctx, l, "m", v, "digest", "overflow", time.Now().Add(time.Hour)); !errors.As(e, &limited) {
		t.Fatal(e)
	}
	_, other := tenant(t, s)
	ol := leaseFor(t, s, other)
	if e = s.PutPrivate(ctx, other, "provider", "m", []byte("key")); e != nil {
		t.Fatal(e)
	}
	_, ov, e := s.GetPrivateVersion(ctx, other, "provider", "m")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetMistralHold(ctx, ol, "m", ov, "digest", "overflow", time.Now().Add(time.Hour)); e != nil {
		t.Fatal("cross-tenant identity quota", e)
	}
	ticket, e := s.JoinMistralHold(ctx, l, "m", v, "digest", "0")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.TryMistralProbe(ctx, ol, ticket); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-tenant ticket", e)
	}
}
func TestVideoMetadataSeparateLimits(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	o := artifacts.Owner{OrganizationID: sc.OrganizationID, UserID: sc.UserID, Generation: sc.Generation}
	now := time.Now()
	makeArtifact := func(mime string, size int64) artifacts.Artifact {
		id := core.ID()
		return artifacts.Artifact{ID: id, ObjectKey: "artifacts/" + id, Owner: o, ContentType: mime, Size: size, CreatedAt: now, ExpiresAt: now.Add(time.Hour), UploadDeadline: now.Add(time.Minute)}
	}
	video := makeArtifact("video/mp4", 200<<20)
	if e := s.Reserve(ctx, o, []artifacts.Artifact{video}, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Reserve(ctx, o, []artifacts.Artifact{makeArtifact("image/png", 51<<20)}, now); e == nil {
		t.Fatal("image limit widened")
	}
	if e := s.Reserve(ctx, o, []artifacts.Artifact{makeArtifact("video/mp4", 201<<20)}, now); e == nil {
		t.Fatal("video file limit")
	}
	if e := s.Reserve(ctx, o, []artifacts.Artifact{makeArtifact("video/mp4", 1), makeArtifact("image/png", 1)}, now); e == nil {
		t.Fatal("mixed response accepted")
	}
	batch := []artifacts.Artifact{makeArtifact("video/mp4", 200<<20), makeArtifact("video/mp4", 200<<20), makeArtifact("video/mp4", 200<<20)}
	if e := s.Reserve(ctx, o, batch, now); e != nil {
		t.Fatal(e)
	}
	batch = append(batch, makeArtifact("video/mp4", 1))
	if e := s.Reserve(ctx, o, batch, now); e == nil {
		t.Fatal("video batch limit")
	}
}

func TestKeyMetadataSafePrefixAndAdmissionLastUse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, sc := tenant(t, s)
	k, raw, e := s.IssueKey(ctx, sc, "personal", nil)
	if e != nil {
		t.Fatal(e)
	}
	if k.CreatedAt.IsZero() || k.Prefix != raw[:12] || k.Device || k.LastUsedAt != nil {
		t.Fatal("issuance metadata", k)
	}
	rows, e := s.ListKeys(ctx, sc)
	if e != nil || len(rows) != 1 || rows[0].CreatedAt.IsZero() || rows[0].Prefix != k.Prefix || rows[0].LastUsedAt != nil {
		t.Fatal(rows, e)
	}
	if _, e = s.AuthenticateKey(ctx, raw); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.ListKeys(ctx, sc)
	if rows[0].LastUsedAt != nil {
		t.Fatal("mere lookup counted as usage")
	}
	if _, e = s.Admit(ctx, raw); e != nil {
		t.Fatal(e)
	}
	rows, e = s.ListKeys(ctx, sc)
	if e != nil || rows[0].LastUsedAt == nil {
		t.Fatal(rows, e)
	}
	d, e := s.StartDevice(ctx, "cli", "https://example.com")
	if e != nil {
		t.Fatal(e)
	}
	cli, e := s.ApproveDevice(ctx, sc, d.Code, nil)
	if e != nil || !cli.Device || cli.CreatedAt.IsZero() || len(cli.Prefix) != 12 {
		t.Fatal(cli, e)
	}
	rows, e = s.ListKeys(ctx, sc)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		if row.ID == cli.ID && !row.Device {
			t.Fatal("CLI mapped as personal")
		}
	}
	execSQL(t, s, "UPDATE api_keys SET prefix='' WHERE id=$1", k.ID)
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	rows, e = s.ListKeys(ctx, sc)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		if row.ID == k.ID && row.Prefix != "" {
			t.Fatal("fabricated legacy prefix")
		}
	}
}
