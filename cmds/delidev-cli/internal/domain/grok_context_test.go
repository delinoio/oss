package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGrokContextSnapshotPreservesSourceAndRejectsInventedDefaults(t *testing.T) {
	c := grokSettingsFixture(t)
	legacy, _ := json.Marshal(c)
	if strings.Contains(string(legacy), "grok_context") {
		t.Fatal("legacy configuration digest changed")
	}
	if _, err := c.GrokFirstTextContext(ExecuteMode); err == nil {
		t.Fatal("absent context gained a native default")
	}
	for _, source := range []EvidenceSource{Known, UserDeclared} {
		limit := uint64(48000)
		a := Agent{Name: "Original", Harness: GrokBuild, ModelID: c.ModelID, Options: c.Options}
		m := Model{Name: "Original", NativeID: c.NativeModel, ProviderID: c.ProviderID, Harnesses: []Harness{GrokBuild}, ContextLimit: &limit, MetadataSource: source}
		selected, err := ResolveExecutionConfiguration(c.AgentID, 1, a, 2, m, Priority, nil)
		if err != nil || selected.GrokContext == nil || selected.GrokContext.Source != source {
			t.Fatal("missing original context source", err)
		}
		digest, _ := selected.Digest()
		limit = 64000
		after, _ := selected.Digest()
		if selected.GrokContext.Tokens != 48000 || digest != after {
			t.Fatal("model mutation rewrote immutable context")
		}
		if tokens, err := selected.GrokFirstTextContext(ExecuteMode); err != nil || tokens != 48000 {
			t.Fatal("selected context unavailable", err)
		}
		if _, err := selected.GrokFirstTextContext(PlanMode); err == nil {
			t.Fatal("first text granted Plan execution")
		}
		observed := ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionDefault, GrokMode: GrokDefaultMode, GrokContextTokens: 48000}
		if err := observed.ValidateForInput(selected, ExecuteMode); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []uint64{0, 32000, 64000} {
			observed.GrokContextTokens = invalid
			if observed.ValidateForInput(selected, ExecuteMode) == nil {
				t.Fatal("native context drift was accepted")
			}
		}
		selected.GrokContext.Tokens++
		after, _ = selected.Digest()
		if digest == after {
			t.Fatal("context is not bound to configuration digest")
		}
	}
	for _, context := range []GrokModelContext{{0, Known}, {1023, Known}, {1_000_000_001, Known}, {48000, Unknown}, {48000, "invented"}} {
		c.GrokContext = &context
		if c.Validate() == nil {
			t.Fatal("invalid context provenance accepted", context)
		}
	}
	c.GrokContext = &GrokModelContext{48000, UserDeclared}
	c.Harness = Codex
	if c.Validate() == nil {
		t.Fatal("foreign harness inherited Grok context")
	}
}
