// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func managedClaudeFixture() Account {
	now := time.Now().UTC()
	operation := NewID()
	return Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionClaude, Health: AccountDisconnected, Subscription: &SubscriptionState{
		ClaudeCredential: ClaudeCredentialServerToken,
		Pending:          &SubscriptionOperation{ID: operation, Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionQueued},
		ServerOperation:  &ServerSubscriptionOperation{ID: operation, Epoch: NewID(), FinishID: NewID(), Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, State: SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}}
}

func TestManagedClaudeStateValidation(t *testing.T) {
	valid := managedClaudeFixture()
	if err := valid.Subscription.Validate(valid); err != nil || !valid.ManagedClaudeAccount() {
		t.Fatalf("server-token login was rejected: %v", err)
	}
	commitment := strings.Repeat("ab", 32)
	for name, mutate := range map[string]func(*Account){
		"native profile": func(a *Account) { a.Subscription.NativeProfileID = NewID() },
		"owner machine":  func(a *Account) { a.Subscription.OwnerMachineID = NewID() },
		"unknown mode":   func(a *Account) { a.Subscription.ClaudeCredential = "unknown" },
		"refresh": func(a *Account) {
			a.Subscription.ServerOperation.Action = SubscriptionRefresh
			a.Subscription.Pending.Action = SubscriptionRefresh
		},
		"callback method":    func(a *Account) { a.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCallback },
		"consumed no claim":  func(a *Account) { a.Subscription.ServerOperation.CodeConsumed = true },
		"codex diagnostic":   func(a *Account) { a.Subscription.ServerOperation.Diagnostic = &CodexDiagnostic{} },
		"identity no gen":    func(a *Account) { a.Subscription.IdentityCommitment = commitment },
		"generation no hash": func(a *Account) { a.Subscription.Generation = NewID(); a.Subscription.IdentityCommitment = "short" },
		"non execute lease":  func(a *Account) { a.Subscription.Lease = &SubscriptionLease{Action: SubscriptionLogin} },
		"observation":        func(a *Account) { a.Subscription.QuotaState = Observed },
		"expiry too long": func(a *Account) {
			a.Subscription.ServerOperation.ExpiresAt = a.Subscription.ServerOperation.StartedAt.Add(time.Hour)
		},
		"waiting diagnostic":  func(a *Account) { a.Subscription.ServerOperation.ClaudeDiagnostic = &NativeSubscriptionDiagnostic{} },
		"chatgpt claims mode": func(a *Account) { a.SubscriptionService = SubscriptionChatGPT },
	} {
		t.Run(name, func(t *testing.T) {
			a := managedClaudeFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid managed Claude ownership was accepted")
			}
		})
	}
}

func TestManagedClaudeModeIsOmittedForDeviceAccounts(t *testing.T) {
	raw, err := json.Marshal(SubscriptionState{})
	if err != nil || strings.Contains(string(raw), "claude_credential") {
		t.Fatalf("historical device-owned accounts gained a credential mode: %s %v", raw, err)
	}
	raw, err = json.Marshal(ServerSubscriptionOperation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"login_method", "code_submission_id", "code_consumed", "claude_diagnostic"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("legacy server operation gained %s", key)
		}
	}
}
