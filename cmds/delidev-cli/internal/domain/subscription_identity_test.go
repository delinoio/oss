// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func TestSubscriptionServiceIdentityIsIndependentAndClosed(t *testing.T) {
	for _, service := range []SubscriptionService{SubscriptionChatGPT, SubscriptionClaude, SubscriptionGrok, SubscriptionOpenCodeGo} {
		a := Account{Alias: "Native", Type: SubscriptionAccount, SubscriptionService: service, Health: AccountDisconnected}
		m := Model{SourceKind: SubscriptionModel, SubscriptionService: service, NativeID: "native-model", Name: "Native model", Harnesses: []Harness{service.Harness()}, MetadataSource: UserDeclared}
		if a.Validate() != nil || m.Validate() != nil || !m.MatchesAccount(a, service.Harness()) {
			t.Fatal("valid independent identity rejected", service)
		}
		bad := a
		bad.ProviderID = NewID()
		if bad.Validate() == nil {
			t.Fatal("subscription accepted a provider")
		}
		bad.Type = APIAccount
		if bad.Validate() == nil {
			t.Fatal("API account accepted a service")
		}
		bad = a
		bad.SubscriptionService = "unknown"
		if bad.Validate() == nil {
			t.Fatal("unknown service accepted")
		}
		wrong := m
		wrong.Harnesses = []Harness{OpenCode}
		if service == SubscriptionOpenCodeGo {
			wrong.Harnesses = []Harness{Codex}
		}
		if wrong.Validate() == nil || m.MatchesAccount(a, wrong.Harnesses[0]) {
			t.Fatal("cross-harness authority")
		}
		other := a
		other.SubscriptionService = SubscriptionClaude
		if service == SubscriptionClaude {
			other.SubscriptionService = SubscriptionGrok
		}
		if m.MatchesAccount(other, service.Harness()) {
			t.Fatal("empty providers merged service authority")
		}
		permission := PermissionDefault
		if service == SubscriptionChatGPT {
			permission = PermissionReadOnly
		}
		agent := Agent{Name: "Native", Harness: service.Harness(), ModelID: NewID(), Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}, Options: AgentOptions{Permission: permission}}
		c, err := resolveInlineFixture(NewID(), 1, agent, 1, m, Priority, nil)
		if err != nil || c.ProviderID != "" || c.SubscriptionService != service || !c.Subscription {
			t.Fatal("snapshot lost native identity", err)
		}
		if service == SubscriptionChatGPT && c.Validate() != nil {
			t.Fatal("managed snapshot invalid")
		}
		agent.ReconfigurationRequired = true
		if _, err := resolveInlineFixture(NewID(), 1, agent, 1, m, Priority, nil); SafeError(err).Code != RecoveryRequired {
			t.Fatal("retired Agent obtained snapshot")
		}
	}
	api := Account{Alias: "API", ProviderID: NewID(), Type: APIAccount, Health: AccountDisconnected}
	m := Model{ProviderID: api.ProviderID, NativeID: "api-model", Name: "API", MetadataSource: Unknown}
	if api.Validate() != nil || m.Validate() != nil || !m.MatchesAccount(api, Codex) {
		t.Fatal("API-only compatibility changed")
	}
}

func TestOpenCodeGoAccountCannotBorrowNativeLoginQuotaOrCredentialGenerations(t *testing.T) {
	profile := OpenCodeGoProvider().LegacyAPIFormat()
	account := Account{Alias: "Go Plus", Type: SubscriptionAccount, SubscriptionService: SubscriptionOpenCodeGo, Health: AccountReady, Connection: &AccountConnection{ID: NewID(), Authentication: BearerAuth, APIFormat: &profile, ConnectedAt: time.Now().UTC()}}
	if account.Validate() != nil {
		t.Fatal("fixed protected Go connection rejected")
	}
	for _, change := range []func(*Account){
		func(a *Account) { a.Subscription = &SubscriptionState{} },
		func(a *Account) { a.ConfirmedExhausted = true },
		func(a *Account) { a.Connection.Authentication = SubscriptionAuth },
		func(a *Account) { a.Connection.CredentialID = NewID() },
		func(a *Account) { a.Connection.APIFormat.Endpoint = "https://foreign.example/v1" },
		func(a *Account) { a.ProviderID = NewID() },
	} {
		copied := account
		connection := *account.Connection
		format := *account.Connection.APIFormat
		connection.APIFormat = &format
		copied.Connection = &connection
		change(&copied)
		if copied.Validate() == nil {
			t.Fatal("foreign/native subscription authority was accepted")
		}
	}
}
