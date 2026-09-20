package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexEquivalentMCPKeysRejectConflictsBeforeMutation(t *testing.T) {
	for _, tc := range []struct{ name, text, code string }{
		{"bare", "[mcp_servers.async-commit-hook]\ncommand='user'\n", "agent-conflict"},
		{"quoted", "[mcp_servers.\"async-commit-hook\"]\ncommand='user'\n", "agent-conflict"},
		{"literal", "['mcp_servers'.'async-commit-hook']\ncommand='user'\n", "agent-conflict"},
		{"whitespace", "[ mcp_servers . \"async-commit-hook\" ]\ncommand='user'\n", "agent-conflict"},
		{"escaped", "[mcp_servers.\"\\u0061sync-commit-hook\"]\ncommand='user'\n", "agent-conflict"},
		{"dotted", "mcp_servers.\"async-commit-hook\".command='user'\n", "agent-conflict"},
		{"parent", "[mcp_servers]\n'async-commit-hook' = {command='user'}\n", "agent-conflict"},
		{"inline", "mcp_servers = {'async-commit-hook'={command='user'}}\n", "agent-conflict"},
		{"array", "[[mcp_servers.'async-commit-hook']]\ncommand='user'\n", "agent-conflict"},
		{"scalar-parent", "mcp_servers='user'\n", "agent-conflict"},
		{"sealed-parent", "mcp_servers={other={command='user'}}\n", "agent-conflict"},
		{"invalid", "[mcp_servers\n", "agent-config-invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n")
			path := filepath.Join(repo, ".codex", "config.toml")
			if err := AtomicWrite(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := s.Agent("codex", "project", repo, false)
			var typed *Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatal("missing conflict", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, []byte(tc.text)) {
				t.Fatal("changed user configuration", err)
			}
			if _, err = os.Stat(filepath.Join(repo, ".agents", "skills", "async-commit-hook", "SKILL.md")); !os.IsNotExist(err) {
				t.Fatal("created skill before conflict validation", err)
			}
			var count int
			if err = s.Store.DB.QueryRow("SELECT count(*) FROM installations").Scan(&count); err != nil || count != 0 {
				t.Fatal("recorded conflicting ownership", count, err)
			}
			if entries, err := os.ReadDir(filepath.Join(s.Store.Root, "backups")); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
				t.Fatal("created backup before conflict validation", err)
			}
		})
	}
}

func TestCodexTOMLSemanticInspectionPreservesUnrelatedText(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	path := filepath.Join(repo, ".codex", "config.toml")
	text := "# Example: [mcp_servers.async-commit-hook]\nnotes = '[mcp_servers.async-commit-hook]'\n\"mcp_servers.async-commit-hook\" = 'one unrelated key'\n[mcp_servers.\"other\"]\ncommand = 'user' # Keep this comment\n"
	if err := AtomicWrite(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := s.Agent("codex", "project", repo, false)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(result.Path)
		if err != nil || !bytes.HasPrefix(data, []byte(text)) {
			t.Fatal("unrelated settings changed", err)
		}
		var parsed map[string]any
		if err = toml.Unmarshal(data, &parsed); err != nil {
			t.Fatal("installed invalid TOML", err)
		}
		servers := parsed["mcp_servers"].(map[string]any)
		if len(servers) != 2 || servers["async-commit-hook"] == nil || servers["other"] == nil {
			t.Fatal("lost MCP settings", servers)
		}
	}
	if _, err := s.Agent("codex", "project", repo, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, []byte(text)) {
		t.Fatal("removal changed unrelated TOML", err)
	}
}
