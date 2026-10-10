// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFixtureFile(t *testing.T, root, name, contents string) {
	t.Helper()
	p := filepath.Join(root, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestClaudeConfigurationOriginalSourcesAndSecretExclusion(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	nativeFixtureFile(t, user, ".claude/settings.json", `{"model":"user-model","apiKey":"sk-ant-private","unknown":{"value":"private"}}`)
	nativeFixtureFile(t, user, ".claude.json", `{"oauthToken":"never-read"}`)
	nativeFixtureFile(t, project, ".claude/settings.json", `{"model":"project-model","hooks":{"PreToolUse":[{"command":"echo fixture"}]}}`)
	nativeFixtureFile(t, project, ".claude/settings.local.json", `{"effortLevel":"high"}`)
	nativeFixtureFile(t, project, ".claude/rules/safety.md", "---\npaths: [\"src/**\"]\n---\nKeep exact rule.\n")
	nativeFixtureFile(t, project, "CLAUDE.local.md", "Local instructions.\n")
	nativeFixtureFile(t, project, ".claude/skills/example/SKILL.md", "# Exact skill\n")
	nativeFixtureFile(t, project, ".claude/plugins/example/plugin.json", `{"name":"fixture","version":"1","author":{"name":"Fixture author"}}`)
	nativeFixtureFile(t, project, ".mcp.json", `{"mcpServers":{"safe":{"command":"fixture"},"private":{"headers":{"Authorization":"secret"}}}}`)
	snapshot, e := readClaudeConfigurationSources(context.Background(), user, project)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(snapshot)
	for _, secret := range []string{"sk-ant-private", "never-read", "\"value\":\"private\"", "Authorization", "\"secret\""} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret escaped: %s", secret)
		}
	}
	seen := map[string]bool{}
	for _, entry := range snapshot.Entries {
		seen[string(entry.Source)+":"+entry.Name] = true
		if entry.Activation != domain.NativeConfigurationDisabled {
			t.Fatal("implicit activation")
		}
		if entry.Kind == domain.NativeConfigurationPlugin && !entry.Supported {
			t.Fatal("ordinary plugin author mistaken for credentials")
		}
		if entry.Name == "safety.md" && entry.Files[0].Contents != "---\npaths: [\"src/**\"]\n---\nKeep exact rule.\n" {
			t.Fatal("rule lost exact contents")
		}
	}
	for _, key := range []string{"user:model", "project:model", "project-local:effortLevel", "project-local:CLAUDE.local.md", "project:safety.md", "project:example", "project:safe", "project:private"} {
		if !seen[key] {
			t.Fatal("missing source", key)
		}
	}
	before, e := os.ReadFile(filepath.Join(project, ".claude/settings.json"))
	if e != nil || !strings.Contains(string(before), "project-model") {
		t.Fatal("source altered")
	}
	nativeFixtureFile(t, project, ".claude/settings.json", `{"model":"edited"}`)
	fresh, e := readClaudeConfigurationSources(context.Background(), user, project)
	if e != nil || fresh.Digest == snapshot.Digest {
		t.Fatal("source drift invisible", e)
	}
}
func TestClaudeConfigurationRejectsSymlinkAndBounds(t *testing.T) {
	root := t.TempDir()
	nativeFixtureFile(t, root, ".claude/settings.json", `{}`)
	outside := t.TempDir()
	nativeFixtureFile(t, outside, "rule.md", "private")
	if e := os.MkdirAll(filepath.Join(root, ".claude/rules"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(outside, "rule.md"), filepath.Join(root, ".claude/rules/escape.md")); e != nil {
		t.Fatal(e)
	}
	if _, e := readClaudeConfigurationSources(context.Background(), "", root); e == nil {
		t.Fatal("followed native symlink")
	}
	os.Remove(filepath.Join(root, ".claude/rules/escape.md"))
	nativeFixtureFile(t, root, ".claude/settings.json", strings.Repeat(" ", 65<<10))
	if _, e := readClaudeConfigurationSources(context.Background(), "", root); e == nil {
		t.Fatal("unbounded native read")
	}
}
