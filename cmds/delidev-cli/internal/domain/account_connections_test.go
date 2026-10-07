// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func generationAccount() Account {
	root := NewID()
	now := time.Now().UTC()
	old := ProviderAPIFormat{Protocol: OpenAIResponses, Endpoint: "https://original.example.test/v1", Authentication: BearerAuth}
	current := ProviderAPIFormat{Protocol: OpenAIChat, Endpoint: "https://current.example.test/v1", Authentication: BearerAuth}
	a := Account{Alias: "Fixture", Type: APIAccount, ProviderID: NewID(), APIProtocol: OpenAIChat, Enabled: true, Health: AccountUnverified,
		Connection:          &AccountConnection{ID: NewID(), CredentialID: root, ConnectedAt: now, Authentication: BearerAuth, APIFormat: &current},
		RetainedConnections: []AccountConnectionGeneration{{Connection: AccountConnection{ID: root, ConnectedAt: now, Authentication: BearerAuth, APIFormat: &old}, Health: AccountReady, Validation: &AccountValidation{RequestID: NewID(), ConnectionID: root, State: Observed, Authentication: CredentialAccepted, ObservedAt: now}}}}
	return a
}

func TestAccountConnectionGenerationsPreserveOriginalEvidence(t *testing.T) {
	a := generationAccount()
	a.Catalog = &CatalogObservation{RequestID: NewID(), ConnectionID: a.Connection.ID, State: Observed, ObservedAt: time.Now().UTC()}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	a.ConfirmedExhausted = true
	original := a.ForConnection(a.RetainedConnections[0].Connection.ID)
	if original.Health != AccountReady || original.APIProtocol != OpenAIResponses || original.Validation == nil || !original.ConfirmedExhausted || original.Connection.CredentialReferenceID() != a.Connection.CredentialReferenceID() {
		t.Fatal("lost independent evidence or shared controls")
	}
	if a.ForConnection(NewID()).Connection != nil {
		t.Fatal("unknown generation granted authority")
	}
	a.Connection = nil
	if a.ForConnection(original.Connection.ID).Connection != nil {
		t.Fatal("disconnect retained authority")
	}
}

func TestAccountConnectionGenerationsRejectForgedReferences(t *testing.T) {
	for _, mutate := range []func(*Account){
		func(a *Account) { a.Connection.CredentialID = NewID() },
		func(a *Account) { a.RetainedConnections[0].Connection.CredentialID = NewID() },
		func(a *Account) { a.RetainedConnections[0].Connection.ID = a.Connection.ID },
		func(a *Account) { a.RetainedConnections[0].Connection.APIFormat = nil },
		func(a *Account) { a.RetainedConnections[0].Connection.Authentication = KeylessAuth },
		func(a *Account) { a.RetainedConnections[0].Validation.ConnectionID = NewID() },
		func(a *Account) { a.RetainedConnections = nil },
		func(a *Account) { a.RetainedConnections = append(a.RetainedConnections, a.RetainedConnections[0]) },
	} {
		a := generationAccount()
		mutate(&a)
		if a.Validate() == nil {
			t.Fatal("forged generation accepted")
		}
	}
}
