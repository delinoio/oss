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
	for name, mutate := range map[string]func(*Account){
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

func grokServerLogoutFixture() Account {
	now := time.Now().UTC()
	operation, generation := NewID(), NewID()
	return Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionGrok, Health: AccountRevoked, Connection: &AccountConnection{ID: NewID(), Authentication: SubscriptionAuth, ConnectedAt: now}, Subscription: &SubscriptionState{
		Generation: generation, IdentityCommitment: strings.Repeat("a", 64),
		Pending:         &SubscriptionOperation{ID: operation, Action: SubscriptionLogout, Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionQueued},
		ServerOperation: &ServerSubscriptionOperation{ID: operation, Epoch: NewID(), FinishID: NewID(), Action: SubscriptionLogout, Actor: Principal{Type: OwnerDevice}, State: SubscriptionPreparing, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}}
}

func TestGrokServerLogoutStateValidation(t *testing.T) {
	queued := grokServerLogoutFixture()
	if err := queued.Subscription.Validate(queued); err != nil || !queued.GrokLoggingOut() {
		t.Fatalf("queued Grok logout: %v %v", err, queued.GrokLoggingOut())
	}
	for name, mutate := range map[string]func(*Account){
		"claimed": func(a *Account) {
			o := a.Subscription.ServerOperation
			o.NativeStarted, o.GrokRevocation, o.Generation = true, GrokRevocationClaimed, a.Subscription.Generation
			a.Subscription.Pending.Phase = SubscriptionClaimed
		},
		"revoked": func(a *Account) {
			o := a.Subscription.ServerOperation
			o.NativeStarted, o.GrokRevocation, o.Generation = true, GrokRevocationConfirmed, a.Subscription.Generation
			a.Subscription.Pending.Phase = SubscriptionClaimed
			a.Subscription.RevokedGeneration = a.Subscription.Generation
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerLogoutFixture()
			mutate(&a)
			if err := a.Subscription.Validate(a); err != nil {
				t.Fatalf("Grok logout was rejected: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*Account){
		"claim before start": func(a *Account) { a.Subscription.ServerOperation.GrokRevocation = GrokRevocationClaimed },
		"unknown phase":      func(a *Account) { a.Subscription.ServerOperation.GrokRevocation = "invented" },
		"refresh claim":      func(a *Account) { a.Subscription.ServerOperation.RefreshClaimed = true },
		"login method":       func(a *Account) { a.Subscription.ServerOperation.LoginMethod = SubscriptionBrowserCallback },
		"callback":           func(a *Account) { a.Subscription.ServerOperation.CallbackForwarded = true },
		"revocation on refresh": func(a *Account) {
			a.Subscription.ServerOperation.Action, a.Subscription.Pending.Action = SubscriptionRefresh, SubscriptionRefresh
			a.Subscription.ServerOperation.GrokRevocation = GrokRevocationConfirmed
		},
		"revocation on chatgpt": func(a *Account) {
			a.SubscriptionService = SubscriptionChatGPT
			a.Subscription.ServerOperation.GrokRevocation = GrokRevocationConfirmed
		},
		"revoked generation chat": func(a *Account) {
			a.SubscriptionService = SubscriptionChatGPT
			a.Subscription.RevokedGeneration = NewID()
		},
		"bad revoked generation": func(a *Account) { a.Subscription.RevokedGeneration = "not-an-id" },
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerLogoutFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid Grok logout ownership was accepted")
			}
		})
	}
}

func TestGrokLoggingOutIsLimitedToAnAcceptedGrokLogout(t *testing.T) {
	for name, mutate := range map[string]func(*Account){
		"ready":    func(a *Account) { a.Health = AccountReady },
		"chatgpt":  func(a *Account) { a.SubscriptionService = SubscriptionChatGPT },
		"refresh":  func(a *Account) { a.Subscription.ServerOperation.Action = SubscriptionRefresh },
		"no op":    func(a *Account) { a.Subscription.Pending = nil },
		"no state": func(a *Account) { a.Subscription = nil },
		"api":      func(a *Account) { a.Type = APIAccount },
	} {
		t.Run(name, func(t *testing.T) {
			a := grokServerLogoutFixture()
			mutate(&a)
			if a.GrokLoggingOut() {
				t.Fatal("an account that is not logging out was reported as logging out")
			}
		})
	}
}
