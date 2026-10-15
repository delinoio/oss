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

// grokLoginDiagnosticFixture builds the locally reconstructed diagnostic of a
// failed Go-owned login step, correlated with its operation.
func grokLoginDiagnosticFixture(code Code, operation ID) *GrokDiagnostic {
	d := GrokErrorDiagnostic(WithGrokDiagnostic("", GrokLogin, Fail(code, "The native operation did not complete.", "Check the Grok Build installation.")))
	d.CorrelationID = string(operation)
	return d
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
		a.Subscription.ServerOperation.GrokDiagnostic = grokLoginDiagnosticFixture(Unauthenticated, a.Subscription.ServerOperation.ID)
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
			a.Subscription.ServerOperation.GrokDiagnostic = grokLoginDiagnosticFixture(Internal, a.Subscription.ServerOperation.ID)
		},
		"foreign correlation": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.CorrelationID = string(NewID())
		},
		"unknown phase": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.Phase = GrokPhase("invented")
		},
		"unknown code": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.GrokDiagnostic.Code = "invented"
		},
		"canceled carries diagnostic": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.State = SubscriptionCanceled
		},
		"codex and grok diagnostics together": func(a *Account) {
			diagnostic(a)
			a.Subscription.ServerOperation.Diagnostic = &CodexDiagnostic{CorrelationID: string(a.Subscription.ServerOperation.ID)}
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

func grokServerDeviceLoginFixture() Account {
	a := grokServerLoginFixture()
	a.Subscription.Pending.DeviceCode = true
	return a
}

func TestGrokServerDeviceLoginStateValidation(t *testing.T) {
	queued := grokServerDeviceLoginFixture()
	if err := queued.Subscription.Validate(queued); err != nil {
		t.Fatalf("queued Grok device login was rejected: %v", err)
	}
	claimed := grokServerDeviceLoginFixture()
	o := claimed.Subscription.ServerOperation
	o.State, o.LoginMethod, o.CodeSubmissionID, o.CodeConsumed, o.NativeStarted = SubscriptionWaiting, SubscriptionDeviceCode, NewID(), true, true
	claimed.Subscription.Pending.Phase = SubscriptionClaimed
	if err := claimed.Subscription.Validate(claimed); err != nil {
		t.Fatalf("claimed Grok device login was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Account){
		"device method on a browser request": func(a *Account) {
			a.Subscription.Pending.DeviceCode = false
			a.Subscription.ServerOperation.LoginMethod = SubscriptionDeviceCode
		},
		"browser method on a device request": func(a *Account) { a.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCallback },
		"forwarded callback on a device login": func(a *Account) {
			a.Subscription.ServerOperation.LoginMethod = SubscriptionDeviceCode
			a.Subscription.ServerOperation.CallbackForwarded = true
		},
		"device method on a refresh": func(a *Account) {
			a.Health = AccountReady
			a.Connection = &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: time.Now().UTC()}
			a.Subscription.Generation = NewID()
			a.Subscription.IdentityCommitment = strings.Repeat("a", 64)
			a.Subscription.ServerOperation.Action = SubscriptionRefresh
			a.Subscription.Pending.Action = SubscriptionRefresh
			a.Subscription.ServerOperation.LoginMethod = SubscriptionDeviceCode
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerDeviceLoginFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid Grok device login ownership was accepted")
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

func grokServerRefreshFixture() Account {
	a := grokServerLoginFixture()
	a.Health = AccountReady
	a.Connection = &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: time.Now().UTC()}
	a.Subscription.Generation = NewID()
	a.Subscription.IdentityCommitment = strings.Repeat("a", 64)
	a.Subscription.ServerOperation.Action = SubscriptionRefresh
	a.Subscription.Pending.Action = SubscriptionRefresh
	return a
}

func TestGrokServerRefreshStateValidation(t *testing.T) {
	queued := grokServerRefreshFixture()
	if err := queued.Subscription.Validate(queued); err != nil {
		t.Fatalf("queued Grok refresh was rejected: %v", err)
	}
	claimed := grokServerRefreshFixture()
	o := claimed.Subscription.ServerOperation
	o.NativeStarted, o.RefreshClaimed, o.Generation = true, true, claimed.Subscription.Generation
	claimed.Subscription.Pending.Phase = SubscriptionClaimed
	if err := claimed.Subscription.Validate(claimed); err != nil {
		t.Fatalf("claimed Grok refresh was rejected: %v", err)
	}
	settled := grokServerRefreshFixture()
	settled.Subscription.ServerOperation.State = SubscriptionSucceeded
	settled.Subscription.ServerOperation.RefreshClaimed = true
	settled.Subscription.Pending = nil
	if err := settled.Subscription.Validate(settled); err != nil {
		t.Fatalf("settled Grok refresh keeping its claim was rejected: %v", err)
	}
	admitted := grokServerRefreshFixture()
	admitted.Subscription.ServerOperation.HealthBefore = AccountExpired
	if err := admitted.Subscription.Validate(admitted); err != nil {
		t.Fatalf("a refresh admitted under an expired account was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Account){
		"unknown admitted health": func(a *Account) { a.Subscription.ServerOperation.HealthBefore = "bogus" },
		"revoked admitted health": func(a *Account) { a.Subscription.ServerOperation.HealthBefore = AccountRevoked },
		"admitted health on login": func(a *Account) {
			a.Subscription.ServerOperation.Action = SubscriptionLogin
			a.Subscription.Pending.Action = SubscriptionLogin
			a.Subscription.ServerOperation.HealthBefore = AccountReady
		},
		"admitted health on another service": func(a *Account) {
			a.SubscriptionService = SubscriptionChatGPT
			a.Subscription.ServerOperation.HealthBefore = AccountReady
		},
		"claim before the lease":  func(a *Account) { a.Subscription.ServerOperation.RefreshClaimed = true },
		"login code on a refresh": func(a *Account) { a.Subscription.ServerOperation.CodeConsumed = true },
		"login method":            func(a *Account) { a.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCallback },
		"callback on a refresh":   func(a *Account) { a.Subscription.ServerOperation.CallbackForwarded = true },
		"claim on a login": func(a *Account) {
			a.Subscription.ServerOperation.Action = SubscriptionLogin
			a.Subscription.Pending.Action = SubscriptionLogin
			a.Subscription.ServerOperation.RefreshClaimed = true
			a.Subscription.ServerOperation.NativeStarted = true
		},
		"claim on another service": func(a *Account) {
			a.SubscriptionService = SubscriptionChatGPT
			a.Subscription.ServerOperation.NativeStarted = true
			a.Subscription.ServerOperation.RefreshClaimed = true
			a.Subscription.ServerOperation.Generation = a.Subscription.Generation
			a.Subscription.Pending.Phase = SubscriptionClaimed
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerRefreshFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid Grok refresh ownership was accepted")
			}
		})
	}
}

func TestRefreshClaimIsOmittedFromLegacyServerOperations(t *testing.T) {
	raw, err := json.Marshal(ServerSubscriptionOperation{})
	if err != nil || strings.Contains(string(raw), "refresh_claimed") {
		t.Fatalf("legacy server operation gained a refresh claim: %s %v", raw, err)
	}
}
