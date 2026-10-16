// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// ClaudeCredentialMode is the closed ownership mode of a Claude subscription
// account. An omitted value is the historical device-owned native profile. The
// server-token mode owns one sealed setup-token generation in the server vault.
type ClaudeCredentialMode string

const (
	// ClaudeCredentialServerToken marks a new account whose setup token is
	// created on the server and delivered only through protected execution leases.
	ClaudeCredentialServerToken ClaudeCredentialMode = "server-token"
)

// ManagedClaude reports whether this state belongs to a server-token account.
func (s SubscriptionState) ManagedClaude() bool {
	return s.ClaudeCredential == ClaudeCredentialServerToken
}

// ManagedClaudeAccount reports whether the account is a server-token Claude
// subscription. It never inspects credential material.
func (a Account) ManagedClaudeAccount() bool {
	return a.Type == SubscriptionAccount && a.SubscriptionService == SubscriptionClaude && a.Subscription != nil && a.Subscription.ManagedClaude()
}

func (s SubscriptionState) validateManagedClaude(account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid server-owned Claude subscription ownership.", "Preserve the original server operation, generation and execution lease.")
	}
	if s.ClaudeCredential != ClaudeCredentialServerToken || s.NativeProfileID != "" || s.OwnerMachineID != "" || s.NativeOperation != nil {
		return invalid()
	}
	if s.Observation != nil || s.ResetCredits != nil || s.QuotaState != "" || s.QuotaObservedAt != nil || s.SpendControlReached != nil || s.SpendControlObservedAt != nil || s.ServerQuota != nil || s.ServerCredit != nil {
		return invalid()
	}
	if s.Generation != "" && !NativeIdentityCommitmentValid(s.IdentityCommitment) {
		return invalid()
	}
	if s.Generation == "" && s.IdentityCommitment != "" {
		return invalid()
	}
	if l := s.Lease; l != nil && l.Action != SubscriptionExecute {
		return invalid()
	}
	if s.ClaudeUsage != nil && s.ClaudeUsage.Validate(account) != nil {
		return invalid()
	}
	if o := s.ServerOperation; o != nil {
		if o.Action != SubscriptionLogin && o.Action != SubscriptionLogout || o.Diagnostic != nil || o.CallbackForwarded {
			return invalid()
		}
		if o.ExpiresAt.Sub(o.StartedAt) > 15*time.Minute {
			return invalid()
		}
		if o.LoginMethod != 0 && o.LoginMethod != SubscriptionBrowserCode || o.CodeSubmissionID != "" && (o.CodeSubmissionID.Validate() != nil || o.LoginMethod != SubscriptionBrowserCode) || o.CodeConsumed && o.CodeSubmissionID == "" {
			return invalid()
		}
		if o.CleanupPhase != "" {
			// Only a settled operation can still owe vault cleanup. Later account
			// changes, such as a database restore, keep the checkpoint until the
			// bounded retirement of obsolete references is confirmed.
			if o.CleanupPhase != SubscriptionCredentialCleanupPending || o.State != SubscriptionSucceeded || o.NativeStarted {
				return invalid()
			}
		}
		if d := o.ClaudeDiagnostic; d != nil && (d.Validate() != nil || d.CorrelationID != o.ID || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
			return invalid()
		}
	}
	return nil
}

// ClaudeSetupTokenValid reports whether value has the closed shape of one
// Claude setup token. It validates syntax only; it never proves that the
// provider accepted the token or identifies a human account.
func ClaudeSetupTokenValid(value []byte) bool {
	const prefix = "sk-ant-oat01-"
	if len(value) < len(prefix)+24 || len(value) > 512 || string(value[:len(prefix)]) != prefix {
		return false
	}
	for _, b := range value[len(prefix):] {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}
