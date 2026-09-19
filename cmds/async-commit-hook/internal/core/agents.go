package core

import (
	"bytes"
	"database/sql"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

//go:embed agent-guide.md
var AgentGuide string

func (s *Service) Agent(client, scope, repo string, remove bool) (InstallResult, error) {
	lock, err := TryLock(filepath.Join(s.Paths.Control, "installations.lock"))
	if err != nil {
		return InstallResult{}, err
	}
	if lock == nil {
		return InstallResult{}, E("installation-busy", "another integration edit is active; retry", 3)
	}
	defer lock.Close()
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "project" {
		return InstallResult{}, E("invalid-scope", "agent scope must be user or project", 2)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return InstallResult{}, e
	}
	base := home
	if scope == "project" {
		base, e = filepath.Abs(repo)
		if e != nil {
			return InstallResult{}, e
		}
	}
	var path, skill, parent string
	switch client {
	case "codex":
		path = filepath.Join(base, ".codex", "config.toml")
		if scope == "user" && os.Getenv("CODEX_HOME") != "" {
			path = filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
		}
		skill = filepath.Join(base, ".agents", "skills", "async-commit-hook", "SKILL.md")
	case "claude-code":
		path = filepath.Join(base, ".mcp.json")
		if scope == "user" {
			path = filepath.Join(home, ".claude.json")
		}
		skill = filepath.Join(base, ".claude", "skills", "async-commit-hook", "SKILL.md")
		if scope == "user" && os.Getenv("CLAUDE_CONFIG_DIR") != "" {
			dir := os.Getenv("CLAUDE_CONFIG_DIR")
			path = filepath.Join(dir, ".claude.json")
			skill = filepath.Join(dir, "skills", "async-commit-hook", "SKILL.md")
		}
		parent = "mcpServers"
	case "opencode":
		path = filepath.Join(base, "opencode.jsonc")
		skill = filepath.Join(base, ".opencode", "skills", "async-commit-hook", "SKILL.md")
		if scope == "user" {
			configHome := os.Getenv("XDG_CONFIG_HOME")
			if configHome == "" {
				configHome = filepath.Join(home, ".config")
			}
			path = filepath.Join(configHome, "opencode", "opencode.jsonc")
			skill = filepath.Join(configHome, "opencode", "skills", "async-commit-hook", "SKILL.md")
		}
		if _, e = os.Stat(path); os.IsNotExist(e) {
			alternative := strings.TrimSuffix(path, "c")
			if _, e = os.Stat(alternative); e == nil {
				path = alternative
			}
		}
		parent = "mcp"
	default:
		return InstallResult{}, E("invalid-agent", "choose codex, claude-code or opencode", 2)
	}
	id := "agent:" + client + ":" + path
	owned, ownedErr := s.installation(id)
	old, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return InstallResult{}, e
	}
	skillOld, skillErr := os.ReadFile(skill)
	if skillErr != nil && !os.IsNotExist(skillErr) {
		return InstallResult{}, skillErr
	}
	if remove && ownedErr != nil {
		if !errors.Is(ownedErr, sql.ErrNoRows) {
			return InstallResult{}, ownedErr
		}
		if os.IsNotExist(skillErr) && !bytes.Contains(old, []byte("async-commit-hook")) {
			return InstallResult{Path: path}, nil
		}
		return InstallResult{}, E("agent-conflict", "integration is not recorded as product-owned; preserve the existing configuration", 2)
	}
	if skillErr == nil && (ownedErr != nil || Hash(skillOld) != owned.SkillHash) {
		return InstallResult{}, E("agent-conflict", "skill already exists or was edited; move it aside explicitly before changing integration", 2)
	}
	executable, e := os.Executable()
	if e != nil {
		return InstallResult{}, e
	}
	var updated []byte
	var entry string
	if client == "codex" {
		start := "# BEGIN ach-owned MCP v1\n"
		end := "# END ach-owned MCP v1\n"
		text := string(old)
		i := strings.Index(text, start)
		if i >= 0 {
			j := strings.Index(text[i:], end)
			if j < 0 || ownedErr != nil {
				return InstallResult{}, E("agent-conflict", "invalid or unowned Codex MCP block", 2)
			}
			j += i + len(end)
			if text[i:j] != owned.Entry {
				return InstallResult{}, E("agent-conflict", "Codex ach MCP entry was modified", 2)
			}
			text = text[:i] + text[j:]
		} else if strings.Contains(text, "mcp_servers.async-commit-hook") {
			return InstallResult{}, E("agent-conflict", "Codex already has an unowned async-commit-hook MCP entry", 2)
		}
		if !remove {
			entry = start + "[mcp_servers.async-commit-hook]\ncommand = " + string(Encode(executable)) + "\nargs = " + string(Encode([]string{"mcp", "--config", s.Paths.Config})) + "\n" + end
			updated = []byte(strings.TrimRight(text, "\n") + "\n" + entry)
		} else {
			updated = []byte(text)
		}
	} else {
		if len(old) == 0 {
			old = []byte("{}\n")
		}
		value, err := hujson.Parse(old)
		if err != nil {
			return InstallResult{}, E("agent-config-invalid", "agent JSON/JSONC is invalid; no changes made", 2)
		}
		pointer := "/" + parent + "/async-commit-hook"
		existing := value.Find(pointer)
		if existing != nil {
			copy := existing.Clone()
			copy.Standardize()
			copy.Minimize()
			if ownedErr != nil || string(copy.Pack()) != owned.Entry {
				return InstallResult{}, E("agent-conflict", "agent MCP entry was modified or is not product-owned", 2)
			}
		}
		patch := []map[string]any{}
		if existing != nil {
			patch = append(patch, map[string]any{"op": "remove", "path": pointer})
		}
		if !remove {
			if value.Find("/"+parent) == nil {
				patch = append(patch, map[string]any{"op": "add", "path": "/" + parent, "value": map[string]any{}})
			}
			m := map[string]any{"command": executable, "args": []string{"mcp", "--config", s.Paths.Config}, "type": "stdio"}
			if client == "opencode" {
				m = map[string]any{"type": "local", "command": []string{executable, "mcp", "--config", s.Paths.Config}, "enabled": true}
			}
			entry = string(Encode(m))
			patch = append(patch, map[string]any{"op": "add", "path": pointer, "value": m})
		}
		if err = value.Patch(Encode(patch)); err != nil {
			return InstallResult{}, E("agent-config-invalid", "cannot safely merge MCP configuration", 2)
		}
		updated = value.Pack()
	}
	backup := filepath.Join(s.Store.Root, "backups", ID()+"-agent-config")
	if e = AtomicWrite(backup, old, 0600); e != nil {
		return InstallResult{}, e
	}
	if !remove {
		// Record intended ownership before publication so a crash between the
		// settings and skill writes can be completed by a repeated install.
		if e = s.saveInstallation(Installation{ID: id, Path: path, Hash: Hash(updated), Kind: "agent", Entry: entry, SkillPath: skill, SkillHash: Hash([]byte(AgentGuide))}); e != nil {
			return InstallResult{}, e
		}
		if e = AtomicWrite(skill, []byte(AgentGuide), 0600); e != nil {
			return InstallResult{}, e
		}
	}
	if e = AtomicWrite(path, updated, 0600); e != nil {
		if !remove && os.IsNotExist(skillErr) {
			_ = os.Remove(skill)
		}
		return InstallResult{}, e
	}
	if remove {
		if e = os.Remove(skill); e != nil && !os.IsNotExist(e) {
			return InstallResult{}, e
		}
		_, e = s.Store.DB.Exec("DELETE FROM installations WHERE id=?", id)
	} else {
		e = s.saveInstallation(Installation{ID: id, Path: path, Hash: Hash(updated), Kind: "agent", Entry: entry, SkillPath: skill, SkillHash: Hash([]byte(AgentGuide))})
	}
	return InstallResult{Installed: !remove, Path: path, Backup: backup}, e
}
