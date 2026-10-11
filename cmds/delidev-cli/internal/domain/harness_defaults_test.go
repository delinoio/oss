// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestHarnessDefaultsResolveSelectedProfileAndExplicitEmpty(t *testing.T) {
	provider := NewID()
	account := Account{Type: APIAccount, ProviderID: provider, APIProtocol: OpenAIResponses}
	old := InlineModel{ModelIdentity: ModelIdentity{ProviderID: provider, NativeID: "old"}, MetadataSource: Unknown}
	model := old
	model.NativeID = "selected-profile"
	effort := "high"
	empty := ""
	options := AgentOptions{Permission: PermissionDefault, MaxConcurrency: 9}
	agent := Agent{Name: "Inherited", Harness: Codex, HarnessSettings: InheritHarnessSelection(), Routes: []AgentSourceRoute{{Model: &old, ModelMode: SettingInherit}}, Options: AgentOptions{Permission: PermissionDefault}}
	agent.HarnessSettings.Effort = InheritedSetting[string]{Mode: SettingOverride, Value: &empty}
	resolved, source, err := ResolveHarnessDefaults(agent, agent.Routes[0], account, nil, []HarnessDefault{{Harness: Codex, ProviderID: provider, APIProtocol: OpenAIChat, Model: &old, Effort: &effort, Options: &options}, {Harness: Codex, ProviderID: provider, APIProtocol: OpenAIResponses, Model: &model, Effort: &effort, Options: &options}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model.NativeID != "selected-profile" || resolved.Effort != "" || source.EffortOwner != "agent" {
		t.Fatalf("incorrect inheritance: %#v %#v", resolved, source)
	}
	model.NativeID = "changed-after-freeze"
	if resolved.Model.NativeID != "selected-profile" {
		t.Fatal("resolved model shares mutable default")
	}
}

func TestHarnessDefaultsMissingEvidenceBlocksRatherThanUsingLegacyModel(t *testing.T) {
	provider := NewID()
	model := InlineModel{ModelIdentity: ModelIdentity{ProviderID: provider, NativeID: "legacy"}, MetadataSource: Unknown}
	agent := Agent{Name: "Inherited", Harness: Codex, HarnessSettings: InheritHarnessSelection(), Options: AgentOptions{Permission: PermissionDefault}, Routes: []AgentSourceRoute{{Model: &model, ModelMode: SettingInherit}}}
	_, _, err := ResolveHarnessDefaults(agent, agent.Routes[0], Account{Type: APIAccount, ProviderID: provider}, nil, nil)
	if err == nil || SafeError(err).Code != MissingInput {
		t.Fatalf("missing default accepted: %v", err)
	}
}

func TestHarnessDefaultsProjectPrecedenceAndZeroOptionsOverride(t *testing.T) {
	provider := NewID()
	model := InlineModel{ModelIdentity: ModelIdentity{ProviderID: provider, NativeID: "native"}, MetadataSource: Unknown}
	serverEffort := "high"
	projectEffort := "medium"
	serverOptions := AgentOptions{Permission: PermissionFullAccess, MaxConcurrency: 4}
	zero := AgentOptions{Permission: PermissionDefault}
	agent := Agent{Name: "Inherited", Harness: Codex, HarnessSettings: InheritHarnessSelection(), Options: zero, Routes: []AgentSourceRoute{{Model: &model, ModelMode: SettingOverride}}}
	agent.HarnessSettings.Options = InheritedSetting[AgentOptions]{Mode: SettingOverride, Value: &zero}
	resolved, provenance, err := ResolveHarnessDefaults(agent, agent.Routes[0], Account{Type: APIAccount, ProviderID: provider}, []HarnessDefault{{Harness: Codex, Effort: &projectEffort}}, []HarnessDefault{{Harness: Codex, ProviderID: provider, Model: &model, Effort: &serverEffort, Options: &serverOptions}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effort != projectEffort || resolved.Options.MaxConcurrency != 0 || resolved.Options.Permission != PermissionDefault || provenance.ModelOwner != "agent" || provenance.EffortOwner != "project" || provenance.OptionsOwner != "agent" {
		t.Fatalf("wrong precedence: %#v %#v", resolved, provenance)
	}
}

func TestInheritedHarnessSettingRejectsAmbiguousStates(t *testing.T) {
	empty := ""
	for _, v := range []InheritedSetting[string]{{}, {Mode: SettingOverride}, {Mode: SettingInherit, Value: &empty}, {Mode: "unknown", Value: &empty}} {
		if v.Validate() == nil {
			t.Fatalf("ambiguous setting accepted: %#v", v)
		}
	}
}

func TestInheritedHarnessSettingRejectsNullAndUnknownFields(t *testing.T) {
	for _, raw := range []string{`{"mode":"inherit","value":null}`, `{"mode":"override","value":null}`, `{"mode":"inherit","future":true}`} {
		var value InheritedSetting[string]
		if Decode([]byte(raw), &value) == nil {
			t.Fatalf("ambiguous wire state accepted: %s", raw)
		}
	}
	var value InheritedSetting[string]
	if err := Decode([]byte(`{"mode":"override","value":""}`), &value); err != nil || value.Value == nil || *value.Value != "" {
		t.Fatalf("empty override rejected: %v %#v", err, value)
	}
}
