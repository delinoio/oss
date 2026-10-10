// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func defaultFixture(provider ID, native string) *InlineModel {
	return &InlineModel{ModelIdentity: ModelIdentity{ProviderID: provider, NativeID: native}, MetadataSource: Unknown}
}
func TestHarnessDefaultsSourcePrecedenceAndExplicitEmpty(t *testing.T) {
	provider, foreign := NewID(), NewID()
	anchor := defaultFixture(provider, "legacy")
	selected := Agent{Name: "fixture", Harness: Codex, HarnessSelection: &HarnessSelection{}, Options: AgentOptions{Permission: PermissionDefault}, Effort: "legacy-high"}
	route := AgentSourceRoute{Model: anchor, ModelSelection: &InheritedValue[InlineModel]{State: InheritValue}}
	high, medium, empty := "high", "medium", ""
	server := []HarnessDefault{{Harness: Codex, Selection: HarnessSelection{Effort: &InheritedValue[string]{State: OverrideValue, Value: &high}}}, {Harness: Codex, ProviderID: provider, Model: defaultFixture(provider, "server")}, {Harness: Codex, ProviderID: foreign, Model: defaultFixture(foreign, "foreign")}}
	project := []HarnessDefault{{Harness: Codex, ProviderID: provider, APIProtocol: OpenAIResponses, Model: defaultFixture(provider, "project"), Selection: HarnessSelection{Effort: &InheritedValue[string]{State: OverrideValue, Value: &medium}}}}
	got, resolved, err := ResolveHarnessSource(selected, route, OpenAIResponses, server, project)
	if err != nil || resolved.Model.NativeID != "project" || got.Effort != "medium" || anchor.NativeID != "legacy" {
		t.Fatalf("incorrect inherited source resolution: %+v %+v %v", got, resolved, err)
	}
	selected.HarnessSelection.Effort = &InheritedValue[string]{State: OverrideValue, Value: &empty}
	explicit := *defaultFixture(provider, "agent")
	route.ModelSelection = &InheritedValue[InlineModel]{State: OverrideValue, Value: &explicit}
	got, resolved, err = ResolveHarnessSource(selected, route, OpenAIResponses, server, project)
	if err != nil || got.Effort != "" || resolved.Model.NativeID != "agent" {
		t.Fatalf("explicit empty/model override lost: %+v %+v %v", got, resolved, err)
	}
}
func TestHarnessDefaultsUnknownOrForeignBlocks(t *testing.T) {
	provider, foreign := NewID(), NewID()
	agent := Agent{Harness: Codex, HarnessSelection: &HarnessSelection{}}
	route := AgentSourceRoute{Model: defaultFixture(provider, "legacy"), ModelSelection: &InheritedValue[InlineModel]{State: InheritValue}}
	if _, _, err := ResolveHarnessSource(agent, route, OpenAIResponses, nil, nil); SafeError(err).Code != MissingInput {
		t.Fatalf("legacy selector became a default: %v", err)
	}
	model := *defaultFixture(foreign, "foreign")
	route.ModelSelection = &InheritedValue[InlineModel]{State: OverrideValue, Value: &model}
	if _, _, err := ResolveHarnessSource(agent, route, OpenAIResponses, nil, nil); SafeError(err).Code != InvalidArgument {
		t.Fatalf("foreign override accepted: %v", err)
	}
}
func TestHarnessInheritanceRejectsAmbiguousStates(t *testing.T) {
	empty := ""
	for _, v := range []InheritedValue[string]{{}, {State: InheritValue, Value: &empty}, {State: OverrideValue}} {
		if v.Validate() == nil {
			t.Fatalf("ambiguous state accepted: %+v", v)
		}
	}
	valid := InheritedValue[string]{State: OverrideValue, Value: &empty}
	raw, _ := json.Marshal(valid)
	var copy InheritedValue[string]
	if err := json.Unmarshal(raw, &copy); err != nil || copy.Validate() != nil || copy.Value == nil || *copy.Value != "" {
		t.Fatal("empty override failed round trip")
	}
}
func TestHarnessDefaultsNativeOptionsRemainExplicit(t *testing.T) {
	zero := uint32(0)
	concurrency := InheritedValue[uint32]{State: OverrideValue, Value: &zero}
	got := (HarnessSelection{MaxConcurrency: &concurrency}).Apply(Agent{Options: AgentOptions{Permission: PermissionDefault, MaxConcurrency: 12}})
	if got.Options.MaxConcurrency != 0 {
		t.Fatal("explicit zero became inheritance")
	}
}

func TestHarnessInheritanceRejectsNullInsteadOfOmission(t *testing.T) {
	for _, raw := range []string{`{"state":"inherit","value":null}`, `{"state":"override","value":null}`, `{"state":"inherit","foreign":true}`} {
		var value InheritedValue[string]
		if json.Unmarshal([]byte(raw), &value) == nil {
			t.Fatalf("ambiguous wire value accepted: %s", raw)
		}
	}
}
