// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestCodexWebSearchSelectorsKeepAbsenceAndExplicitModes(t *testing.T) {
	for _, mode := range []WebSearchMode{WebSearchDisabled, WebSearchCached, WebSearchLive} {
		s := completeHarnessFixture("native")
		s.WebSearch = OverrideHarnessSetting(mode)
		origin := HarnessSettingOrigin{Layer: HarnessProjectLayer, ID: NewID(), Revision: 3}
		source := HarnessDefaultScope{Harness: Codex, Source: HarnessSubscriptionSource, SubscriptionService: SubscriptionChatGPT}
		_, _, options, provenance, err := ResolveHarnessSettings([]HarnessSettingsLayer{{Settings: s, Origin: origin}}, HarnessSettingsProvenance{Source: source, AccountID: NewID(), ConnectionID: NewID()})
		if err != nil || options.WebSearch != mode || provenance.Fields["web_search"] != origin {
			t.Fatal("mode or provenance changed", err)
		}
		// Editing the configured default cannot mutate accepted execution options.
		s.WebSearch = OverrideHarnessSetting(WebSearchDisabled)
		if options.WebSearch != mode {
			t.Fatal("accepted mode was mutable")
		}
	}
	s := completeHarnessFixture("native")
	if s.WebSearch != nil {
		t.Fatal("legacy omission became a selected default")
	}
	for _, raw := range []string{`{"version":1,"web_search":{"mode":"override","value":"unknown"}}`, `{"version":1,"web_search":{"mode":"override","value":""}}`, `{"version":1,"web_search":{"mode":"inherit","value":"cached"}}`, `{"version":1,"web_search":null}`} {
		var value HarnessSettings
		if Decode([]byte(raw), &value) == nil {
			t.Fatal("invalid selector accepted", raw)
		}
	}
	s.WebSearch = &HarnessSetting[WebSearchMode]{Mode: HarnessInherit}
	if _, _, _, _, err := ResolveHarnessSettings([]HarnessSettingsLayer{{Settings: s, Origin: HarnessSettingOrigin{Layer: HarnessAgentLayer, ID: NewID(), Revision: 1}}}, HarnessSettingsProvenance{Source: HarnessDefaultScope{Harness: Codex, Source: HarnessSubscriptionSource}}); err == nil {
		t.Fatal("unknown inherited mode became cached")
	}
}
func TestCodexWebSearchOpaqueResultsAndClosedActions(t *testing.T) {
	raw := `[{"unknown_source":{"integer":9007199254740993},"url":"https://fixture.invalid","original":"한글"}]`
	observation := CodexWebSearchObservation{Query: "Original", Action: &CodexWebSearchAction{Type: CodexWebSearchActionSearch}, Results: &raw, Mode: WebSearchCached, Source: "openai"}
	snapshot := ToolSnapshot{Kind: WebSearchTool, Status: ToolCompleted, WebSearch: &observation}
	if snapshot.Validate() != nil {
		t.Fatal("original opaque results rejected")
	}
	bytes, _ := json.Marshal(snapshot)
	var restored ToolSnapshot
	if Decode(bytes, &restored) != nil || *restored.WebSearch.Results != raw {
		t.Fatal("original native JSON changed")
	}
	for _, action := range []string{`{"type":"other","query":null}`, `{"type":"open_page","pattern":"x"}`, `{"type":"search","queries":null}`, `{"type":"find_in_page","query":"x"}`, `{"type":"search","extra":1}`} {
		var a CodexWebSearchAction
		if Decode([]byte(action), &a) == nil {
			t.Fatal("malformed action accepted", action)
		}
	}
	observation.Mode = WebSearchDisabled
	if snapshot.Validate() == nil {
		t.Fatal("disabled search accepted an observation")
	}
}

func TestNativeWebSnapshotRequiresOriginalEvidenceEnvelope(t *testing.T) {
	for _, raw := range []string{`{"query":"q","action":null,"mode":"live","source":"openai"}`, `{"query":null,"action":null,"results":null,"mode":"live","source":"openai"}`, `{"query":"q","action":null,"results":null,"mode":"disabled","source":"openai"}`} {
		var value CodexWebSearchObservation
		if Decode([]byte(raw), &value) == nil {
			t.Fatal("missing or malformed evidence accepted", raw)
		}
	}
}
