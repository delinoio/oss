// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestAutomaticCreditSelectionRetainsDisplayOrder(t *testing.T) {
	now := time.Now().UTC()
	early, late := now.Add(time.Hour), now.Add(2*time.Hour)
	entries := []SubscriptionResetCreditDetail{{ID: "late", ResetType: CodexRateLimitsReset, Status: SubscriptionCreditAvailable, GrantedAt: now, ExpiresAt: &late}, {ID: "noexpiry", ResetType: CodexRateLimitsReset, Status: SubscriptionCreditAvailable, GrantedAt: now}, {ID: "early-z", ResetType: CodexRateLimitsReset, Status: SubscriptionCreditAvailable, GrantedAt: now, ExpiresAt: &early}, {ID: "early-a", ResetType: CodexRateLimitsReset, Status: SubscriptionCreditAvailable, GrantedAt: now, ExpiresAt: &early}}
	before := append([]SubscriptionResetCreditDetail(nil), entries...)
	v := SubscriptionResetCredits{ObservationID: NewID(), ObservedAt: now, AvailableCount: 4, Credits: &entries}
	id, ok := SelectAutomaticResetCredit(&v, now)
	if !ok || id != "early-a" {
		t.Fatalf("wrong selection %q %v", id, ok)
	}
	if !reflect.DeepEqual(before, entries) {
		t.Fatal("changed displayed inventory")
	}
}
func TestAutomaticCreditSelectionRejectsUnavailableAuthority(t *testing.T) {
	now := time.Now().UTC()
	empty := []SubscriptionResetCreditDetail{}
	expired := now.Add(-time.Second)
	entries := []SubscriptionResetCreditDetail{{ID: "expired", ResetType: CodexRateLimitsReset, Status: SubscriptionCreditAvailable, GrantedAt: now.Add(-time.Hour), ExpiresAt: &expired}}
	for name, v := range map[string]*SubscriptionResetCredits{"nil": nil, "zero": {ObservationID: NewID(), ObservedAt: now}, "stale": {ObservationID: NewID(), ObservedAt: now.Add(-6 * time.Minute), AvailableCount: 1}, "future": {ObservationID: NewID(), ObservedAt: now.Add(time.Second), AvailableCount: 1}, "empty": {ObservationID: NewID(), ObservedAt: now, AvailableCount: 1, Credits: &empty}, "expired": {ObservationID: NewID(), ObservedAt: now, AvailableCount: 1, Credits: &entries}, "malformed": {ObservedAt: now, AvailableCount: 1}} {
		t.Run(name, func(t *testing.T) {
			if _, ok := SelectAutomaticResetCredit(v, now); ok {
				t.Fatal("unavailable inventory authorized spending")
			}
		})
	}
	v := SubscriptionResetCredits{ObservationID: NewID(), ObservedAt: now, AvailableCount: 1}
	if id, ok := SelectAutomaticResetCredit(&v, now); !ok || id != "" {
		t.Fatal("count-only inventory did not select native next")
	}
}
func TestAutomaticCreditEpisodeRearmsWithoutNotifications(t *testing.T) {
	now := time.Now().UTC()
	zero, high := 0.0, 0.8
	a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Subscription: &SubscriptionState{AutomaticCreditEpisode: &AutomaticResetCreditEpisode{ID: NewID()}}, ConfirmedExhausted: true, RecoveryNotifications: false, Quota: []QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &zero, ObservedAt: now, State: Observed}}}
	notified, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &high}}}, now)
	if err != nil || notified || a.ConfirmedExhausted || a.Subscription.AutomaticCreditEpisode != nil {
		t.Fatal("episode rearm depended on notification preference", err)
	}
}

func TestAutomaticCreditFreshRecoveryClearsGenerationStaleEpisode(t *testing.T) {
	for _, scenario := range []string{"new-generation-positive", "new-generation-sparse", "same-generation-positive"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			positive := 0.8
			generation := NewID()
			episodeGeneration := NewID()
			if scenario == "same-generation-positive" {
				episodeGeneration = generation
			}
			block := SubscriptionQuotaBlock{SessionID: NewID(), ExecutionID: NewID(), NativeThreadID: NativeIdentity(NewID()), NativeTurnID: NativeIdentity(NewID()), Reason: CodexUsageLimitExceeded}
			a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, ConfirmedExhausted: false, Subscription: &SubscriptionState{Generation: generation, AutomaticCreditEpisode: &AutomaticResetCreditEpisode{ID: NewID(), Generation: episodeGeneration}, AutomaticCreditBlocks: []SubscriptionQuotaBlock{block}}, Quota: []QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &positive, State: Observed, ObservedAt: now.Add(-time.Second)}}}
			observed := SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &positive}}}
			if scenario == "new-generation-sparse" {
				observed.Windows = nil
			}
			notified, err := ApplySubscriptionQuota(&a, observed, now)
			if err != nil || notified {
				t.Fatal("unexpected recovery notification", err)
			}
			wantCleared := scenario == "new-generation-positive"
			if (a.Subscription.AutomaticCreditEpisode == nil) != wantCleared {
				t.Fatal("stale generation episode recovery boundary changed", scenario)
			}
			if len(a.Subscription.AutomaticCreditBlocks) != 1 || a.Subscription.AutomaticCreditBlocks[0] != block {
				t.Fatal("recovery discarded original turn spending fence")
			}
		})
	}
}
