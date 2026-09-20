package core

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentUninstallPreservesMissingOrUnintegratedSettings(t *testing.T) {
	for _, tc := range []struct{ client, config, initial string }{
		{"codex", ".codex/config.toml", "# unrelated settings\n"},
		{"claude-code", ".mcp.json", "{\"unrelated\":true}\n"},
		{"opencode", "opencode.jsonc", "// unrelated settings\n{\"theme\":\"dark\"}\n"},
		{"opencode", "opencode.json", "{\"theme\":\"dark\"}\n"},
	} {
		for _, state := range []string{"missing-settings", "missing-both", "entry-removed", "empty-settings"} {
			t.Run(tc.config+"/"+state, func(t *testing.T) {
				s, repo := fixture(t, "version=1\n")
				path := filepath.Join(repo, tc.config)
				if err := AtomicWrite(path, []byte(tc.initial), 0600); err != nil {
					t.Fatal(err)
				}
				installed, err := s.Agent(tc.client, "project", repo, false)
				if err != nil {
					t.Fatal(err)
				}
				path = installed.Path
				id := "agent:" + tc.client + ":" + path
				owned, err := s.installation(id)
				if err != nil {
					t.Fatal(err)
				}
				var before os.FileInfo
				expected := tc.initial
				switch state {
				case "missing-settings", "missing-both":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if state == "missing-both" {
						if err := os.Remove(owned.SkillPath); err != nil {
							t.Fatal(err)
						}
					}
				default:
					if state == "empty-settings" {
						expected = ""
					}
					if err := AtomicWrite(path, []byte(expected), 0600); err != nil {
						t.Fatal(err)
					}
					before, err = os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				backupsBefore, err := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					result, err := s.Agent(tc.client, "project", repo, true)
					if err != nil || result.Installed || result.Backup != "" {
						t.Fatal(result, err)
					}
				}
				if before == nil {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("deleted settings recreated", err)
					}
				} else {
					after, err := os.Stat(path)
					if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
						t.Fatal("unchanged settings replaced", err)
					}
					data, err := os.ReadFile(path)
					if err != nil || string(data) != expected {
						t.Fatal("unrelated settings changed", err)
					}
				}
				if tc.client == "opencode" && tc.config == "opencode.json" {
					if _, err := os.Lstat(filepath.Join(repo, "opencode.jsonc")); !os.IsNotExist(err) {
						t.Fatal("fallback cleanup created JSONC settings", err)
					}
				}
				if _, err := os.Lstat(owned.SkillPath); !os.IsNotExist(err) {
					t.Fatal("owned skill not removed", err)
				}
				if _, err := s.installation(id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("stale ownership retained", err)
				}
				backupsAfter, err := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
				if err != nil || len(backupsBefore) != len(backupsAfter) {
					t.Fatal("no-op settings created backup", err)
				}
			})
		}
	}
}
