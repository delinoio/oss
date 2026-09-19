package core

import (
	"bytes"
	"context"
	"database/sql"
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
	Example   string `json:"example,omitempty"`
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
		command := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
		raw, err := command.Output()
		e = err
		dir = strings.TrimSpace(string(raw))
		if e != nil {
			return "", e
		}
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Join(dir, kind), nil
}
func (s *Service) Hook(ctx context.Context, repo string, prePush, remove bool) ([]InstallResult, error) {
	lock, err := TryLock(filepath.Join(s.Paths.Control, "installations.lock"))
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, E("installation-busy", "another hook/agent edit is active; retry", 3)
	}
	defer lock.Close()
	_, root, _, e := Discover(ctx, repo)
	if e != nil {
		return nil, e
	}
	kinds := []string{"post-commit"}
	if prePush || remove {
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
		command := quoteSh(executable) + " run --repo . --commit HEAD --automatic --config " + quoteSh(s.Paths.Config)
		if kind == "pre-push" {
			command = quoteSh(executable) + " pre-push --config " + quoteSh(s.Paths.Config)
		}
		if remove {
			if errors.Is(ownedErr, sql.ErrNoRows) {
				continue // Unrelated hooks are outside the uninstall scope.
			}
			if ownedErr != nil {
				return out, ownedErr
			}
			if !os.IsNotExist(readErr) {
				if !ownsHookContents(owned, old) {
					return out, E("hook-conflict", "owned hook changed; preserve it and remove the ach integration manually: "+path, 2)
				}
				if e = os.Remove(path); e != nil {
					return out, e
				}
			}
			_, e = s.Store.DB.Exec("DELETE FROM installations WHERE id=?", id)
			if e != nil {
				return out, e
			}
			out = append(out, InstallResult{Path: path})
			continue
		}
		body := hookBody(executable, s.Paths.Config, kind)
		if readErr == nil {
			if ownedErr == nil && ownsHookContents(owned, old) {
				result, err := s.refreshHook(owned, old, body)
				if err != nil {
					return out, err
				}
				out = append(out, result)
				continue
			}
			out = append(out, InstallResult{Path: path, Manual: command + " (add this to your existing " + kind + " hook or hook manager; preserve stdin for pre-push)", Example: hookExample(kind, command)})
			continue
		}
		if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return out, e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
		if e != nil {
			return out, e
		}
		created, e := f.Stat()
		if e != nil {
			_ = f.Close()
			return out, e
		}
		n, e := f.Write(body)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return out, errors.Join(e, rollbackCreatedHook(path, created, body[:n]))
		}
		if e = s.saveInstallation(Installation{ID: id, Path: path, Hash: Hash(body), Kind: "hook"}); e != nil {
			return out, errors.Join(e, rollbackCreatedHook(path, created, body))
		}
		out = append(out, InstallResult{Installed: true, Path: path})
	}
	return out, nil
}

func rollbackCreatedHook(path string, created os.FileInfo, body []byte) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// A concurrently replaced or edited hook is no longer ours to remove.
	if !current.Mode().IsRegular() || !os.SameFile(created, current) || Hash(contents) != Hash(body) {
		return E("hook-rollback-conflict", "hook changed during failed installation; preserve it and inspect manually: "+path, 3)
	}
	return os.Remove(path)
}

func hookExample(kind, command string) string {
	stdin := ""
	if kind == "pre-push" {
		stdin = "      # If another command consumes stdin, use one wrapper that saves and replays it.\n      use_stdin: true\n"
	}
	return "# Existing shell hook: add this command without deleting other commands.\n" + command + "\n\n# Lefthook configuration: merge this named command into the existing hook.\n" + kind + ":\n  commands:\n    async-commit-hook:\n" + stdin + "      run: |\n        " + command + "\n"
}

func hookBody(executable, config, kind string) []byte {
	args := "run --repo . --commit HEAD --automatic"
	if kind == "pre-push" {
		args = "pre-push \"$@\""
	}
	return []byte("#!/bin/sh\n# ach-owned v1: remove with ach hooks uninstall\nexec " + quoteSh(executable) + " " + args + " --config " + quoteSh(config) + "\n")
}

func ownsHookContents(owned Installation, body []byte) bool {
	// A refresh records both exact versions before publication. If interrupted,
	// the next install/uninstall recognizes either side of that atomic rename.
	return Hash(body) == owned.Hash || len(owned.Original) != 0 && bytes.Equal(body, owned.Original)
}

func (s *Service) refreshHook(owned Installation, old, body []byte) (InstallResult, error) {
	result := InstallResult{Path: owned.Path}
	info, err := os.Lstat(owned.Path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, E("hook-conflict", "owned hook is no longer a regular file: "+owned.Path, 2)
	}
	if !bytes.Equal(old, body) {
		result.Backup = filepath.Join(s.Store.Root, "backups", ID()+"-hook")
		if err = AtomicWrite(result.Backup, old, 0600); err != nil {
			return result, err
		}
		owned.Hash = Hash(body)
		owned.Original = old
		if err = s.saveInstallation(owned); err != nil {
			return result, err
		}
		if err = replaceOwnedHook(owned.Path, info, old, body); err != nil {
			return result, err
		}
	}
	if len(owned.Original) != 0 {
		owned.Hash = Hash(body)
		owned.Original = nil
		if err = s.saveInstallation(owned); err != nil {
			return result, err
		}
	}
	result.Installed = true
	return result, nil
}

func replaceOwnedHook(path string, previous os.FileInfo, old, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ach-hook-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0755); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(previous, current) || !bytes.Equal(old, contents) {
		return E("hook-conflict", "hook changed during refresh; preserve it and inspect manually: "+path, 2)
	}
	return replaceFile(f.Name(), path)
}
