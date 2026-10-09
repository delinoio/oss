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
