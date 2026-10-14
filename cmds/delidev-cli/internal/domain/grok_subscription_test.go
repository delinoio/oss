// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func grokServerLoginFixture() Account {
	now := time.Now().UTC()
	operation := NewID()
	return Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionGrok, Health: AccountDisconnected, Subscription: &SubscriptionState{
		Pending:         &SubscriptionOperation{ID: operation, Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionQueued},
		ServerOperation: &ServerSubscriptionOperation{ID: operation, Epoch: NewID(), FinishID: NewID(), Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, State: SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}}
}

func TestGrokServerLoginStateValidation(t *testing.T) {
	valid := grokServerLoginFixture()
	if err := valid.Subscription.Validate(valid); err != nil {
		t.Fatalf("Grok server login was rejected: %v", err)
	}
	claimed := grokServerLoginFixture()
	claimed.Subscription.ServerOperation.State = SubscriptionWaiting
	claimed.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCallback
	claimed.Subscription.ServerOperation.CodeSubmissionID = NewID()
	claimed.Subscription.ServerOperation.CodeConsumed = true
	claimed.Subscription.ServerOperation.CallbackForwarded = true
	claimed.Subscription.ServerOperation.NativeStarted = true
	claimed.Subscription.Pending.Phase = SubscriptionClaimed
	if err := claimed.Subscription.Validate(claimed); err != nil {
		t.Fatalf("claimed Grok login was rejected: %v", err)
	}
	diagnostic := func(a *Account) {
		a.Subscription.ServerOperation.State = SubscriptionFailed
		a.Subscription.Pending = nil
		a.Subscription.ServerOperation.GrokDiagnostic = &GrokDiagnostic{Phase: GrokDiagnosticLogin, Code: Unauthenticated, CorrelationID: a.Subscription.ServerOperation.ID}
	}
	failed := grokServerLoginFixture()
	diagnostic(&failed)
	if err := failed.Subscription.Validate(failed); err != nil {
		t.Fatalf("failed Grok login with a diagnostic was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Account){
		"consumed without claim id": func(a *Account) { a.Subscription.ServerOperation.CodeConsumed = true },
		"bad submission id":         func(a *Account) { a.Subscription.ServerOperation.CodeSubmissionID = "not-an-id" },
		"code method":               func(a *Account) { a.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCode },
		"diagnostic while preparing": func(a *Account) {
			a.Subscription.ServerOperation.GrokDiagnostic = &GrokDiagnostic{Phase: GrokDiagnosticLogin, Code: Internal, CorrelationID: a.Subscription.ServerOperation.ID}
		},
		"foreign correlation": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.CorrelationID = NewID()
		},
		"unknown phase": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.Phase = 99
		},
		"unknown code": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.Code = "invented"
		},
		"chatgpt carries grok diagnostic": func(a *Account) {
			diagnostic(a)
			a.SubscriptionService = SubscriptionChatGPT
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerLoginFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid Grok server login ownership was accepted")
			}
		})
	}
}

func TestGrokDiagnosticIsOmittedFromLegacyServerOperations(t *testing.T) {
	raw, err := json.Marshal(ServerSubscriptionOperation{})
	if err != nil || strings.Contains(string(raw), "grok_diagnostic") {
		t.Fatalf("legacy server operation gained a Grok diagnostic: %s %v", raw, err)
	}
}
