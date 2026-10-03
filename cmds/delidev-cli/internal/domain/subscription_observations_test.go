// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func TestSubscriptionQuotaSparseFailureAndObservedRecovery(t *testing.T) {
	now := time.Now().UTC()
	zero, low, high := 0.0, 0.2, 0.8
	later := now.Add(time.Hour)
	a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Subscription: &SubscriptionState{}, RecoveryNotifications: true, ConfirmedExhausted: true, Quota: []QuotaWindow{{ID: "codex:primary", ComparisonGroup: "chatgpt", Blocking: true, Remaining: &zero, ObservedAt: now, ResetAt: &later, State: Observed}}}
	recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary"}}}, now)
	if err != nil || recovered || !a.ConfirmedExhausted || a.Quota[0].Remaining == nil || *a.Quota[0].Remaining != 0 {
		t.Fatal("sparse absence erased exhaustion", err)
	}
	recovered, err = ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:secondary", Remaining: &high, ResetAt: &later}}}, now)
	if err != nil || recovered || !a.ConfirmedExhausted {
		t.Fatal("one healthy window erased another blocked window")
	}
	recovered, err = ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &low, ResetAt: &later}}}, now)
	if err != nil || !recovered || a.ConfirmedExhausted {
		t.Fatal("fresh positive native evidence did not recover")
	}
	state, score, _, _ := quotaEvidence(a.Quota, now)
	if state != Observed || score == nil || *score != 0.2 {
		t.Fatal("routing did not use minimum remaining fraction")
	}
	if recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &low}}}, now); err != nil || recovered {
		t.Fatal("duplicate recovery produced another notification")
	}
	a.ConfirmedExhausted = true
	if recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: nil}, now); err != nil || recovered || !a.ConfirmedExhausted {
		t.Fatal("empty successful observation reused old positive evidence")
	}
	spendBlocked := true
	if recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, Windows: []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &high}}, SpendControlReached: &spendBlocked}, now); err != nil || recovered || !a.ConfirmedExhausted {
		t.Fatal("positive quota ignored native spend-control block")
	}
	spendBlocked = false
	a.RecoveryNotifications = false
	if recovered, err := ApplySubscriptionQuota(&a, SubscriptionQuotaObservation{ObservedAt: now, SpendControlReached: &spendBlocked}, now); err != nil || recovered || a.ConfirmedExhausted {
		t.Fatal("default-off recovery preference or observed spend clearance lost")
	}
	a.ConfirmedExhausted = true
	a.Subscription.QuotaState = ObservationFailed
	original := *a.Subscription.QuotaObservedAt
	if !a.Subscription.QuotaObservedAt.Equal(original) {
		t.Fatal("failure erased last success")
	}
	// Passing a reset without another native measurement is never recovery.
	_, _, _, _ = quotaEvidence(a.Quota, later.Add(time.Second))
	if !a.ConfirmedExhausted {
		t.Fatal("wall clock granted recovery")
	}
}
func TestResetCreditInventoryPreservesCountAndUnavailableDetails(t *testing.T) {
	now := time.Now().UTC()
	items := []SubscriptionResetCreditDetail{{ID: "credit_1", ResetType: "codexRateLimits", Status: "available", GrantedAt: now}}
	for _, detail := range []*[]SubscriptionResetCreditDetail{nil, &items} {
		v := SubscriptionResetCredits{ObservationID: NewID(), ObservedAt: now, AvailableCount: 2, Credits: detail}
		if err := v.Validate(); err != nil {
			t.Fatal(err)
		}
		if v.AvailableCount != 2 || detail != nil && len(*v.Credits) != 1 {
			t.Fatal("inventory derived count from details")
		}
	}
	op := SubscriptionObservationOperation{ID: NewID(), Action: SubscriptionResetCredit, MachineID: NewID(), Actor: Principal{Type: OwnerDevice}, ConnectionID: NewID(), Generation: NewID(), Phase: SubscriptionObservationSending, RequestedAt: now, NextCredit: true, CreditsObservationID: NewID()}
	if err := op.Validate(); err != nil {
		t.Fatal(err)
	}
	key := op.ID
	op.Phase = SubscriptionObservationUncertain
	op.Phase = SubscriptionObservationQueued
	if op.ID != key || op.Validate() != nil {
		t.Fatal("reconciliation replaced the official key")
	}
	op.ErrorCode = Code("secret_sentinel")
	if op.Validate() == nil {
		t.Fatal("unknown error entered public metadata")
	}
}
