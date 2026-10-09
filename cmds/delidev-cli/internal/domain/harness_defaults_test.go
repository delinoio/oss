// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func inheritedOverride[T any](value T) InheritedValue[T] {
	return InheritedValue[T]{State: HarnessOverride, Value: &value}
}
func TestHarnessInheritanceSpecificityAndEmptyOverrides(t *testing.T) {
	provider, globalModel, sourceModel, profileModel, projectModel := NewID(), NewID(), NewID(), NewID(), NewID()
	entry := func(model ID, scope ID, protocol APIProtocol) HarnessDefault {
		v := InheritedHarnessValues()
		v.Model = inheritedOverride(model)
		v.Effort = inheritedOverride("high")
		return HarnessDefault{Harness: Codex, ProviderID: scope, APIProtocol: protocol, Values: v}
	}
	server := []HarnessDefault{entry(profileModel, provider, OpenAIResponses), entry(globalModel, "", ""), entry(sourceModel, provider, "")}
	project := []HarnessDefault{entry(projectModel, provider, "")}
	agent := InheritedHarnessValues()
	agent.Effort = inheritedOverride("")
	effective := EffectiveHarnessValues(Codex, provider, "", OpenAIResponses, server, project, agent)
	if effective.Model.Value == nil || *effective.Model.Value != projectModel || effective.Effort.Value == nil || *effective.Effort.Value != "" {
		t.Fatal("precedence or explicit empty override lost")
	}
	effective = EffectiveHarnessValues(Codex, provider, "", OpenAIResponses, server, nil, InheritedHarnessValues())
	if *effective.Model.Value != profileModel {
		t.Fatal("document order changed profile specificity")
	}
	other := EffectiveHarnessValues(ClaudeCode, provider, "", AnthropicMessages, server, nil, InheritedHarnessValues())
	if other.Model.Value != nil {
		t.Fatal("another harness inherited a foreign model")
	}
}
func TestHarnessInheritanceRejectsMalformedStatesAndUnknownModels(t *testing.T) {
	value := ""
	for _, selection := range []InheritedValue[string]{{State: HarnessInherit, Value: &value}, {State: HarnessOverride}, {State: "unknown"}} {
		if selection.Validate() == nil {
			t.Fatal("invalid inheritance accepted")
		}
	}
	if _, err := InheritedHarnessValues().Apply(Agent{}); err == nil || SafeError(err).Code != MissingInput {
		t.Fatal("unknown inherited model must block")
	}
	v := InheritedHarnessValues()
	v.Model = inheritedOverride(NewID())
	if ValidateHarnessDefaults([]HarnessDefault{{Harness: Codex, Values: v}, {Harness: Codex, Values: v}}) == nil {
		t.Fatal("duplicate default scope accepted")
	}
}
