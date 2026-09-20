package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentSymlinkConfigurationsRemainManagedByUser(t *testing.T) {
	for _, tc := range []struct{ client, config, skill, initial string }{
		{"codex", ".codex/config.toml", ".agents/skills/async-commit-hook/SKILL.md", "# managed settings\n"},
		{"claude-code", ".mcp.json", ".claude/skills/async-commit-hook/SKILL.md", "{}\n"},
		{"opencode", "opencode.jsonc", ".opencode/skills/async-commit-hook/SKILL.md", "{}\n"},
		{"opencode", "opencode.json", ".opencode/skills/async-commit-hook/SKILL.md", "{}\n"},
	} {
		t.Run(tc.config, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n")
			path, skill := filepath.Join(repo, tc.config), filepath.Join(repo, tc.skill)
			target := filepath.Join(t.TempDir(), "managed-config")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			reject := func(remove bool, link string) {
				t.Helper()
				before, err := os.Readlink(link)
				if err != nil {
					t.Fatal(err)
				}
				var recordsBefore, recordsAfter int
				if err := s.Store.DB.QueryRow("SELECT count(*) FROM installations").Scan(&recordsBefore); err != nil {
					t.Fatal(err)
				}
				backupsBefore, _ := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
				_, err = s.Agent(tc.client, "project", repo, remove)
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != "agent-conflict" || typed.Exit != 2 {
					t.Fatal("symlink did not cause a conflict", err)
				}
				after, err := os.Readlink(link)
				if err != nil || after != before {
					t.Fatal("replaced user's symlink", err)
				}
				if err := s.Store.DB.QueryRow("SELECT count(*) FROM installations").Scan(&recordsAfter); err != nil || recordsBefore != recordsAfter {
					t.Fatal("changed ownership on conflict", err)
				}
				backupsAfter, _ := os.ReadDir(filepath.Join(s.Store.Root, "backups"))
				if len(backupsAfter) != len(backupsBefore) {
					t.Fatal("created backup on conflict")
				}
			}
			// A dangling config must not be mistaken for an absent regular file.
			reject(false, path)
			if err := os.WriteFile(target, []byte(tc.initial), 0600); err != nil {
				t.Fatal(err)
			}
			reject(false, path)
			if data, err := os.ReadFile(target); err != nil || string(data) != tc.initial {
				t.Fatal("edited managed target", err)
			}
			if _, err := os.Lstat(skill); !os.IsNotExist(err) {
				t.Fatal("published skill despite conflict", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := AtomicWrite(path, []byte(tc.initial), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Agent(tc.client, "project", repo, false); err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, installed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			reject(false, path)
			reject(true, path)
			if data, err := os.ReadFile(target); err != nil || !bytes.Equal(data, installed) {
				t.Fatal("edited owned managed target", err)
			}
			if data, err := os.ReadFile(skill); err != nil || string(data) != AgentGuide {
				t.Fatal("changed skill during config conflict", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := AtomicWrite(path, installed, 0600); err != nil {
				t.Fatal(err)
			}
			skillTarget := filepath.Join(t.TempDir(), "managed-skill")
			if err := os.WriteFile(skillTarget, []byte(AgentGuide), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(skill); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(skillTarget, skill); err != nil {
				t.Fatal(err)
			}
			reject(false, skill)
			reject(true, skill)
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, installed) {
				t.Fatal("changed config during skill conflict", err)
			}
		})
	}
}

func TestOpenCodeDanglingJSONCSymlinkDoesNotSelectJSONFallback(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	path := filepath.Join(repo, "opencode.jsonc")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fallback := filepath.Join(repo, "opencode.json")
	if err := os.WriteFile(fallback, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Agent("opencode", "project", repo, false)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "agent-conflict" {
		t.Fatal("bypassed JSONC symlink", err)
	}
	if data, err := os.ReadFile(fallback); err != nil || string(data) != "{}\n" {
		t.Fatal("changed fallback", err)
	}
}

func TestAgentPreservesSymlinkedConfigurationDirectory(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	target := t.TempDir()
	directory := filepath.Join(repo, ".codex")
	if err := os.Symlink(target, directory); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(target, "config.toml")
	original := "# dotfile directory\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for _, remove := range []bool{false, true} {
		if _, err := s.Agent("codex", "project", repo, remove); err != nil {
			t.Fatal(err)
		}
		if link, err := os.Readlink(directory); err != nil || link != target {
			t.Fatal("changed parent directory link", err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != original {
		t.Fatal("did not restore target configuration", err)
	}
}
