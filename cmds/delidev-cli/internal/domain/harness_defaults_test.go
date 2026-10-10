// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func harnessValue[T any](v T) HarnessSetting[T] {
	return HarnessSetting[T]{Mode: OverrideSetting, Value: &v}
}
func harnessFixture() (Agent, InlineModel) {
	model := InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "retained-source-only"}, MetadataSource: Unknown}
	a := Agent{Name: "Inherited", Harness: Codex, Routes: []AgentSourceRoute{{Model: &model, ModelInheritance: InheritSetting, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}}}, Options: AgentOptions{Permission: PermissionDefault}, HarnessSettings: InheritedHarnessSettings()}
	return a, model
}
func harnessDefault(model InlineModel, effort string) HarnessDefault {
	return HarnessDefault{Harness: Codex, ProviderID: model.ProviderID, Model: harnessValue(model), AgentHarnessSettings: AgentHarnessSettings{Effort: harnessValue(effort), Options: HarnessSetting[AgentOptions]{Mode: InheritSetting}}}
}
func TestHarnessDefaultsResolveSourcePrecedenceAndExplicitEmpty(t *testing.T) {
	a, model := harnessFixture()
	sourceDefault := model
	sourceDefault.NativeID = "server-model"
	settings := DefaultSettings()
	settings.HarnessDefaults = []HarnessDefault{harnessDefault(sourceDefault, "server-effort")}
	profile := sourceDefault
	profile.NativeID = "profile-model"
	entry := harnessDefault(profile, "profile-effort")
	entry.APIProtocol = OpenAIResponses
	settings.HarnessDefaults = append(settings.HarnessDefaults, entry)
	projectModel := sourceDefault
	projectModel.NativeID = "project-model"
	p := &Project{HarnessDefaults: []HarnessDefault{harnessDefault(projectModel, "project-effort")}}
	resolved, err := ResolveInheritedHarness(a, a.Routes[0], nil, settings, OpenAIResponses)
	if err != nil || resolved.Model.NativeID != "profile-model" || resolved.Effort != "profile-effort" {
		t.Fatalf("profile: %+v %v", resolved, err)
	}
	resolved, err = ResolveInheritedHarness(a, a.Routes[0], p, settings, OpenAIResponses)
	if err != nil || resolved.Model.NativeID != "project-model" || resolved.Effort != "project-effort" {
		t.Fatalf("project: %+v %v", resolved, err)
	}
	a.Routes[0].ModelInheritance = OverrideSetting
	a.HarnessSettings.Effort = harnessValue("")
	resolved, err = ResolveInheritedHarness(a, a.Routes[0], p, settings, OpenAIResponses)
	if err != nil || resolved.Model.NativeID != model.NativeID || resolved.Effort != "" {
		t.Fatalf("explicit: %+v %v", resolved, err)
	}
	if resolved.HarnessSettings != nil {
		t.Fatal("mutable inheritance leaked into execution")
	}
}
func TestHarnessDefaultsSourceIsolationAndUnknownEvidence(t *testing.T) {
	a, m1 := harnessFixture()
	_, m2 := harnessFixture()
	m1.NativeID = "native-one"
	m2.NativeID = "native-two"
	s := DefaultSettings()
	s.HarnessDefaults = []HarnessDefault{harnessDefault(m1, "medium"), harnessDefault(m2, "high")}
	first, err := ResolveInheritedHarness(a, a.Routes[0], nil, s, "")
	if err != nil || first.Model.NativeID != "native-one" {
		t.Fatal(first, err)
	}
	a.Routes[0].Model = &m2
	second, err := ResolveInheritedHarness(a, a.Routes[0], nil, s, "")
	if err != nil || second.Model.NativeID != "native-two" {
		t.Fatal(second, err)
	}
	s.HarnessDefaults = s.HarnessDefaults[:1]
	if _, err = ResolveInheritedHarness(a, a.Routes[0], nil, s, ""); err == nil || SafeError(err).Message != "Harness model default is unavailable." {
		t.Fatalf("missing evidence silently used retained model: %v", err)
	}
}
func TestHarnessDefaultsRejectMalformedAndPreserveClearPresence(t *testing.T) {
	for _, s := range []HarnessSetting[string]{{Mode: InheritSetting, Value: ptr("foreign")}, {Mode: OverrideSetting}, {Mode: "unknown"}} {
		if s.Validate() == nil {
			t.Fatal("invalid state accepted")
		}
	}
	raw, err := json.Marshal(Settings{HarnessDefaults: []HarnessDefault{}})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if string(fields["harness_defaults"]) != "[]" {
		t.Fatalf("explicit clear erased: %s", raw)
	}
}
func ptr[T any](v T) *T { return &v }
func TestHarnessAgentUpgradeClearsSelectionsAndRetainsReferences(t *testing.T) {
	a, m := harnessFixture()
	a.HarnessSettings = nil
	a.Routes[0].ModelInheritance = ""
	a.Effort = "high"
	a.Options.ServiceTier = "fast"
	a.Templates = []ID{NewID()}
	raw, _ := json.Marshal(a)
	upgraded, changed, err := UpgradeHarnessAgent(raw)
	if err != nil || !changed {
		t.Fatal(err)
	}
	var next Agent
	if err := Decode(upgraded, &next); err != nil {
		t.Fatal(err)
	}
	if next.HarnessSettings == nil || next.Routes[0].ModelInheritance != InheritSetting || next.Effort != "" || next.Options.ServiceTier != "" || next.Routes[0].Model.NativeID != m.NativeID || next.Templates[0] != a.Templates[0] || next.Routes[0].Accounts[0] != a.Routes[0].Accounts[0] {
		t.Fatalf("lost source/references or retained explicit selector: %+v", next)
	}
	repeated, changed, err := UpgradeHarnessAgent(upgraded)
	if err != nil || changed || string(repeated) != string(upgraded) {
		t.Fatal("upgrade not idempotent", err)
	}
}
