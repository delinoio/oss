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
	resolved, err := ResolveExecutionConfiguration(NewID(), 7, agent, 9, model, Priority, templates)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Instructions != templates[0].Contents+"\n\n"+templates[1].Contents || resolved.Options != agent.Options || resolved.Routing != RoundRobin || resolved.NativeModel != model.NativeID || resolved.ModelID != agent.ModelID {
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
	if _, err := ResolveExecutionConfiguration(NewID(), 7, agent, 9, model, Priority, templates); err == nil {
		t.Fatal("changed template order was accepted")
	}
}

func TestExecutionInstructionsNeverTruncateToFit(t *testing.T) {
	first, second := NewID(), NewID()
	agent := Agent{Name: "Fixture", Harness: Codex, ModelID: NewID(), Templates: []ID{first, second}, Options: AgentOptions{Permission: PermissionDefault}}
	model := Model{Name: "Fixture", NativeID: "fixture", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}
	templates := []AppliedTemplate{{ID: first, Revision: 1, Contents: strings.Repeat("a", 128<<10)}, {ID: second, Revision: 1, Contents: strings.Repeat("b", 128<<10)}}
	_, err := ResolveExecutionConfiguration(NewID(), 1, agent, 1, model, Priority, templates)
	if err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatal("instruction separator overflow was silently truncated")
	}
}
