// SPDX-License-Identifier: Apache-2.0
// Package state stores versioned, current-user-private CowCache records.
package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/delinoio/oss/cmds/cowcache/internal/lease"
)

type Store struct{ Root string }
type Settings struct {
	SchemaVersion int    `json:"schemaVersion"`
	Source        string `json:"source,omitempty"`
}

func Open(root string) (*Store, error) {
	if root == "" {
		base, e := os.UserConfigDir()
		if runtime.GOOS == "windows" {
			base, e = os.UserCacheDir()
		}
		if e != nil {
			return nil, e
		}
		root = filepath.Join(base, "cowcache")
	}
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = lease.EnsurePrivateDirectory(root); e != nil {
		return nil, e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	return &Store{root}, nil
}
func Key(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func (s *Store) Directory(name string) (string, error) {
	p := filepath.Join(s.Root, name)
	return p, lease.EnsurePrivateDirectory(p)
}
func (s *Store) settingsPath(worktree string) string {
	return filepath.Join(s.Root, "settings-"+Key(worktree)+".json")
}
func (s *Store) Settings(worktree string) (Settings, error) {
	v := Settings{SchemaVersion: 1}
	e := Read(s.settingsPath(worktree), &v)
	if os.IsNotExist(e) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	if v.SchemaVersion != 1 {
		return v, errors.New("unsupported settings schemaVersion; use a compatible cowc without removing the record")
	}
	return v, nil
}
func (s *Store) SetSource(worktree, source string) error {
	v, e := s.Settings(worktree)
	if e != nil {
		return e
	}
	v.Source = source
	return Write(s.settingsPath(worktree), v)
}
func Read(path string, v any) error {
	if e := lease.ValidatePrivateFile(path); e != nil {
		return e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return errors.New("trailing data in state record")
	}
	return nil
}
func Write(path string, v any) error {
	if _, e := os.Lstat(path); e == nil {
		if e = lease.ValidatePrivateFile(path); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".cowc-state-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncParent(path)
}
