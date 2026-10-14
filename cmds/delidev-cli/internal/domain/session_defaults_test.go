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
func TestBranchPrefixInstructionLiteralIsExact(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		quoted string
	}{
		{"html-sensitive", "a<&>", `"a<&>"`},
		{"slash", "a/b", `"a/b"`},
		{"non-ascii", "팀-🐦/", `"팀-🐦/"`},
		{"quote", `a"b`, `"a\"b"`},
		{"tab", "a\tb", `"a\tb"`},
		{"line-separator", "a\u2028b", "\"a\u2028b\""},
		{"paragraph-separator", "a\u2029b", "\"a\u2029b\""},
		{"replacement-character", "a\ufffdb", "\"a\ufffdb\""},
	}
	for _, tc := range cases {
		c := ExecutionConfiguration{Harness: Codex, BranchPrefix: &BranchPrefixSelection{Version: BranchPrefixVersionExactQuote, Prefix: tc.prefix}}
		got, err := c.NativeInstructions(ExecuteMode)
		if err != nil || !strings.Contains(got, "concatenate the literal prefix "+tc.quoted+" with a suffix") {
			t.Fatalf("%s: instruction literal changed: %q %v", tc.name, got, err)
		}
		if strings.Contains(got, `\u00`) || strings.Contains(got, `\u20`) || strings.Contains(got, `\ufffd`) {
			t.Fatalf("%s: unexpected unicode substitution: %q", tc.name, got)
		}
		for _, mode := range []SessionMode{PlanMode} {
			if plan, _ := c.NativeInstructions(mode); plan != "" {
				t.Fatalf("%s: Plan produced an instruction", tc.name)
			}
		}
		c.SidechatPolicy = "read-only"
		if side, _ := c.NativeInstructions(ExecuteMode); side != "" {
			t.Fatalf("%s: read-only Sidechat produced an instruction", tc.name)
		}
	}
	if ValidateBranchPrefix("a<&>") != nil {
		t.Fatal("html-sensitive prefix must stay valid")
	}
	for _, bad := range []string{"a~", "a b", "a\\", "a..", "/a"} {
		if ValidateBranchPrefix(bad) == nil {
			t.Fatalf("invalid prefix %q accepted", bad)
		}
	}
}
func TestBranchPrefixVersionOneRetainsLegacyInstructionBytes(t *testing.T) {
	cases := []struct {
		prefix string
		quoted string
	}{
		{"a<&>", `"a\u003c\u0026\u003e"`},
		{"a\u2028b\u2029", `"a\u2028b\u2029"`},
		{"team/", `"team/"`},
	}
	for _, tc := range cases {
		legacy := ExecutionConfiguration{Harness: Codex, BranchPrefix: &BranchPrefixSelection{Version: BranchPrefixVersionLegacyQuote, Prefix: tc.prefix}}
		got, err := legacy.NativeInstructions(ExecuteMode)
		want := "When creating a new Git branch, concatenate the literal prefix " + tc.quoted + " with a suffix you choose. Do not insert a separator or rename existing branches. Preserve existing branches and workspace permissions."
		if err != nil || got != want {
			t.Fatalf("version 1 instruction bytes changed for %q: %q %v", tc.prefix, got, err)
		}
		if legacy.BranchPrefix.Validate() != nil {
			t.Fatalf("version 1 selection %q must stay valid", tc.prefix)
		}
	}
	if (BranchPrefixSelection{Version: 3, Prefix: "team"}).Validate() == nil || (BranchPrefixSelection{Version: 0, Prefix: "team"}).Validate() == nil {
		t.Fatal("unknown selection versions accepted")
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
