// SPDX-License-Identifier: Apache-2.0
package shim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

const manifestName = ".cowcache-shims.json"
const manifestOwner = "cowcache-native-shims"

type manifest struct {
	Version int               `json:"version"`
	Owner   string            `json:"owner"`
	Hashes  map[Tool][]string `json:"hashes"`
}
type State string

const (
	Missing  State = "missing"
	Owned    State = "owned"
	Conflict State = "conflict"
)

type Entry struct {
	Tool  Tool
	Path  string
	State State
}

func filename(t Tool) string {
	if runtime.GOOS == "windows" {
		return string(t) + ".exe"
	}
	return string(t)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func readManifest(dir string) (manifest, error) {
	m := manifest{Version: 1, Owner: manifestOwner, Hashes: map[Tool][]string{}}
	path := filepath.Join(dir, manifestName)
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if !st.Mode().IsRegular() {
		return m, fmt.Errorf("manifest is not a regular file: %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.Version != 1 || m.Owner != manifestOwner || m.Hashes == nil {
		return m, fmt.Errorf("unknown shim manifest; restore a compatible CowCache version")
	}
	for t, hashes := range m.Hashes {
		if !t.Valid() || len(hashes) == 0 || len(hashes) > 2 {
			return m, fmt.Errorf("invalid shim manifest")
		}
		for _, h := range hashes {
			raw, e := hex.DecodeString(h)
			if e != nil || len(raw) != 32 {
				return m, fmt.Errorf("invalid shim digest")
			}
		}
	}
	return m, nil
}
func entry(dir string, t Tool, m manifest) (Entry, error) {
	e := Entry{Tool: t, Path: filepath.Join(dir, filename(t)), State: Missing}
	st, err := os.Lstat(e.Path)
	if errors.Is(err, os.ErrNotExist) {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	e.State = Conflict
	if !st.Mode().IsRegular() {
		return e, nil
	}
	b, err := os.ReadFile(e.Path)
	if err != nil {
		return e, err
	}
	for _, h := range m.Hashes[t] {
		if digest(b) == h {
			e.State = Owned
			break
		}
	}
	return e, nil
}

// List reports content-verified ownership, never mere filename ownership.
func List(dir string) ([]Entry, error) {
	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	var result []Entry
	for _, t := range Tools() {
		e, err := entry(dir, t, m)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, nil
}

// Install copies the native cowc binary. A journal records both old and new
// digests before replacement, so an interrupted upgrade can be retried safely.
func Install(dir, executable string) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	unlock, err := lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	entries, err := List(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.State == Conflict {
			return fmt.Errorf("shim conflict at %s; move the unrelated file or choose a different shim directory", e.Path)
		}
	}
	b, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return fmt.Errorf("CowCache executable is empty")
	}
	h := digest(b)
	for _, e := range entries {
		hashes := []string{h}
		if e.State == Owned {
			old, err := os.ReadFile(e.Path)
			if err != nil {
				return err
			}
			if digest(old) != h {
				hashes = append(hashes, digest(old))
			}
		}
		m.Hashes[e.Tool] = hashes
	}
	if err := saveManifest(dir, m); err != nil {
		return err
	}
	for _, e := range entries {
		if err := atomicWrite(e.Path, b, 0700); err != nil {
			return err
		}
	}
	for _, t := range Tools() {
		m.Hashes[t] = []string{h}
	}
	if err := saveManifest(dir, m); err != nil {
		return err
	}
	slog.Debug("CowCache shims installed", "directory", dir)
	return nil
}

// Uninstall preserves modified or unrelated files and leaves shell registration
// intact. The CLI should explain that `cowc shell remove` removes registration.
func Uninstall(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := privateDir(dir); err != nil {
		return err
	}
	unlock, err := lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := List(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.State == Conflict {
			return fmt.Errorf("refusing to remove modified or unrelated shim %s; move it manually before retrying", e.Path)
		}
	}
	for _, e := range entries {
		if e.State == Owned {
			if err := os.Remove(e.Path); err != nil {
				return err
			}
		}
	}
	err = os.Remove(filepath.Join(dir, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func saveManifest(dir string, m manifest) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, manifestName), b, 0600)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cowcache-stage-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func lock(dir string) (func(), error) {
	p := filepath.Join(dir, ".cowcache-install.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("shim installation is locked: %w; if no installer is running, remove %s and retry", err, p)
	}
	f.Close()
	return func() { _ = os.Remove(p) }, nil
}
