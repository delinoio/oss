package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Installation struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Hash      string `json:"hash"`
	Original  []byte `json:"original,omitempty"`
	Kind      string `json:"kind"`
	Entry     string `json:"entry,omitempty"`
	SkillPath string `json:"skill_path,omitempty"`
	SkillHash string `json:"skill_hash,omitempty"`
}
type InstallResult struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Manual    string `json:"manual,omitempty"`
	Backup    string `json:"backup,omitempty"`
}

func (s *Service) installation(id string) (Installation, error) {
	var b []byte
	err := s.Store.DB.QueryRow("SELECT record FROM installations WHERE id=?", id).Scan(&b)
	var out Installation
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	return out, err
}
func (s *Service) saveInstallation(i Installation) error {
	_, e := s.Store.DB.Exec("INSERT INTO installations(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record", i.ID, Encode(i))
	return e
}
func quoteSh(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func hookPath(ctx context.Context, root, kind string) (string, error) {
	// Read effective hooksPath without the execution helper's managed-hooks override.
	c := exec.CommandContext(ctx, "git", "-C", root, "config", "--path", "--get", "core.hooksPath")
	b, e := c.Output()
	dir := strings.TrimSpace(string(b))
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) || exit.ExitCode() != 1 {
			return "", E("hooks-discovery-failed", "cannot read effective core.hooksPath", 3)
		}
	}
	if dir == "" {
		dir, e = Git(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
		if e != nil {
			return "", e
		}
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Join(dir, kind), nil
}
func (s *Service) Hook(ctx context.Context, repo string, prePush, remove bool) ([]InstallResult, error) {
	_, root, _, e := Discover(ctx, repo)
	if e != nil {
		return nil, e
	}
	kinds := []string{"post-commit"}
	if prePush {
		kinds = append(kinds, "pre-push")
	}
	out := []InstallResult{}
	executable, e := os.Executable()
	if e != nil {
		return out, e
	}
	for _, kind := range kinds {
		path, e := hookPath(ctx, root, kind)
		if e != nil {
			return out, e
		}
		id := "hook:" + path
		owned, ownedErr := s.installation(id)
		old, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return out, readErr
		}
		command := "ach run --repo . --commit HEAD --automatic"
		if kind == "pre-push" {
			command = "ach pre-push"
		}
		if remove {
			if os.IsNotExist(readErr) {
				continue
			}
			if ownedErr != nil || Hash(old) != owned.Hash {
				return out, E("hook-conflict", "hook changed or is not product-owned; preserve it and remove the ach integration manually: "+path, 2)
			}
			if e = os.Remove(path); e != nil {
				return out, e
			}
			_, e = s.Store.DB.Exec("DELETE FROM installations WHERE id=?", id)
			if e != nil {
				return out, e
			}
			out = append(out, InstallResult{Path: path})
			continue
		}
		if readErr == nil {
			if ownedErr == nil && Hash(old) == owned.Hash {
				out = append(out, InstallResult{Installed: true, Path: path})
				continue
			}
			out = append(out, InstallResult{Path: path, Manual: command + " (add this to your existing " + kind + " hook or hook manager; preserve stdin for pre-push)"})
			continue
		}
		args := "run --repo . --commit HEAD --automatic"
		if kind == "pre-push" {
			args = "pre-push \"$@\""
		}
		body := []byte("#!/bin/sh\n# ach-owned v1: remove with ach hooks uninstall\nexec " + quoteSh(executable) + " " + args + "\n")
		if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return out, e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
		if e != nil {
			return out, e
		}
		_, e = f.Write(body)
		if e == nil {
			e = f.Sync()
		}
		_ = f.Close()
		if e != nil {
			return out, e
		}
		if e = s.saveInstallation(Installation{ID: id, Path: path, Hash: Hash(body), Kind: "hook"}); e != nil {
			return out, e
		}
		out = append(out, InstallResult{Installed: true, Path: path})
	}
	return out, nil
}
