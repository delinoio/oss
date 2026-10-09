// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBranchPrefixLiteralValidation(t *testing.T) {
	for _, prefix := range []string{"", "delidev/", "team", "team/", "팀/", strings.Repeat("a", 256)} {
		if err := ValidateBranchPrefix(prefix); err != nil {
			t.Fatalf("valid literal rejected: %q: %v", prefix, err)
		}
	}
	for _, prefix := range []string{" ", "team\n", "..", "team//", "/team", "-team", "team@{", "team\\", "team/.hidden/", "team.lock/", strings.Repeat("한", 86), string([]byte{0xff})} {
		if ValidateBranchPrefix(prefix) == nil {
			t.Fatalf("invalid literal accepted: %q", prefix)
		}
	}
}
func TestSessionDefaultsInheritanceAndExplicitEmpty(t *testing.T) {
	settings := Settings{}
	if settings.EffectiveBranchPrefix() != DefaultBranchPrefix {
		t.Fatal("legacy default changed")
	}
	prefix := ""
	project := &Project{Settings: &ProjectBehavior{PlanModeDefault: DisabledBoolean, BranchPrefix: &prefix}}
	if project.EffectiveBranchPrefix("team/") != "" || project.EffectivePlanMode(true) {
		t.Fatal("explicit false/empty lost")
	}
	project.Settings.PlanModeDefault = InheritBoolean
	project.Settings.BranchPrefix = nil
	if project.EffectiveBranchPrefix("team") != "team" || !project.EffectivePlanMode(true) {
		t.Fatal("inheritance changed")
	}
}
func TestBranchPrefixInstructionsRetainLegacyBytesAndExecuteOnly(t *testing.T) {
	original := ExecutionConfiguration{Instructions: " exact template\n", Harness: Codex}
	raw, _ := json.Marshal(original)
	if strings.Contains(string(raw), "branch_prefix") {
		t.Fatal("legacy digest declaration invented")
	}
	for _, harness := range []Harness{Codex, ClaudeCode, OpenCode, GrokBuild} {
		c := original
		c.Harness = harness
		c.BranchPrefix = &BranchPrefixSelection{Version: 1, Prefix: "team"}
		plan, err := c.NativeInstructions(PlanMode)
		if err != nil || plan != original.Instructions {
			t.Fatal("Plan instructions changed", err)
		}
		execute, err := c.NativeInstructions(ExecuteMode)
		if err != nil || !strings.HasPrefix(execute, original.Instructions+"\n\n") || !strings.Contains(execute, `literal prefix "team"`) || strings.Contains(execute, `"team/"`) {
			t.Fatal("literal Execute instruction changed", err)
		}
		c.BranchPrefix.Prefix = ""
		empty, _ := c.NativeInstructions(ExecuteMode)
		if empty != original.Instructions {
			t.Fatal("disabled instruction still applied")
		}
	}
	c := original
	c.Instructions = strings.Repeat("a", MaxAppliedInstructions)
	c.BranchPrefix = &BranchPrefixSelection{Version: 1, Prefix: "team"}
	if _, err := c.NativeInstructions(ExecuteMode); err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatal("aggregate overflow truncated")
	}
	if _, err := c.NativeInstructions(PlanMode); err != nil {
		t.Fatal("Plan overflow invented")
	}
}
func TestBranchPrefixProvenanceRejectsForeignOrMissingRevision(t *testing.T) {
	p := BranchPrefixSelection{Version: 1, Prefix: "team", SettingsID: NewID(), SettingsRevision: 2, ProjectID: NewID(), ProjectRevision: 3}
	if p.Validate() != nil {
		t.Fatal("valid provenance rejected")
	}
	p.ProjectRevision = 0
	if p.Validate() == nil {
		t.Fatal("missing original revision accepted")
	}
}

func TestBranchPrefixJSONRejectsNullAndLossyUnicode(t *testing.T) {
	for _, raw := range []string{`{"branch_prefix":null}`, `{"branch_prefix":"\ud800"}`, `{"branch_prefix":"\udc00"}`, `{"plan_mode_default":null}`} {
		var s Settings
		if Decode([]byte(raw), &s) == nil {
			t.Fatal("invalid field accepted", raw)
		}
	}
	var s Settings
	if Decode([]byte(`{"branch_prefix":"\ud83d\udc26/"}`), &s) != nil || s.BranchPrefix == nil || *s.BranchPrefix != "🐦/" {
		t.Fatal("paired scalar changed")
	}
}
