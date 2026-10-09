// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func TestPaidCreditSparseRetainsExactDecimalAndObservation(t *testing.T) {
	now := time.Now().UTC()
	yes, no := true, false
	balance := "1250.5000000000000000000001"
	s := &SubscriptionState{}
	b := SubscriptionPaidCreditBucket{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &balance, ObservedAt: now}
	if mergePaidCredits(s, []SubscriptionPaidCreditBucket{b}) != nil {
		t.Fatal("valid projection refused")
	}
	if mergePaidCredits(s, nil) != nil || len(s.PaidCredits) != 1 || *s.PaidCredits[0].Balance != balance || !s.PaidCredits[0].ObservedAt.Equal(now) {
		t.Fatal("sparse field changed retained evidence")
	}
	b.Balance = nil
	b.ObservedAt = now.Add(time.Second)
	if mergePaidCredits(s, []SubscriptionPaidCreditBucket{b}) != nil || s.PaidCredits[0].Balance != nil {
		t.Fatal("observed null became zero or retained balance")
	}
	b.ID = "another"
	b.Balance = &balance
	if mergePaidCredits(s, []SubscriptionPaidCreditBucket{b}) != nil || len(s.PaidCredits) != 2 {
		t.Fatal("distinct buckets combined")
	}
}
func TestPaidCreditRejectsInvalidDecimalWithoutChangingLastGood(t *testing.T) {
	now := time.Now().UTC()
	yes, no := true, false
	good := "0"
	s := &SubscriptionState{PaidCredits: []SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &good, ObservedAt: now}}}
	for _, bad := range []string{"-1", "1e3", ".5", "1.", "private balance", "", "00000000000000000000000000000000000000000000000000000000000000000"} {
		b := s.PaidCredits[0]
		b.Balance = &bad
		b.ObservedAt = now.Add(time.Second)
		if mergePaidCredits(s, []SubscriptionPaidCreditBucket{b}) == nil || *s.PaidCredits[0].Balance != "0" || !s.PaidCredits[0].ObservedAt.Equal(now) {
			t.Fatalf("invalid balance changed original evidence: %q", bad)
		}
	}
	for _, v := range []string{"0", "0.00", "0000.01", "1234567890123456789012345678901234567890.1234567890"} {
		if !ValidPaidCreditBalance(v) {
			t.Fatal("exact decimal refused")
		}
	}
}

func TestPaidCreditNewerBucketSurvivesOlderAggregateQuotaSnapshot(t *testing.T) {
	now := time.Now().UTC()
	latestQuota := now.Add(-10 * time.Second)
	retainedBucket := now.Add(-50 * time.Second)
	incomingBucket := now.Add(-30 * time.Second)
	yes, no := true, false
	zero, positive := 0.0, 0.8
	oldBalance, newBalance := "1.000", "2.000000000000000000001"
	a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, ConfirmedExhausted: true, RecoveryNotifications: true,
		Subscription: &SubscriptionState{QuotaObservedAt: &latestQuota, QuotaState: Observed, SpendControlReached: &yes, SpendControlObservedAt: &latestQuota,
			PaidCredits: []SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &oldBalance, ObservedAt: retainedBucket}}},
		Quota: []QuotaWindow{{ID: "codex:primary", Blocking: true, Remaining: &zero, ObservedAt: latestQuota, State: Observed}}}
	observation := SubscriptionQuotaObservation{ObservedAt: incomingBucket, SpendControlReached: &no,
		Windows:     []SubscriptionQuotaWindow{{ID: "codex:primary", Remaining: &positive}},
		PaidCredits: []SubscriptionPaidCreditBucket{{ID: "codex", HasCredits: &yes, Unlimited: &no, Balance: &newBalance, ObservedAt: incomingBucket}}}
	assertQuotaUnchanged := func() {
		t.Helper()
		if !a.Subscription.QuotaObservedAt.Equal(latestQuota) || a.Subscription.QuotaState != Observed || !*a.Subscription.SpendControlReached || !a.Subscription.SpendControlObservedAt.Equal(latestQuota) || !a.ConfirmedExhausted || *a.Quota[0].Remaining != zero || !a.Quota[0].ObservedAt.Equal(latestQuota) {
			t.Fatal("older aggregate quota altered exhaustion or newer quota evidence")
		}
	}
	if recovered, err := ApplySubscriptionQuota(&a, observation, now); err != nil || recovered {
		t.Fatal("independent paid merge failed or granted quota recovery", err)
	}
	if *a.Subscription.PaidCredits[0].Balance != newBalance || !a.Subscription.PaidCredits[0].ObservedAt.Equal(incomingBucket) {
		t.Fatal("aggregate ordering dropped newer exact paid-credit evidence")
	}
	assertQuotaUnchanged()
	observation.ObservedAt = now.Add(-40 * time.Second)
	observation.PaidCredits[0].ObservedAt = observation.ObservedAt
	observation.PaidCredits[0].Balance = &oldBalance
	if recovered, err := ApplySubscriptionQuota(&a, observation, now); err != nil || recovered || *a.Subscription.PaidCredits[0].Balance != newBalance || !a.Subscription.PaidCredits[0].ObservedAt.Equal(incomingBucket) {
		t.Fatal("older bucket rolled back retained successful evidence", err)
	}
	assertQuotaUnchanged()
	observation.ObservedAt = now.Add(-20 * time.Second)
	observation.PaidCredits[0].ObservedAt = observation.ObservedAt
	badBalance := "1e3"
	observation.PaidCredits[0].Balance = &badBalance
	if _, err := ApplySubscriptionQuota(&a, observation, now); err == nil || *a.Subscription.PaidCredits[0].Balance != newBalance || !a.Subscription.PaidCredits[0].ObservedAt.Equal(incomingBucket) {
		t.Fatal("invalid older aggregate changed retained bucket")
	}
	assertQuotaUnchanged()
}
