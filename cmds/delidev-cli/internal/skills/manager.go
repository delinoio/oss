// SPDX-License-Identifier: Apache-2.0
// Package skills owns bounded package observations and immutable selected copies.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

const MaxPackageBytes = 16 << 20
const MaxTotalBytes = 64 << 20
const MaxFiles = 1024
const MaxInventory = 256

type Manager struct{ Root, Home string }
type packageRecord struct {
	Entry domain.SkillEntry `json:"entry"`
	Path  string            `json:"path"`
}
type inventory struct {
	Scope    domain.SkillReadRequest `json:"scope"`
	Packages []packageRecord         `json:"packages"`
}
type snapshot struct {
	Scope   domain.SkillReadRequest `json:"scope"`
	Entries []domain.SkillEntry     `json:"entries"`
}

func unavailable() error {
	return domain.Fail(domain.Unavailable, "The selected skill package is unavailable.", "Refresh the skill inventory and select an enabled readable package.")
}
func bound() error {
	return domain.Fail(domain.ResourceExhausted, "The skill package limit is exceeded.", "Use smaller packages or fewer skills.")
}
func boundedDirectory(path string, limit int) ([]os.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(limit + 1)
	if len(entries) > limit {
		return nil, bound()
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}
func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, bound()
	}
	return b, nil
}

// os.Root confines every opened resource even if a link changes during the read.
type packageFile struct {
	Bytes      []byte
	Executable bool
}

func packageFiles(ctx context.Context, path string) (map[string]packageFile, string, error) {
	r, e := os.OpenRoot(path)
	if e != nil {
		return nil, "", unavailable()
	}
	defer r.Close()
	files := map[string]packageFile{}
	total := 0
	visited := 0
	var walk func(string) error
	walk = func(dir string) error {
		f, err := r.Open(dir)
		if err != nil {
			return unavailable()
		}
		entries, err := f.ReadDir(MaxFiles + 1)
		f.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return unavailable()
		}
		if len(entries) > MaxFiles {
			return bound()
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			visited++
			if visited > MaxFiles {
				return bound()
			}
			name := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if err := walk(name); err != nil {
					return err
				}
				continue
			}
			resource, err := r.OpenFile(name, os.O_RDONLY|resourceOpenFlags(), 0)
			if err != nil {
				return unavailable()
			}
			info, err := resource.Stat()
			if err != nil || !info.Mode().IsRegular() {
				resource.Close()
				return unavailable()
			}
			data, err := io.ReadAll(io.LimitReader(resource, MaxPackageBytes+1))
			resource.Close()
			if err != nil {
				return unavailable()
			}
			total += len(data)
			if total > MaxPackageBytes {
				return bound()
			}
			files[name] = packageFile{Bytes: data, Executable: info.Mode().Perm()&0111 != 0}
		}
		return nil
	}
	if e = walk("."); e != nil {
		return nil, "", e
	}

	if len(files["SKILL.md"].Bytes) == 0 || len(files["SKILL.md"].Bytes) > 1<<20 {
		return nil, "", unavailable()
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		b, _ := json.Marshal([]any{k, hex.EncodeToString(hash(files[k].Bytes)), files[k].Executable})
		h.Write(b)
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}
func hash(b []byte) []byte { h := sha256.Sum256(b); return h[:] }
func metadata(b []byte) (string, string, error) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", "", unavailable()
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return "", "", unavailable()
	}
	var value struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(s[4:4+end]), &value) != nil || domain.Text(value.Name, "skill name", 128, true) != nil || strings.ContainsAny(value.Name, " \t\r\n$") || domain.Text(value.Description, "skill description", 4096, true) != nil {
		return "", "", unavailable()
	}
	return value.Name, value.Description, nil
}
func writeJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return security.WriteAtomicOwned(path, b)
}
func (m Manager) disabled() (map[string]bool, error) {
	result := map[string]bool{}
	b, e := readBounded(filepath.Join(m.Home, ".codex", "config.toml"), 1<<20)
	if errors.Is(e, os.ErrNotExist) {
		return result, nil
	}
	if e != nil {
		return nil, unavailable()
	}
	// Parse only skill-specific enable flags. No general configuration is imported.
	var v struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled *bool  `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if toml.Unmarshal(b, &v) != nil {
		return nil, unavailable()
	}
	if len(v.Skills.Config) > MaxInventory {
		return nil, bound()
	}
	for _, c := range v.Skills.Config {
		if c.Enabled == nil {
			return nil, unavailable()
		}
		p, e := filepath.EvalSymlinks(c.Path)
		if e != nil {
			return nil, unavailable()
		}
		if !*c.Enabled {
			result[filepath.Clean(p)] = true
			result[filepath.Dir(p)] = true
		}
	}
	return result, nil
}
func (m Manager) List(ctx context.Context, scope domain.SkillReadRequest, projectRoots []string) (domain.SkillReadResult, error) {
	result := domain.SkillReadResult{Entries: []domain.SkillEntry{}}
	disabled, e := m.disabled()
	if e != nil {
		return result, e
	}
	inv := inventory{Scope: scope}
	id := domain.NewID()
	seen := map[string]bool{}
	roots := []struct{ path, provenance string }{{filepath.Join(m.Home, ".agents", "skills"), "user"}, {filepath.Join(m.Home, ".codex", "skills"), "user"}}
	for _, p := range projectRoots {
		roots = append(roots, struct{ path, provenance string }{filepath.Join(p, ".agents", "skills"), "project"}, struct{ path, provenance string }{filepath.Join(p, ".codex", "skills"), "project"})
	}
	for _, root := range roots {
		entries, e := boundedDirectory(root.path, MaxInventory)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return result, unavailable()
		}
		if len(entries) > MaxInventory {
			return result, bound()
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			p, e := filepath.EvalSymlinks(filepath.Join(root.path, entry.Name()))
			if e != nil {
				return result, unavailable()
			}
			if seen[p] || disabled[p] {
				continue
			}
			seen[p] = true
			files, digest, e := packageFiles(ctx, p)
			if e != nil {
				return result, e
			}
			name, desc, e := metadata(files["SKILL.md"].Bytes)
			if e != nil {
				return result, e
			}
			if len(inv.Packages) >= MaxInventory {
				return result, bound()
			}
			item := domain.SkillEntry{WorkerDeviceID: scope.WorkerDeviceID, InventoryID: id, SkillID: domain.NewID(), ContentRevision: digest, Name: name, Description: desc, Provenance: root.provenance}
			inv.Packages = append(inv.Packages, packageRecord{Entry: item, Path: p})
			result.Entries = append(result.Entries, item)
		}
	}
	raw, _ := json.Marshal(result)
	if len(raw) > 256<<10 {
		return result, bound()
	}
	if e = m.inventoryRoom(); e != nil {
		return result, e
	}
	raw, _ = json.Marshal(inv)
	if len(raw) > 256<<10 {
		return result, bound()
	}
	if e = writeJSON(filepath.Join(m.Root, "skill-inventories", string(id)+".json"), inv); e != nil {
		return result, unavailable()
	}
	return result, nil
}
func sameScope(a, b domain.SkillReadRequest) bool {
	return a.ProjectID == b.ProjectID && a.WorkerInstanceID == b.WorkerInstanceID && a.WorkerDeviceID == b.WorkerDeviceID && a.AgentRevision == b.AgentRevision && a.ActorID == b.ActorID && a.MachineID == b.MachineID && a.AgentID == b.AgentID && a.SessionID == b.SessionID
}
func snapshotPath(root string, id domain.ID) string {
	return filepath.Join(root, "skill-snapshots", string(id))
}
func (m Manager) Prepare(ctx context.Context, scope domain.SkillReadRequest) error {
	if len(scope.Selections) == 0 || domain.ValidateSkills(scope.Selections) != nil {
		return unavailable()
	}
	id := scope.Selections[0].SnapshotID
	target := snapshotPath(m.Root, id)
	if b, e := readBounded(filepath.Join(target, "snapshot.json"), 256<<10); e == nil {
		var saved snapshot
		if domain.Decode(b, &saved) != nil || !sameScope(saved.Scope, scope) {
			return unavailable()
		}
		a, _ := json.Marshal(saved.Scope.Selections)
		b, _ := json.Marshal(scope.Selections)
		if string(a) != string(b) {
			return unavailable()
		}
		_, e = m.Resolve(ctx, scope.Selections)
		return e
	} else if !errors.Is(e, os.ErrNotExist) {
		return unavailable()
	}
	if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return unavailable()
	}
	tmp, e := os.MkdirTemp(filepath.Dir(target), ".pending-")
	if e != nil {
		return unavailable()
	}
	defer os.RemoveAll(tmp)
	saved := snapshot{Scope: scope}
	total := 0
	for _, selection := range scope.Selections {
		if selection.SnapshotID != id {
			return unavailable()
		}
		b, e := readBounded(filepath.Join(m.Root, "skill-inventories", string(selection.InventoryID)+".json"), 256<<10)
		if e != nil {
			return unavailable()
		}
		var inv inventory
		if domain.Decode(b, &inv) != nil || !sameScope(inv.Scope, scope) {
			return unavailable()
		}
		var p *packageRecord
		for i := range inv.Packages {
			if inv.Packages[i].Entry.SkillID == selection.SkillID {
				p = &inv.Packages[i]
			}
		}
		if p == nil || p.Entry.ContentRevision != selection.ContentRevision {
			return unavailable()
		}
		disabled, e := m.disabled()
		if e != nil || disabled[p.Path] {
			return unavailable()
		}
		files, digest, e := packageFiles(ctx, p.Path)
		if e != nil {
			return e
		}
		if digest != selection.ContentRevision {
			return unavailable()
		}
		for name, b := range files {
			total += len(b.Bytes)
			if total > MaxTotalBytes {
				return bound()
			}
			path := filepath.Join(tmp, string(selection.SkillID), name)
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return unavailable()
			}
			if e = security.WriteAtomicOwnedMode(path, b.Bytes, b.Executable); e != nil {
				return unavailable()
			}
		}
		saved.Entries = append(saved.Entries, p.Entry)
	}
	if e = writeJSON(filepath.Join(tmp, "snapshot.json"), saved); e != nil {
		return unavailable()
	}
	if e = os.Rename(tmp, target); e != nil {
		return unavailable()
	}
	return security.SyncParent(target)
}

type NativeSelection struct{ Name, Path string }

func (m Manager) Resolve(ctx context.Context, bindings []domain.SkillBinding) ([]NativeSelection, error) {
	if domain.ValidateSkills(bindings) != nil {
		return nil, unavailable()
	}
	out := []NativeSelection{}
	for _, s := range bindings {
		b, e := readBounded(filepath.Join(snapshotPath(m.Root, s.SnapshotID), "snapshot.json"), 256<<10)
		if e != nil {
			return nil, unavailable()
		}
		var saved snapshot
		if domain.Decode(b, &saved) != nil {
			return nil, unavailable()
		}
		var entry *domain.SkillEntry
		for i := range saved.Entries {
			v := &saved.Entries[i]
			if v.SkillID == s.SkillID && v.InventoryID == s.InventoryID && v.ContentRevision == s.ContentRevision {
				entry = v
			}
		}
		if entry == nil {
			return nil, unavailable()
		}
		path := filepath.Join(snapshotPath(m.Root, s.SnapshotID), string(s.SkillID))
		_, digest, e := packageFiles(ctx, path)
		if e != nil || digest != s.ContentRevision {
			return nil, unavailable()
		}
		out = append(out, NativeSelection{Name: entry.Name, Path: filepath.Join(path, "SKILL.md")})
	}
	return out, nil
}

// CopyToRuntime verifies the retained digest before and after independent copying.
func (m Manager) CopyToRuntime(ctx context.Context, bindings []domain.SkillBinding, runtime string) ([]NativeSelection, error) {
	entries, e := m.Resolve(ctx, bindings)
	if e != nil {
		return nil, e
	}
	out := []NativeSelection{}
	for i, s := range bindings {
		files, digest, e := packageFiles(ctx, filepath.Dir(entries[i].Path))
		if e != nil || digest != s.ContentRevision {
			return nil, unavailable()
		}
		target := filepath.Join(runtime, "selected-skills", string(s.SkillID))
		if e = os.MkdirAll(target, 0700); e != nil {
			return nil, e
		}
		for name, b := range files {
			path := filepath.Join(target, name)
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return nil, e
			}
			if e = security.WriteAtomicOwnedMode(path, b.Bytes, b.Executable); e != nil {
				return nil, e
			}
		}
		_, after, e := packageFiles(ctx, target)
		if e != nil || after != digest {
			return nil, unavailable()
		}
		if e = writeJSON(filepath.Join(runtime, "selected-skill-proofs", string(s.SkillID)+".json"), s); e != nil {
			return nil, unavailable()
		}
		out = append(out, NativeSelection{Name: entries[i].Name, Path: filepath.Join(target, "SKILL.md")})
	}
	return out, nil
}

// CheckContext rejects a snapshot from another original accepted execution scope.
func (m Manager) CheckContext(bindings []domain.SkillBinding, input domain.ExecutionJobInput) error {
	for _, binding := range bindings {
		b, e := readBounded(filepath.Join(snapshotPath(m.Root, binding.SnapshotID), "snapshot.json"), 256<<10)
		if e != nil {
			return unavailable()
		}
		var saved snapshot
		if domain.Decode(b, &saved) != nil || saved.Scope.WorkerDeviceID != binding.WorkerDeviceID || saved.Scope.MachineID != input.MachineID || saved.Scope.AgentID != input.Configuration.AgentID || saved.Scope.AgentRevision != input.Configuration.AgentRevision || (saved.Scope.SessionID != "" && saved.Scope.SessionID != input.SessionID) {
			return unavailable()
		}
	}
	return nil
}

// Expiry affects read observations only. Accepted package snapshots never expire.
func (m Manager) inventoryRoom() error {
	path := filepath.Join(m.Root, "skill-inventories")
	if e := security.PrivateDir(path); e != nil {
		return unavailable()
	}
	entries, e := boundedDirectory(path, MaxInventory)
	if e != nil {
		return unavailable()
	}
	if len(entries) > MaxInventory {
		return bound()
	}
	count := 0
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return unavailable()
		}
		if time.Since(info.ModTime()) > 15*time.Minute && !entry.IsDir() {
			if e = os.Remove(filepath.Join(path, entry.Name())); e != nil {
				return unavailable()
			}
		} else {
			count++
		}
	}
	if count >= MaxInventory {
		return bound()
	}
	return nil
}

// CloneRuntimePackage copies only a digest-proved package from an original private
// runtime. It does not consult current inventory or inherit the source lifetime.
func CloneRuntimePackage(ctx context.Context, sourceHome, sourcePath, childHome string) (string, int, error) {
	rel, err := filepath.Rel(filepath.Join(sourceHome, "selected-skills"), sourcePath)
	parts := strings.Split(rel, string(filepath.Separator))
	if err != nil || len(parts) != 2 || parts[1] != "SKILL.md" || domain.ID(parts[0]).Validate() != nil {
		return "", 0, unavailable()
	}
	b, err := readBounded(filepath.Join(sourceHome, "selected-skill-proofs", parts[0]+".json"), 4096)
	var binding domain.SkillBinding
	if err != nil || domain.Decode(b, &binding) != nil || domain.ValidateSkills([]domain.SkillBinding{binding}) != nil || string(binding.SkillID) != parts[0] {
		return "", 0, unavailable()
	}
	files, digest, err := packageFiles(ctx, filepath.Dir(sourcePath))
	if err != nil || digest != binding.ContentRevision {
		return "", 0, unavailable()
	}
	target := filepath.Join(childHome, "selected-skills", parts[0])
	total := 0
	for name, contents := range files {
		total += len(contents.Bytes)
		path := filepath.Join(target, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", 0, unavailable()
		}
		if err := security.WriteAtomicOwnedMode(path, contents.Bytes, contents.Executable); err != nil {
			return "", 0, unavailable()
		}
	}
	if err := writeJSON(filepath.Join(childHome, "selected-skill-proofs", parts[0]+".json"), binding); err != nil {
		return "", 0, unavailable()
	}
	_, copied, err := packageFiles(ctx, target)
	if err != nil || copied != digest {
		return "", 0, unavailable()
	}
	return filepath.Join(target, "SKILL.md"), total, nil
}

func VerifyRuntimePackage(ctx context.Context, home, path string) error {
	rel, err := filepath.Rel(filepath.Join(home, "selected-skills"), path)
	parts := strings.Split(rel, string(filepath.Separator))
	if err != nil || len(parts) != 2 || parts[1] != "SKILL.md" || domain.ID(parts[0]).Validate() != nil {
		return unavailable()
	}
	b, err := readBounded(filepath.Join(home, "selected-skill-proofs", parts[0]+".json"), 4096)
	var binding domain.SkillBinding
	if err != nil || domain.Decode(b, &binding) != nil || domain.ValidateSkills([]domain.SkillBinding{binding}) != nil || string(binding.SkillID) != parts[0] {
		return unavailable()
	}
	_, digest, err := packageFiles(ctx, filepath.Dir(path))
	if err != nil || digest != binding.ContentRevision {
		return unavailable()
	}
	return nil
}
