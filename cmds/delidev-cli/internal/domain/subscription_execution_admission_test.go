// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
	"time"
)

func paidAdmissionAccount(now time.Time) Account {
	yes, no, zero, balance := true, false, 0.0, "1250.50"
	return Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Enabled: true, Health: AccountReady, ConfirmedExhausted: true, RecoveryNotifications: true,
		Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: now.Add(-time.Hour)},
		Subscription: &SubscriptionState{SpendControlReached: &no, SpendControlObservedAt: &now,
			PaidCredits: []SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &balance, ObservedAt: now}}},
		Quota: []QuotaWindow{{ID: "codex:primary", Blocking: true, Remaining: &zero, ObservedAt: now, State: Observed, ComparisonGroup: "chatgpt"}}}
}

func TestExecutionQuotaAdmissionRequiresIndependentFreshNativeEvidence(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		change func(*Account, *Harness)
		want   ExecutionQuotaAdmission
	}{
		{"has credits", nil, PaidCreditAdmission},
		{"unlimited null balance", func(a *Account, _ *Harness) {
			yes, no := true, false
			a.Subscription.PaidCredits[0].HasCredits = &no
			a.Subscription.PaidCredits[0].Unlimited = &yes
			a.Subscription.PaidCredits[0].Balance = nil
		}, PaidCreditAdmission},
		{"five minute boundary", func(a *Account, _ *Harness) {
			at := now.Add(-5 * time.Minute)
			a.Subscription.PaidCredits[0].ObservedAt = at
			a.Subscription.SpendControlObservedAt = &at
		}, PaidCreditAdmission},
		{"null balance", func(a *Account, _ *Harness) { a.Subscription.PaidCredits[0].Balance = nil }, PaidCreditAdmission},
		{"zero balance", func(a *Account, _ *Harness) { balance := "0"; a.Subscription.PaidCredits[0].Balance = &balance }, PaidCreditAdmission},
		{"positive balance without availability", func(a *Account, _ *Harness) { no := false; a.Subscription.PaidCredits[0].HasCredits = &no }, QuotaAdmissionBlocked},
		{"missing flags", func(a *Account, _ *Harness) { a.Subscription.PaidCredits[0].Unlimited = nil }, QuotaAdmissionBlocked},
		{"missing bucket", func(a *Account, _ *Harness) { a.Subscription.PaidCredits = nil }, QuotaAdmissionBlocked},
		{"other bucket", func(a *Account, _ *Harness) { a.Subscription.PaidCredits[0].ID = "codex:primary" }, QuotaAdmissionBlocked},
		{"future bucket", func(a *Account, _ *Harness) { a.Subscription.PaidCredits[0].ObservedAt = now.Add(time.Nanosecond) }, QuotaAdmissionBlocked},
		{"old bucket", func(a *Account, _ *Harness) {
			a.Subscription.PaidCredits[0].ObservedAt = now.Add(-5*time.Minute - time.Nanosecond)
		}, QuotaAdmissionBlocked},
		{"zero bucket time", func(a *Account, _ *Harness) { a.Subscription.PaidCredits[0].ObservedAt = time.Time{} }, QuotaAdmissionBlocked},
		{"spend reached", func(a *Account, _ *Harness) { yes := true; a.Subscription.SpendControlReached = &yes }, QuotaAdmissionBlocked},
		{"spend missing", func(a *Account, _ *Harness) { a.Subscription.SpendControlReached = nil }, QuotaAdmissionBlocked},
		{"spend time missing", func(a *Account, _ *Harness) { a.Subscription.SpendControlObservedAt = nil }, QuotaAdmissionBlocked},
		{"spend stale", func(a *Account, _ *Harness) {
			at := now.Add(-5*time.Minute - time.Nanosecond)
			a.Subscription.SpendControlObservedAt = &at
		}, QuotaAdmissionBlocked},
		{"spend future", func(a *Account, _ *Harness) {
			at := now.Add(time.Nanosecond)
			a.Subscription.SpendControlObservedAt = &at
		}, QuotaAdmissionBlocked},
		{"API account", func(a *Account, _ *Harness) { a.Type = APIAccount }, QuotaAdmissionBlocked},
		{"provider account", func(a *Account, _ *Harness) { a.ProviderID = NewID() }, QuotaAdmissionBlocked},
		{"Claude account", func(a *Account, h *Harness) { a.SubscriptionService = SubscriptionClaude; *h = ClaudeCode }, QuotaAdmissionBlocked},
		{"OpenCode harness", func(_ *Account, h *Harness) { *h = OpenCode }, QuotaAdmissionBlocked},
		{"no subscription", func(a *Account, _ *Harness) { a.Subscription = nil }, QuotaAdmissionBlocked},
		{"included available", func(a *Account, _ *Harness) { a.ConfirmedExhausted = false; a.Subscription = nil }, IncludedQuotaAdmission},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, harness := paidAdmissionAccount(now), Codex
			if test.change != nil {
				test.change(&a, &harness)
			}
			if got := ClassifyExecutionQuotaAdmission(a, harness, now); got != test.want {
				t.Fatalf("admission=%s want=%s", got, test.want)
			}
			if test.want == PaidCreditAdmission && !a.ConfirmedExhausted {
				t.Fatal("admission cleared included exhaustion")
			}
		})
	}
}

func TestPaidExecutionAdmissionPreservesSourcePriorityAndRestrictions(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name        string
		change      func(ID, *Agent, []SourceRouteInput, *Project)
		eligibility Eligibility
	}{
		{"eligible", nil, Eligible},
		{"disabled", func(_ ID, a *Agent, sources []SourceRouteInput, _ *Project) {
			id := a.Routes[0].Accounts[0].ID
			v := sources[0].Accounts[id]
			v.Enabled = false
			sources[0].Accounts[id] = v
		}, DisabledAccount},
		{"unauthenticated", func(_ ID, a *Agent, sources []SourceRouteInput, _ *Project) {
			id := a.Routes[0].Accounts[0].ID
			v := sources[0].Accounts[id]
			v.Connection = nil
			sources[0].Accounts[id] = v
		}, UnauthenticatedAccount},
		{"removed", func(_ ID, a *Agent, sources []SourceRouteInput, _ *Project) {
			id := a.Routes[0].Accounts[0].ID
			v := sources[0].Accounts[id]
			v.Removal = &AccountRemoval{RequestID: NewID(), ExpectedRevision: 1}
			sources[0].Accounts[id] = v
		}, UnauthenticatedAccount},
		{"incompatible", func(_ ID, _ *Agent, sources []SourceRouteInput, _ *Project) {
			sources[0].Model.SubscriptionService = SubscriptionClaude
		}, IncompatibleAccount},
		{"project restricted", func(_ ID, a *Agent, _ []SourceRouteInput, p *Project) {
			p.Accounts = Restriction{Configured: true, IDs: a.Routes[1].accountIDs()}
		}, ProjectRestricted},
		{"agent restricted", func(_ ID, _ *Agent, _ []SourceRouteInput, p *Project) { p.Agents = Restriction{Configured: true} }, AgentRestricted},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, agent, sources := sourceFixture()
			for key := range sources[0].Accounts {
				sources[0].Accounts[key] = paidAdmissionAccount(now)
			}
			project := Project{}
			if test.change != nil {
				test.change(id, &agent, sources, &project)
			}
			route, _, err := RouteSources(id, agent, &project, sources, Priority, RoutingState{}, now)
			if test.eligibility == Eligible {
				if err != nil || route.SourceIndex == nil || *route.SourceIndex != 0 {
					t.Fatalf("paid source lost priority: %+v %v", route, err)
				}
				if route.Candidates[0].Score == nil || *route.Candidates[0].Score != 0 {
					t.Fatal("paid admission changed included quota score")
				}
			} else if len(route.Sources) == 0 || route.Sources[0].Route.Candidates[0].Eligibility != test.eligibility {
				t.Fatalf("restriction bypassed: %+v %v", route, err)
			}
		})
	}
	id, agent, sources := sourceFixture()
	for key := range sources[0].Accounts {
		sources[0].Accounts[key] = paidAdmissionAccount(now)
	}
	route, _, err := RouteSources(id, agent, nil, sources, Priority, RoutingState{}, now.Add(5*time.Minute+time.Nanosecond))
	if err != nil || route.SourceIndex == nil || *route.SourceIndex != 1 {
		t.Fatalf("genuine exhaustion did not advance source: %+v %v", route, err)
	}
}

func TestPaidCreditMergeDoesNotRecoverQuotaOrRearmEpisode(t *testing.T) {
	now := time.Now().UTC()
	a := paidAdmissionAccount(now.Add(-time.Minute))
	episode := &AutomaticResetCreditEpisode{ID: NewID(), Generation: NewID()}
	a.Subscription.AutomaticCreditEpisode = episode
	quotaBefore := append([]QuotaWindow(nil), a.Quota...)
	incoming := paidAdmissionAccount(now).Subscription
	recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, SpendControlReached: incoming.SpendControlReached, PaidCredits: incoming.PaidCredits}, now)
	if err != nil || recovered || !a.ConfirmedExhausted || a.Subscription.AutomaticCreditEpisode != episode || !reflect.DeepEqual(a.Quota, quotaBefore) {
		t.Fatal("paid observations granted quota recovery, changed score or rearmed automatic credit", err)
	}
	if ConfirmedSubscriptionQuotaUsable(a, now) || ClassifyExecutionQuotaAdmission(a, Codex, now) != PaidCreditAdmission {
		t.Fatal("included recovery and paid admission were conflated")
	}
}
