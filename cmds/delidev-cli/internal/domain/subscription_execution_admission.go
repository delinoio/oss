// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// ExecutionQuotaAdmission classifies only quota. Callers retain every other
// account, model, project, Worker and native ownership restriction.
type ExecutionQuotaAdmission string

const (
	IncludedQuotaAdmission ExecutionQuotaAdmission = "included-quota"
	PaidCreditAdmission    ExecutionQuotaAdmission = "paid-credit"
	QuotaAdmissionBlocked  ExecutionQuotaAdmission = "exhausted"
)

// ClassifyExecutionQuotaAdmission does not clear included exhaustion or rearm
// reset-credit episodes. Native Codex still decides whether to accept input.
func ClassifyExecutionQuotaAdmission(a Account, harness Harness, now time.Time) ExecutionQuotaAdmission {
	if !a.ConfirmedExhausted {
		return IncludedQuotaAdmission
	}
	if a.Type != SubscriptionAccount || a.SubscriptionService != SubscriptionChatGPT || a.ProviderID != "" || harness != Codex || a.Subscription == nil {
		return QuotaAdmissionBlocked
	}
	state := a.Subscription
	if state.SpendControlReached == nil || *state.SpendControlReached || state.SpendControlObservedAt == nil || !freshExecutionQuotaEvidence(*state.SpendControlObservedAt, now) {
		return QuotaAdmissionBlocked
	}
	for _, bucket := range state.PaidCredits {
		// Exact balance is display data and cannot supply availability.
		if bucket.ID == "codex" && bucket.HasCredits != nil && bucket.Unlimited != nil && (*bucket.HasCredits || *bucket.Unlimited) && freshExecutionQuotaEvidence(bucket.ObservedAt, now) {
			return PaidCreditAdmission
		}
	}
	return QuotaAdmissionBlocked
}

func freshExecutionQuotaEvidence(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= 5*time.Minute
}
