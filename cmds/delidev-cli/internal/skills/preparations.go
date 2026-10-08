// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const preparationLimit = 4096
const preparationJournalBytes = 8 << 20

type preparationFile struct {
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}
type preparationIntent struct {
	Version      uint32                  `json:"version"`
	Scope        domain.SkillReadRequest `json:"scope"`
	RootIdentity string                  `json:"root_identity,omitempty"`
	Files        []preparationFile       `json:"files,omitempty"`
	Ready        bool                    `json:"ready,omitempty"`
	Removing     bool                    `json:"removing,omitempty"`
	Deleted      bool                    `json:"deleted,omitempty"`
}

func (m Manager) preparationPath(id domain.ID) string {
	return filepath.Join(m.Root, "skill-preparations", string(id)+".json")
}
func (m Manager) terminalPreparationPath(id domain.ID) string {
	hash := sha256.Sum256([]byte(id))
	return filepath.Join(m.Root, "skill-preparation-receipts", hex.EncodeToString(hash[:])[:2], string(id)+".json")
}
func (m Manager) retainedPreparation(id domain.ID) (preparationIntent, error) {
	v, e := readPreparation(m.terminalPreparationPath(id))
	if !errors.Is(e, os.ErrNotExist) {
		return v, e
	}
	return readPreparation(m.preparationPath(id))
}
func (m Manager) retirePreparation(id domain.ID, v preparationIntent) error {
	v.Files = nil
	v.Ready = false
	path := m.terminalPreparationPath(id)
	if e := security.PrivateDir(filepath.Dir(path)); e != nil {
		return e
	}
	if e := writePreparation(path, v); e != nil {
		return e
	}
	if e := os.Remove(m.preparationPath(id)); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return security.SyncParent(m.preparationPath(id))
}

// Session deletion already joined original native owners. Retire the original
// preparation only for its exact bound references, then prove byte absence.
func (m Manager) DeletePreparedSnapshot(ctx context.Context, machine domain.ID, binding domain.SkillBinding) error {
	intent, e := m.retainedPreparation(binding.SnapshotID)
	if e != nil || intent.Scope.MachineID != machine || intent.Scope.WorkerDeviceID != binding.WorkerDeviceID {
		return unavailable()
	}
	found := false
	for _, v := range intent.Scope.Selections {
		if v == binding {
			found = true
		}
	}
	if !found {
		return unavailable()
	}
	scope := intent.Scope
	scope.Action = domain.CleanupSkillPreparation
	return m.CleanupPreparation(ctx, scope)
}
func (m Manager) preparationGate(id domain.ID) (*security.Lock, error) {
	if id.Validate() != nil {
		return nil, unavailable()
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "skill-preparation-locks")); err != nil {
		return nil, unavailable()
	}
	return security.TryLock(filepath.Join(m.Root, "skill-preparation-locks", string(id)+".lock"))
}
func readPreparation(path string) (preparationIntent, error) {
	var v preparationIntent
	raw, err := security.ReadPrivate(path, preparationJournalBytes)
	if err != nil {
		return v, err
	}
	if domain.Decode(raw, &v) != nil || v.Version != 1 || domain.ValidateSkillPreparation(v.Scope) != nil || v.Scope.Action != "" || len(v.Files) > domain.MaxSelectedSkills*MaxFiles+1 || v.Ready && v.RootIdentity == "" || v.Deleted && !v.Removing {
		return v, unavailable()
	}
	seen := map[string]bool{}
	var total int64
	for _, file := range v.Files {
		digest, err := hex.DecodeString(file.SHA256)
		if !fs.ValidPath(file.Name) || file.Name == "." || seen[file.Name] || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != file.SHA256 || file.Size < 0 {
			return v, unavailable()
		}
		seen[file.Name] = true
		total += file.Size
	}
	if total > MaxTotalBytes+(256<<10) {
		return v, bound()
	}
	return v, nil
}
func writePreparation(path string, v preparationIntent) error {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > preparationJournalBytes {
		return bound()
	}
	return security.WriteAtomicOwned(path, raw)
}
func directoryIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", unavailable()
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return preparationDirectoryIdentity(file)
}
func preparationScope(scope domain.SkillReadRequest) domain.SkillReadRequest {
	// Direct trusted Manager callers still receive a private original intent.
	// The authenticated workspace lane separately requires an explicit server proof.
	if scope.Preparation == nil && len(scope.Selections) > 0 {
		scope.Preparation = &domain.SkillPreparationProof{ServerID: scope.ActorID, RequestID: scope.Selections[0].SnapshotID, OriginalInstanceID: scope.WorkerInstanceID}
		scope.Preparation.ScopeDigest = domain.SkillPreparationDigest(scope)
	}
	return scope
}
func (m Manager) Prepare(ctx context.Context, scope domain.SkillReadRequest) error {
	scope = preparationScope(scope)
	if scope.Action != "" || domain.ValidateSkillPreparation(scope) != nil {
		return unavailable()
	}
	id := scope.Preparation.RequestID
	gate, err := m.preparationGate(id)
	if err != nil {
		return unavailable()
	}
	defer gate.Close()
	journal := m.preparationPath(id)
	intent, err := m.retainedPreparation(id)
	if errors.Is(err, os.ErrNotExist) {
		if err = security.PrivateDir(filepath.Dir(journal)); err != nil {
			return unavailable()
		}
		entries, e := boundedDirectory(filepath.Dir(journal), preparationLimit)
		if e != nil || len(entries) >= preparationLimit {
			return bound()
		}
		intent = preparationIntent{Version: 1, Scope: scope}
		// Synchronize original scope before even creating the copy namespace.
		if err = writePreparation(journal, intent); err != nil {
			return unavailable()
		}
	} else if err != nil {
		return unavailable()
	}
	if intent.Scope.Preparation == nil || *intent.Scope.Preparation != *scope.Preparation || domain.SkillPreparationDigest(intent.Scope) != domain.SkillPreparationDigest(scope) || intent.Removing || intent.Deleted {
		return unavailable()
	}
	target := snapshotPath(m.Root, id)
	if intent.RootIdentity != "" {
		identity, e := directoryIdentity(target)
		if e != nil || identity != intent.RootIdentity {
			return unavailable()
		}
	}
	if intent.Ready {
		_, err = m.Resolve(ctx, scope.Selections)
		return err
	}
	files, saved, err := m.plannedPreparation(ctx, scope)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(saved)
	if err != nil || len(raw) > 256<<10 {
		return bound()
	}
	files["snapshot.json"] = packageFile{Bytes: raw}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	plan := make([]preparationFile, 0, len(names))
	for _, name := range names {
		file := files[name]
		digest := sha256.Sum256(file.Bytes)
		plan = append(plan, preparationFile{Name: name, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(file.Bytes)), Executable: file.Executable})
	}
	if len(intent.Files) > 0 {
		a, _ := json.Marshal(intent.Files)
		b, _ := json.Marshal(plan)
		if string(a) != string(b) {
			return unavailable()
		}
	} else {
		intent.Files = plan
		if err = writePreparation(journal, intent); err != nil {
			return err
		}
	}
	if intent.RootIdentity == "" {
		if err = security.PrivateDir(filepath.Dir(target)); err != nil {
			return unavailable()
		}
		if err = os.Mkdir(target, 0700); err != nil {
			return unavailable()
		}
		identity, e := directoryIdentity(target)
		if e != nil {
			return unavailable()
		}
		intent.RootIdentity = identity
		// No package byte is written before the original native root is synchronized.
		if err = writePreparation(journal, intent); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return unavailable()
	}
	defer root.Close()
	// Publish the closed snapshot descriptor last. Partial copies remain owned by
	// this intent and never become executable selections.
	names = append(removeName(names, "snapshot.json"), "snapshot.json")
	for _, name := range names {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		value := files[name]
		nativeName := filepath.FromSlash(name)
		if err = makeRootDirectories(root, filepath.Dir(nativeName)); err != nil {
			return unavailable()
		}
		mode := fs.FileMode(0600)
		if value.Executable {
			mode = 0700
		}
		file, e := root.OpenFile(nativeName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(e, os.ErrExist) {
			file, e = root.OpenFile(nativeName, os.O_RDONLY|resourceOpenFlags(), 0)
			if e != nil {
				return unavailable()
			}
			info, statErr := file.Stat()
			actual, readErr := io.ReadAll(io.LimitReader(file, MaxPackageBytes+1))
			file.Close()
			if statErr != nil || !info.Mode().IsRegular() || readErr != nil || string(actual) != string(value.Bytes) || info.Mode().Perm() != mode {
				return unavailable()
			}
			continue
		}
		if e != nil {
			return unavailable()
		}
		_, e = file.Write(value.Bytes)
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e != nil || closeErr != nil {
			return unavailable()
		}
	}
	if identity, e := directoryIdentity(target); e != nil || identity != intent.RootIdentity {
		return unavailable()
	}
	if err = security.SyncParent(filepath.Join(target, "snapshot.json")); err != nil {
		return unavailable()
	}
	intent.Ready = true
	return writePreparation(journal, intent)
}
func removeName(values []string, name string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != name {
			result = append(result, value)
		}
	}
	return result
}
func makeRootDirectories(root *os.Root, name string) error {
	if name == "." {
		return nil
	}
	parts := strings.Split(name, string(filepath.Separator))
	current := ""
	for _, part := range parts {
		current = filepath.Join(current, part)
		if err := root.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return unavailable()
		}
	}
	return nil
}
func (m Manager) plannedPreparation(ctx context.Context, scope domain.SkillReadRequest) (map[string]packageFile, snapshot, error) {
	files := map[string]packageFile{}
	saved := snapshot{Scope: scope}
	total := 0
	for _, selection := range scope.Selections {
		raw, err := readBounded(filepath.Join(m.Root, "skill-inventories", string(selection.InventoryID)+".json"), 256<<10)
		if err != nil {
			return nil, saved, unavailable()
		}
		var inv inventory
		if domain.Decode(raw, &inv) != nil || !sameScope(inv.Scope, scope) {
			return nil, saved, unavailable()
		}
		var selected *packageRecord
		for i := range inv.Packages {
			if inv.Packages[i].Entry.SkillID == selection.SkillID {
				selected = &inv.Packages[i]
			}
		}
		if selected == nil || selected.Entry.ContentRevision != selection.ContentRevision {
			return nil, saved, unavailable()
		}
		disabled, err := m.disabled()
		if err != nil || disabled[selected.Path] {
			return nil, saved, unavailable()
		}
		resources, digest, err := packageFiles(ctx, selected.Path)
		if err != nil {
			return nil, saved, err
		}
		if digest != selection.ContentRevision {
			return nil, saved, unavailable()
		}
		for name, value := range resources {
			total += len(value.Bytes)
			if total > MaxTotalBytes {
				return nil, saved, bound()
			}
			relative := path.Join(string(selection.SkillID), filepath.ToSlash(name))
			if !fs.ValidPath(relative) {
				return nil, saved, unavailable()
			}
			files[relative] = value
		}
		saved.Entries = append(saved.Entries, selected.Entry)
	}
	return files, saved, nil
}

// Cleanup accepts only the original synchronized intent. It never adopts a
// replacement root or removes a changed/unlisted entry after uncertain copying.
func (m Manager) CleanupPreparation(ctx context.Context, scope domain.SkillReadRequest) error {
	if scope.Action != domain.CleanupSkillPreparation || domain.ValidateSkillPreparation(scope) != nil {
		return unavailable()
	}
	id := scope.Preparation.RequestID
	gate, err := m.preparationGate(id)
	if err != nil {
		return unavailable()
	}
	defer gate.Close()
	journal := m.preparationPath(id)
	intent, err := m.retainedPreparation(id)
	if errors.Is(err, os.ErrNotExist) {
		// A lost dispatch may never have entered Prepare. Publish a removal-only
		// original intent under the same gate before proving absence. It fences
		// any delayed original preparation without adopting an existing root.
		if _, statErr := os.Lstat(snapshotPath(m.Root, id)); !errors.Is(statErr, os.ErrNotExist) {
			return unavailable()
		}
		original := scope
		original.Action = ""
		intent = preparationIntent{Version: 1, Scope: original, Removing: true, Deleted: true}
		if err = security.PrivateDir(filepath.Dir(journal)); err != nil {
			return unavailable()
		}
		entries, e := boundedDirectory(filepath.Dir(journal), preparationLimit)
		if e != nil || len(entries) >= preparationLimit {
			return bound()
		}
		return m.retirePreparation(id, intent)
	}
	if err != nil {
		return unavailable()
	}
	if *intent.Scope.Preparation != *scope.Preparation || domain.SkillPreparationDigest(intent.Scope) != domain.SkillPreparationDigest(scope) {
		return unavailable()
	}
	target := snapshotPath(m.Root, id)
	info, statErr := os.Lstat(target)
	if errors.Is(statErr, os.ErrNotExist) {
		intent.Removing = true
		intent.Deleted = true
		return m.retirePreparation(id, intent)
	}
	if statErr != nil || intent.Deleted || intent.RootIdentity == "" || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return unavailable()
	}
	identity, err := directoryIdentity(target)
	if err != nil || identity != intent.RootIdentity {
		return unavailable()
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return unavailable()
	}
	defer root.Close()
	expected := map[string]preparationFile{}
	directories := map[string]bool{".": true}
	for _, file := range intent.Files {
		expected[file.Name] = file
		for parent := path.Dir(file.Name); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	var actualFiles, actualDirs []string
	var inspect func(string) error
	inspect = func(dir string) error {
		file, e := root.Open(filepath.FromSlash(dir))
		if e != nil {
			return unavailable()
		}
		entries, e := file.ReadDir(domain.MaxSelectedSkills*MaxFiles + 2)
		file.Close()
		if e != nil && !errors.Is(e, io.EOF) || len(entries) > domain.MaxSelectedSkills*MaxFiles+1 {
			return bound()
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return domain.SafeError(ctx.Err())
			}
			name := path.Join(dir, entry.Name())
			if entry.IsDir() {
				if !directories[name] {
					return unavailable()
				}
				actualDirs = append(actualDirs, name)
				if e = inspect(name); e != nil {
					return e
				}
				continue
			}
			claim, ok := expected[name]
			if !ok || entry.Type()&os.ModeSymlink != 0 {
				return unavailable()
			}
			f, e := root.OpenFile(filepath.FromSlash(name), os.O_RDONLY|resourceOpenFlags(), 0)
			if e != nil {
				return unavailable()
			}
			metadata, e := f.Stat()
			if e != nil || !metadata.Mode().IsRegular() || metadata.Size() != claim.Size || (metadata.Mode().Perm()&0111 != 0) != claim.Executable {
				f.Close()
				return unavailable()
			}
			hash := sha256.New()
			_, e = io.Copy(hash, io.LimitReader(f, claim.Size+1))
			f.Close()
			if e != nil || hex.EncodeToString(hash.Sum(nil)) != claim.SHA256 {
				return unavailable()
			}
			actualFiles = append(actualFiles, name)
		}
		return nil
	}
	if err = inspect("."); err != nil {
		return err
	}
	intent.Removing = true
	if err = writePreparation(journal, intent); err != nil {
		return err
	}
	for _, name := range actualFiles {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		if err = root.Remove(filepath.FromSlash(name)); err != nil {
			return unavailable()
		}
	}
	sort.Slice(actualDirs, func(i, j int) bool { return len(actualDirs[i]) > len(actualDirs[j]) })
	for _, name := range actualDirs {
		if err = root.Remove(filepath.FromSlash(name)); err != nil {
			return unavailable()
		}
	}
	if identity, err = directoryIdentity(target); err != nil || identity != intent.RootIdentity {
		return unavailable()
	}
	root.Close()
	if err = os.Remove(target); err != nil {
		return unavailable()
	}
	if err = security.SyncParent(target); err != nil {
		return unavailable()
	}
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return unavailable()
	}
	intent.Deleted = true
	return m.retirePreparation(id, intent)
}
