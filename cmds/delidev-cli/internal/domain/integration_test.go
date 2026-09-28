package domain

import (
	"testing"
	"time"
)

func TestIntegrationGenerationAndIdentityValidation(t *testing.T) {
	base := Integration{IntegrationDefinition: IntegrationDefinition{Name: "Work", Provider: GitHubCom, TokenKind: FineGrainedPAT, ResourceOwner: "test-org"}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", "-org", "org/other", "org.example", "org "} {
		changed := base
		changed.ResourceOwner = owner
		if changed.Validate() == nil {
			t.Fatalf("owner accepted %q", owner)
		}
	}
	id := NewID()
	validation := IntegrationValidation{GenerationID: id, State: IntegrationIdentityVerified, CheckedAt: time.Now(), Identity: &GitHubIdentity{ID: "17", NodeID: "U_17", Login: "test-user"}}
	base.Connection = &IntegrationConnection{GenerationID: id, ConnectedAt: time.Now(), Validation: &validation}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	validation.GenerationID = NewID()
	if base.Validate() == nil {
		t.Fatal("foreign generation accepted")
	}
	validation.GenerationID = id
	validation.State = IntegrationInvalidToken
	if base.Validate() == nil {
		t.Fatal("failed identity retained")
	}
	validation.Identity = nil
	validation.Problem = Fail(Unauthenticated, "Invalid token.", "")
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Pending = &IntegrationPending{RequestID: NewID(), CompletionID: NewID(), ExpectedRevision: 1, Operation: IntegrationDelete, StartedAt: time.Now()}
	if base.Validate() == nil {
		t.Fatal("connection survived pending denial")
	}
	base.Connection = nil
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Pending.Operation = IntegrationReplaceToken
	if base.Validate() == nil {
		t.Fatal("unbound replacement accepted")
	}
	base.Pending.GenerationID = base.Pending.RequestID
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
