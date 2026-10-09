// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"regexp"
	"slices"
	"time"
)

// Paid balances are native bucket observations, never currency or spend authority.
type SubscriptionPaidCreditBucket struct {
	ID         string    `json:"id"`
	HasCredits *bool     `json:"has_credits"`
	Unlimited  *bool     `json:"unlimited"`
	Balance    *string   `json:"balance"`
	ObservedAt time.Time `json:"observed_at"`
}

var paidCreditDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func ValidPaidCreditBalance(v string) bool { return len(v) <= 64 && paidCreditDecimal.MatchString(v) }
func ValidatePaidCreditBuckets(v []SubscriptionPaidCreditBucket) error {
	if len(v) > 32 {
		return InvalidSubscriptionObservation()
	}
	seen := map[string]bool{}
	for _, b := range v {
		if !ValidSubscriptionOpaqueID(b.ID) || len(b.ID) > 110 || seen[b.ID] || b.HasCredits == nil || b.Unlimited == nil || b.ObservedAt.Unix() <= 0 || b.ObservedAt.Year() > 9999 || b.Balance != nil && !ValidPaidCreditBalance(*b.Balance) {
			return InvalidSubscriptionObservation()
		}
		seen[b.ID] = true
	}
	return nil
}
func mergePaidCredits(state *SubscriptionState, incoming []SubscriptionPaidCreditBucket) error {
	merged := append([]SubscriptionPaidCreditBucket(nil), state.PaidCredits...)
	for _, b := range incoming {
		i := slices.IndexFunc(merged, func(old SubscriptionPaidCreditBucket) bool { return old.ID == b.ID })
		if i < 0 {
			merged = append(merged, b)
		} else if !b.ObservedAt.Before(merged[i].ObservedAt) {
			merged[i] = b
		}
	}
	if err := ValidatePaidCreditBuckets(merged); err != nil {
		return err
	}
	state.PaidCredits = merged
	return nil
}
