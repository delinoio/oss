package domain

import (
	"strings"
	"testing"
)

func TestExecutionConfigurationPreservesOrderedInstructionsAndOptions(t *testing.T) {
	first, second := NewID(), NewID()
	route := RoundRobin
	agent := Agent{Name: "Fixture", Harness: Codex, ModelID: NewID(), Effort: "high", Accounts: []WeightedAccount{{ID: NewID(), Weight: 3}}, Routing: &route, Templates: []ID{second, first}, Options: AgentOptions{Permission: PermissionReadOnly, ServiceTier: "fast", SubagentEffort: "low", SubagentModel: "child", MaxConcurrency: 2, ApprovalPolicy: "on-request", ApprovalReviewModel: "review"}}
	model := Model{Name: "Fixture", NativeID: "canonical-native", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}
	templates := []AppliedTemplate{{ID: second, Revision: 2, Contents: "  Keep whitespace 한글\n"}, {ID: first, Revision: 5, Contents: "Then this 🐦"}}
	resolved, err := resolveInlineFixture(NewID(), 7, agent, 9, model, Priority, templates)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Instructions != templates[0].Contents+"\n\n"+templates[1].Contents || resolved.Options != agent.Options || resolved.Routing != RoundRobin || resolved.NativeModel != model.NativeID || resolved.ModelID != (ModelIdentity{ProviderID: model.ProviderID, SubscriptionService: model.SubscriptionService, NativeID: model.NativeID}).Key() {
		t.Fatal("resolved configuration changed exact user selections")
	}
	digest, err := resolved.Digest()
	if err != nil || len(digest) != 64 {
		t.Fatal("configuration digest missing")
	}
	agent.Accounts[0].Weight = 99
	templates[0].Contents = "later edited"
	again, err := resolved.Digest()
	if err != nil || again != digest {
		t.Fatal("later source mutation changed the snapshot")
	}
	templates[0].ID = first
	if _, err := resolveInlineFixture(NewID(), 7, agent, 9, model, Priority, templates); err == nil {
		t.Fatal("changed template order was accepted")
	}
}

func TestExecutionInstructionsNeverTruncateToFit(t *testing.T) {
	first, second := NewID(), NewID()
	agent := Agent{Name: "Fixture", Harness: Codex, ModelID: NewID(), Templates: []ID{first, second}, Options: AgentOptions{Permission: PermissionDefault}}
	model := Model{Name: "Fixture", NativeID: "fixture", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}
	templates := []AppliedTemplate{{ID: first, Revision: 1, Contents: strings.Repeat("a", 128<<10)}, {ID: second, Revision: 1, Contents: strings.Repeat("b", 128<<10)}}
	_, err := resolveInlineFixture(NewID(), 1, agent, 1, model, Priority, templates)
	if err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatal("instruction separator overflow was silently truncated")
	}
}

func TestManagedSubscriptionPreservesEveryPermission(t *testing.T) {
	for _, permission := range []PermissionMode{PermissionDefault, PermissionReadOnly, PermissionWorkspaceWrite, PermissionFullAccess} {
		t.Run(string(permission), func(t *testing.T) {
			configuration := managedSubscriptionExecutionConfiguration(t, permission)
			original, err := configuration.Digest()
			if err != nil || configuration.Validate() != nil {
				t.Fatal("subscription permission rejected", err)
			}
			after, err := configuration.Digest()
			if err != nil || after != original || configuration.Options.Permission != permission {
				t.Fatal("permission changed the immutable snapshot")
			}
		})
	}
}

func managedSubscriptionExecutionConfiguration(t *testing.T, permission PermissionMode) ExecutionConfiguration {
	t.Helper()
	account := NewID()
	modelID := NewID()
	agent := Agent{Name: "Managed fixture", Harness: Codex, ModelID: modelID, Accounts: []WeightedAccount{{ID: account, Weight: 1}}, Options: AgentOptions{Permission: permission}}
	model := Model{Name: "Managed fixture", NativeID: "managed-fixture", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}
	configuration, err := resolveInlineFixture(NewID(), 1, agent, 1, model, Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	configuration.Subscription = true
	return configuration
}

func TestOpenCodeContextSnapshotRequiresKnownSource(t *testing.T) {
	limit := uint64(128000)
	agent := Agent{Name: "Fixture", Harness: OpenCode, ModelID: NewID(), Options: AgentOptions{Permission: PermissionDefault}}
	model := Model{Name: "Fixture", NativeID: "fixture", ProviderID: NewID(), Harnesses: []Harness{OpenCode}, ContextLimit: &limit}
	agentID := NewID()
	for _, source := range []EvidenceSource{Unknown, Known, UserDeclared} {
		t.Run(string(source), func(t *testing.T) {
			model.MetadataSource = source
			if err := model.Validate(); err != nil {
				t.Fatal(err)
			}
			snapshot, err := resolveInlineFixture(agentID, 1, agent, 1, model, Priority, nil)
			if err != nil || snapshot.Validate() != nil {
				t.Fatal("saved model cannot dispatch", err)
			}
			if source != Unknown {
				if snapshot.OpenCodeContext == nil || snapshot.OpenCodeContext.Tokens != limit || snapshot.OpenCodeContext.Source != source {
					t.Fatal("known context lost")
				}
				return
			}
			if snapshot.OpenCodeContext != nil {
				t.Fatal("unknown metadata acquired context authority")
			}
			model.ContextLimit = nil
			legacy, err := resolveInlineFixture(agentID, 1, agent, 1, model, Priority, nil)
			model.ContextLimit = &limit
			if err != nil {
				t.Fatal(err)
			}
			originalDigest, err := legacy.Digest()
			digest, digestErr := snapshot.Digest()
			if err != nil || digestErr != nil || digest != originalDigest {
				t.Fatal("omitted context changed legacy digest")
			}
		})
	}
}
