// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func completeHarnessFixture(model string) HarnessSettings {
	return HarnessSettings{Version: 1, Model: OverrideHarnessSetting(model), Effort: OverrideHarnessSetting("medium"), Permission: OverrideHarnessSetting(PermissionReadOnly), ClaudePermission: OverrideHarnessSetting(ClaudePermissionMode("")), ApprovalPolicy: OverrideHarnessSetting("never"), ApprovalsReviewer: OverrideHarnessSetting(ApprovalsReviewer("")), SubagentModel: OverrideHarnessSetting(""), SubagentEffort: OverrideHarnessSetting(""), MaxConcurrency: OverrideHarnessSetting(uint32(3)), ApprovalReviewModel: OverrideHarnessSetting(""), ServiceTier: OverrideHarnessSetting("")}
}
func TestHarnessDefaultsSourcePrecedenceEmptyAndZero(t *testing.T) {
	providerA, providerB := NewID(), NewID()
	for _, provider := range []ID{providerA, providerB} {
		source := HarnessDefaultScope{Harness: Codex, Source: HarnessAPISource, ProviderID: provider, APIProtocol: OpenAIResponses}
		d := &HarnessDefaults{Version: 1, Entries: []HarnessDefaultEntry{
			{Scope: HarnessDefaultScope{Harness: Codex, Source: HarnessAnySource}, Settings: completeHarnessFixture("harness")},
			{Scope: HarnessDefaultScope{Harness: Codex, Source: HarnessAPISource, ProviderID: providerB}, Settings: HarnessSettings{Version: 1, Model: OverrideHarnessSetting("provider-b")}},
			{Scope: HarnessDefaultScope{Harness: Codex, Source: HarnessAPISource, ProviderID: providerA}, Settings: HarnessSettings{Version: 1, Model: OverrideHarnessSetting("provider-a")}},
			{Scope: HarnessDefaultScope{Harness: Codex, Source: HarnessAPISource}, Settings: HarnessSettings{Version: 1, Model: OverrideHarnessSetting("api")}},
		}}
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
		server := HarnessSettingOrigin{Layer: HarnessServerLayer, ID: NewID(), Revision: 4}
		project := HarnessSettingOrigin{Layer: HarnessProjectLayer, ID: NewID(), Revision: 2}
		agent := HarnessSettingOrigin{Layer: HarnessAgentLayer, ID: NewID(), Revision: 3}
		layers := []HarnessSettingsLayer{{Settings: HarnessSettings{Version: 1, Effort: OverrideHarnessSetting(""), MaxConcurrency: OverrideHarnessSetting(uint32(0))}, Origin: agent}, {Settings: HarnessSettings{Version: 1, Effort: OverrideHarnessSetting("high")}, Origin: project}}
		layers = append(layers, HarnessDefaultLayers(d, source, server)...)
		name, effort, options, provenance, err := ResolveHarnessSettings(layers, HarnessSettingsProvenance{Source: source, AccountID: NewID(), ConnectionID: NewID(), ProfileDigest: HarnessEvidenceDigest([]byte("fixture profile"))})
		want := "provider-a"
		if provider == providerB {
			want = "provider-b"
		}
		if err != nil || name != want || effort != "" || options.MaxConcurrency != 0 || provenance.Fields["effort"] != agent || provenance.Fields["max_concurrency"] != agent || provenance.Fields["model"].Layer != HarnessServerLayer {
			t.Fatalf("wrong effective source/default: %s %s %+v %+v %v", name, effort, options, provenance, err)
		}
		profile := source
		d.Entries = append(d.Entries, HarnessDefaultEntry{Scope: profile, Settings: HarnessSettings{Version: 1, Model: OverrideHarnessSetting("profile")}})
		layers = HarnessDefaultLayers(d, source, server)
		name, err = ResolveHarnessModel(layers)
		if err != nil || name != "profile" {
			t.Fatal(name, err)
		}
	}
}
func TestHarnessDefaultsRequireOnlyApplicableEvidence(t *testing.T) {
	for _, harness := range []Harness{Codex, ClaudeCode, OpenCode, GrokBuild} {
		settings := HarnessSettings{Version: 1, Model: OverrideHarnessSetting("native")}
		if harness != GrokBuild {
			settings.Effort = OverrideHarnessSetting("")
		}
		if harness == Codex {
			settings = completeHarnessFixture("native")
			settings.ClaudePermission = nil
			settings.ApprovalReviewModel = nil
		}
		if harness == ClaudeCode {
			settings.ClaudePermission = OverrideHarnessSetting(ClaudePermissionDefault)
		}
		origin := HarnessSettingOrigin{Layer: HarnessServerLayer, ID: NewID(), Revision: 1}
		source := HarnessDefaultScope{Harness: harness, Source: HarnessAPISource, ProviderID: NewID(), APIProtocol: OpenAIChat}
		if harness == Codex {
			source.APIProtocol = OpenAIResponses
		}
		if harness == ClaudeCode {
			source.APIProtocol = AnthropicMessages
		}
		model, _, options, p, err := ResolveHarnessSettings([]HarnessSettingsLayer{{Settings: settings, Origin: origin}}, HarnessSettingsProvenance{Source: source, AccountID: NewID(), ConnectionID: NewID(), ProfileDigest: HarnessEvidenceDigest([]byte("fixture"))})
		if err != nil || model != "native" {
			t.Fatal(harness, err)
		}
		if harness != Codex && options.Permission != PermissionDefault {
			t.Fatal("irrelevant permission invented", options)
		}
		if harness == GrokBuild && len(p.Fields) != 1 {
			t.Fatal("Grok acquired irrelevant defaults", p)
		}
		settings.Model = nil
		_, _, _, _, err = ResolveHarnessSettings([]HarnessSettingsLayer{{Settings: settings, Origin: origin}}, HarnessSettingsProvenance{Source: source})
		if SafeError(err).Code != HarnessDefaultUnverified {
			t.Fatal("missing model did not fail closed", err)
		}
	}
}
func TestHarnessSelectorsRejectNullAndPreserveExplicitValues(t *testing.T) {
	for _, raw := range []string{`{"mode":"inherit","value":"x"}`, `{"mode":"override"}`, `{"mode":"override","value":null}`, `{"mode":"future"}`} {
		var selection HarnessSetting[string]
		if Decode([]byte(raw), &selection) == nil && selection.Validate() == nil {
			t.Fatal("invalid selector accepted", raw)
		}
	}
	for _, value := range []string{"", "custom-future-effort"} {
		s := OverrideHarnessSetting(value)
		raw, _ := json.Marshal(s)
		var restored HarnessSetting[string]
		if Decode(raw, &restored) != nil || restored.Validate() != nil || !reflect.DeepEqual(s, &restored) {
			t.Fatal("override lost", string(raw))
		}
	}
}
func TestNewLegacyAgentCreateKeepsSubmittedOverridesAcrossUpgrade(t *testing.T) {
	agent := Agent{Name: "New", Harness: Codex, Routes: []AgentSourceRoute{{Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "owner-model"}, MetadataSource: UserDeclared}, Accounts: []WeightedAccount{{ID: NewID(), Weight: 2}}}}, Options: AgentOptions{Permission: PermissionReadOnly}}
	raw, _ := json.Marshal(agent)
	if err := NormalizeNewAgentHarnessSettings(&agent, raw); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(agent)
	if UpgradeAgentHarnessSettings(&agent) {
		t.Fatal("restart upgraded a current create")
	}
	after, _ := json.Marshal(agent)
	if string(before) != string(after) || agent.HarnessSettings.Effort != nil || agent.HarnessSettings.MaxConcurrency != nil || *agent.HarnessSettings.Permission.Value != PermissionReadOnly || *agent.Routes[0].ModelSelection.Value != "owner-model" {
		t.Fatal("create override/omission semantics changed", agent)
	}
}

func TestHarnessResolutionPreservesButDoesNotApplyOtherHarnessFields(t *testing.T) {
	settings := completeHarnessFixture("claude-model")
	settings.ClaudePermission = OverrideHarnessSetting(ClaudePermissionAcceptEdits)
	before, _ := json.Marshal(settings)
	source := HarnessDefaultScope{Harness: ClaudeCode, Source: HarnessSubscriptionSource, SubscriptionService: SubscriptionClaude}
	origin := HarnessSettingOrigin{Layer: HarnessServerLayer, ID: NewID(), Revision: 1}
	_, _, options, provenance, err := ResolveHarnessSettings([]HarnessSettingsLayer{{Settings: settings, Origin: origin}}, HarnessSettingsProvenance{Source: source})
	if err != nil || options.Permission != PermissionDefault || options.ClaudePermission != ClaudePermissionAcceptEdits || options.MaxConcurrency != 0 || options.ApprovalPolicy != "" || len(provenance.Fields) != 3 {
		t.Fatal("Claude applied unrelated Codex selectors", options, provenance, err)
	}
	after, _ := json.Marshal(settings)
	if string(before) != string(after) {
		t.Fatal("unrelated saved fields were removed")
	}
}
