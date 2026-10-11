// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeImportClosedSelectorsAndNativeSettings(t *testing.T) {
	for _, file := range []string{"rules/review.md", "skills/review/SKILL.md", "plugins/review/.claude-plugin/plugin.json"} {
		if !ClaudeConfigurationFile(NativeConfigurationHome, file) || !ClaudeConfigurationFile(NativeConfigurationProject, ".claude/"+file) {
			t.Fatal("explicit file rejected", file)
		}
	}
	for _, file := range []string{"../rules/review.md", "rules/../CLAUDE.md", "rules/auth.md", "skills/review/.env", "plugins/review/memory/state.json", "settings.json", "rules/link\\file.md"} {
		if ClaudeConfigurationFile(NativeConfigurationHome, file) {
			t.Fatal("unsafe selector accepted", file)
		}
	}
	selection := NativeConfigurationPreviewSelection{Version: 1, Harness: ClaudeCode, MachineID: NewID(), MachineRevision: 1, Scopes: []NativeConfigurationScope{{Kind: NativeConfigurationHome, Path: "/explicit/.claude", Files: []string{"rules/review.md"}}}, Target: NativeConfigurationTarget{Kind: NativeConfigurationServerDefaults, ID: NewID(), Revision: 1, Scope: HarnessDefaultScope{Harness: ClaudeCode, Source: HarnessAnySource}}}
	if selection.Validate() != nil {
		t.Fatal("explicit Claude selection rejected")
	}
	selection.Scopes[0].Files = append(selection.Scopes[0].Files, "rules/review.md")
	if selection.Validate() == nil {
		t.Fatal("duplicate source accepted")
	}
	selection.Scopes[0].Files = []string{"rules/review.md"}
	selection.Scopes[0].Profile = "native-selected"
	if selection.Validate() == nil {
		t.Fatal("Claude profile accepted")
	}
	selection.Scopes[0].Profile = ""
	selection.Harness = Codex
	selection.Target.Scope.Harness = Codex
	if selection.Validate() == nil {
		t.Fatal("Claude files changed Codex read authority")
	}
	for _, mode := range []ClaudePermissionMode{ClaudePermissionDefault, ClaudePermissionPlan, ClaudePermissionAcceptEdits, ClaudePermissionDontAsk, ClaudePermissionBypass, ClaudePermissionAuto} {
		settings, err := NativeConfigurationSettingForHarness(ClaudeCode, "claude_permission", []byte(`"`+string(mode)+`"`))
		if err != nil || settings.ClaudePermission == nil || *settings.ClaudePermission.Value != mode || settings.Permission != nil {
			t.Fatal("native mode translated", mode, err)
		}
	}
	for _, field := range []string{"permission", "approval_policy", "service_tier"} {
		if _, err := NativeConfigurationSettingForHarness(ClaudeCode, field, []byte(`"default"`)); err == nil {
			t.Fatal("foreign setting accepted", field)
		}
	}
	if _, err := NativeConfigurationSettingForHarness(ClaudeCode, "effort", []byte(`"ultra"`)); err == nil {
		t.Fatal("Codex effort accepted")
	}
}
func TestClaudeImportPackagesRetainInertContentsAndExcludeCredentials(t *testing.T) {
	text := `{"command":"review-tool","args":["--readonly"]}`
	contents := ClaudeInertConfigurationContents("mcp_servers", text)
	if !NativeConfigurationInertEntry(ClaudeCode, "mcp_servers", contents) || NativeConfigurationInertEntry(Codex, "mcp_servers", contents) {
		t.Fatal("inert proof lost or foreign harness accepted")
	}
	var retained ClaudeInertConfiguration
	if Decode([]byte(contents), &retained) != nil || retained.Contents != text {
		t.Fatal("exact contents lost")
	}
	retained.ExecutionAuthority = true
	changed, _ := json.Marshal(retained)
	if NativeConfigurationInertPackage(ClaudeCode, string(changed)) {
		t.Fatal("authority flag accepted")
	}
	if NativeConfigurationInertPackage(ClaudeCode, strings.Replace(contents, "review-tool", "changed-tool", 1)) {
		t.Fatal("changed package retained original digest")
	}
	for _, unsafe := range []string{`{"env":{"ANY_NAME":"private"}}`, `{"headers":{"Authorization":"private"}}`, `{"url":"https://user:private@example.invalid"}`, `{"apiKey":"private"}`} {
		if NativeConfigurationInertPackage(ClaudeCode, ClaudeInertConfigurationContents("plugins", unsafe)) {
			t.Fatal("credentials retained", unsafe)
		}
	}
	for _, text := range []string{"---\npaths: src/**\n---\nConditional rules.", "Read @../private.md"} {
		proof := NativeImportedPackage{Version: 1, Harness: ClaudeCode, Kind: NativeConfigurationInstruction, SourceDigest: HarnessEvidenceDigest([]byte(text))}
		if proof.Validate(text) == nil {
			t.Fatal("conditional rules became unconditional instructions")
		}
		if !NativeConfigurationInertPackage(ClaudeCode, ClaudeInertConfigurationContents("rules", text)) {
			t.Fatal("inert exact rules rejected")
		}
	}
}
