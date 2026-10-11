// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const MaxRemovalAdmissionEntries = 100000
const MaxRemovalAdmissionBytes = 32 << 20

// NativeIdentity is metadata only: stable native volume/device, file number and
// object type. It deliberately excludes content, timestamps and private paths.
type NativeIdentity struct {
	Device uint64 `json:"device"`
	File   uint64 `json:"file"`
	Kind   uint32 `json:"kind"`
}
type RemovalEntry struct {
	Path     string          `json:"path"`
	Identity *NativeIdentity `json:"identity,omitempty"`
}

// RemovalAdmission retains relative names, original identities and explicit
// absent operands. A caller binds it to immutable managed ownership before use.
type RemovalAdmission struct {
	Ancestors []NativeIdentity `json:"ancestors"`
	Version   uint32           `json:"version"`
	Operands  []string         `json:"operands"`
	Entries   []RemovalEntry   `json:"entries"`
}

func removalRelative(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", domain.SessionDeletionPending()
	}
	return rel, nil
}

// CaptureRemovalAdmission precedes the caller's ownership reads. It never
// follows a symlink and pins existing ancestors before entering an operand.
func CaptureRemovalAdmission(ctx context.Context, root string, paths []string) (*RemovalAdmission, error) {
	a := &RemovalAdmission{Version: 1}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root || !filepath.IsAbs(root) {
		return nil, domain.SessionDeletionPending()
	}
	for p := filepath.Dir(root); ; p = filepath.Dir(p) {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		id, err := CaptureNativeIdentity(p)
		if err != nil || id.Kind != uint32(os.ModeDir) || len(a.Ancestors) >= 1024 {
			return nil, domain.SessionDeletionPending()
		}
		a.Ancestors = append(a.Ancestors, id)
		if p == filepath.Dir(p) {
			break
		}
	}
	entries := map[string]RemovalEntry{}
	metadataBytes := 0
	capture := func(path string, absence bool) error {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		rel, err := removalRelative(root, path)
		if err != nil {
			return err
		}
		if _, ok := entries[rel]; ok {
			return nil
		}
		id, err := CaptureNativeIdentity(path)
		if errors.Is(err, os.ErrNotExist) && absence {
			entries[rel] = RemovalEntry{Path: rel}
		} else if err != nil {
			return domain.SessionDeletionPending()
		} else {
			entries[rel] = RemovalEntry{Path: rel, Identity: &id}
		}
		metadataBytes += len(rel) + 128
		if len(entries) > MaxRemovalAdmissionEntries || metadataBytes > MaxRemovalAdmissionBytes {
			return domain.SessionDeletionPending()
		}
		return nil
	}
	if err := capture(root, false); err != nil {
		return nil, err
	}
	for _, path := range paths {
		rel, err := removalRelative(root, path)
		if err != nil || rel == "." {
			return nil, domain.SessionDeletionPending()
		}
		if slices.Contains(a.Operands, rel) {
			continue
		}
		a.Operands = append(a.Operands, rel)
		// An absent namespace grants no removal authority. Only existing ancestors
		// are pinned; unrelated namespace creation does not invent an unlink.
		ancestor := root
		for _, component := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
			if component == "." {
				continue
			}
			ancestor = filepath.Join(ancestor, component)
			id, err := CaptureNativeIdentity(ancestor)
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil || id.Kind != uint32(os.ModeDir) {
				return nil, domain.SessionDeletionPending()
			}
			if err := capture(ancestor, false); err != nil {
				return nil, err
			}
		}
		if err := capture(path, true); err != nil {
			return nil, err
		}
		if entries[rel].Identity == nil {
			continue
		}
		if err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Check all pinned ancestors before traversal can enter a replacement.
			if err := a.checkCapturedAncestors(root, p, entries); err != nil {
				return err
			}
			return capture(p, false)
		}); err != nil {
			return nil, domain.SessionDeletionPending()
		}
	}
	slices.Sort(a.Operands)
	for _, entry := range entries {
		a.Entries = append(a.Entries, entry)
	}
	slices.SortFunc(a.Entries, func(x, y RemovalEntry) int { return strings.Compare(x.Path, y.Path) })
	if a.Validate() != nil || a.Check(ctx, root, false) != nil {
		return nil, domain.SessionDeletionPending()
	}
	return a, nil
}
func (a *RemovalAdmission) Validate() error {
	if a == nil || a.Version != 1 || len(a.Ancestors) == 0 || len(a.Ancestors) > 1024 || len(a.Entries) == 0 || len(a.Entries) > MaxRemovalAdmissionEntries || len(a.Operands) > MaxRemovalAdmissionEntries {
		return domain.SessionDeletionPending()
	}
	for _, id := range a.Ancestors {
		if id.Kind != uint32(os.ModeDir) || id.File == 0 {
			return domain.SessionDeletionPending()
		}
	}
	entries := map[string]bool{}
	for i, entry := range a.Entries {
		if i > 0 && a.Entries[i-1].Path >= entry.Path {
			return domain.SessionDeletionPending()
		}
		if filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || entry.Path == ".." || strings.HasPrefix(entry.Path, ".."+string(filepath.Separator)) || entries[entry.Path] {
			return domain.SessionDeletionPending()
		}
		if entry.Identity != nil && entry.Identity.File == 0 {
			return domain.SessionDeletionPending()
		}
		entries[entry.Path] = true
	}
	if a.Entries[0].Path != "." || a.Entries[0].Identity == nil || a.Entries[0].Identity.Kind != uint32(os.ModeDir) {
		return domain.SessionDeletionPending()
	}
	for i, operand := range a.Operands {
		if operand == "." || !entries[operand] || i > 0 && a.Operands[i-1] >= operand {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
func (a *RemovalAdmission) checkCapturedAncestors(root, path string, entries map[string]RemovalEntry) error {
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		rel, err := removalRelative(root, p)
		if err != nil {
			return err
		}
		entry, ok := entries[rel]
		if !ok || entry.Identity == nil {
			return domain.SessionDeletionPending()
		}
		id, err := CaptureNativeIdentity(p)
		if err != nil || id != *entry.Identity {
			return domain.SessionDeletionPending()
		}
		if p == root {
			return a.checkRootAncestors(root)
		}
	}
}
func (a *RemovalAdmission) checkRootAncestors(root string) error {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return domain.SessionDeletionPending()
	}
	p := filepath.Dir(root)
	for i, original := range a.Ancestors {
		current, err := CaptureNativeIdentity(p)
		if err != nil || current != original {
			return domain.SessionDeletionPending()
		}
		parent := filepath.Dir(p)
		if parent == p {
			if i != len(a.Ancestors)-1 {
				return domain.SessionDeletionPending()
			}
			return nil
		}
		p = parent
	}
	return domain.SessionDeletionPending()
}
func (a *RemovalAdmission) entryMap() map[string]RemovalEntry {
	entries := map[string]RemovalEntry{}
	for _, entry := range a.Entries {
		entries[entry.Path] = entry
	}
	return entries
}
func (a *RemovalAdmission) removable(rel string) bool {
	for _, operand := range a.Operands {
		if rel == operand || strings.HasPrefix(rel, operand+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Check permits only disappearance of original removable entries on recovery.
// Present ancestors outside the operands remain mandatory and unchanged.
func (a *RemovalAdmission) Check(ctx context.Context, root string, removed bool) error {
	if a.Validate() != nil || a.checkRootAncestors(root) != nil {
		return domain.SessionDeletionPending()
	}
	for _, entry := range a.Entries {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		id, err := CaptureNativeIdentity(filepath.Join(root, entry.Path))
		if errors.Is(err, os.ErrNotExist) && (entry.Identity == nil || removed && a.removable(entry.Path)) {
			continue
		}
		if err != nil || entry.Identity == nil || id != *entry.Identity {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}

// CheckTree rejects new names as well as changed identities before a native
// directory operation. Native command internals retain their separate syscall
// race limit; this check grants no authority to a replacement inventory.
func (a *RemovalAdmission) CheckTree(ctx context.Context, root, path string) error {
	if a.Validate() != nil {
		return domain.SessionDeletionPending()
	}
	return a.checkTree(ctx, root, path, a.entryMap())
}
func (a *RemovalAdmission) checkTree(ctx context.Context, root, path string, entries map[string]RemovalEntry) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if err := a.checkRootAncestors(root); err != nil {
		return err
	}
	rel, err := removalRelative(root, path)
	if err != nil || !a.removable(rel) {
		return domain.SessionDeletionPending()
	}
	entry, ok := entries[rel]
	if !ok {
		return domain.SessionDeletionPending()
	}
	if entry.Identity != nil {
		if err := a.checkCapturedAncestors(root, path, entries); err != nil {
			return err
		}
	} else {
		// No unlink is authorized. Recheck only captured existing ancestors; an
		// originally absent namespace need not be manufactured for a no-op.
		for p := filepath.Dir(path); ; p = filepath.Dir(p) {
			relative, err := removalRelative(root, p)
			if err != nil {
				return err
			}
			if original, ok := entries[relative]; ok && original.Identity != nil {
				current, err := CaptureNativeIdentity(p)
				if err != nil || current != *original.Identity {
					return domain.SessionDeletionPending()
				}
			}
			if p == root {
				break
			}
		}
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if entry.Identity == nil {
		return domain.SessionDeletionPending()
	}
	count := 0
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return domain.SessionDeletionPending()
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		count++
		if count > MaxRemovalAdmissionEntries {
			return domain.SessionDeletionPending()
		}
		if err := a.checkCapturedAncestors(root, p, entries); err != nil {
			return err
		}
		rel, err := removalRelative(root, p)
		entry, ok := entries[rel]
		if err != nil || !ok || entry.Identity == nil {
			return domain.SessionDeletionPending()
		}
		id, err := CaptureNativeIdentity(p)
		if err != nil || id != *entry.Identity {
			return domain.SessionDeletionPending()
		}
		return nil
	})
}
func (a *RemovalAdmission) CheckTrees(ctx context.Context, root string) error {
	if a.Check(ctx, root, true) != nil {
		return domain.SessionDeletionPending()
	}
	entries := a.entryMap()
	for _, operand := range a.Operands {
		if err := a.checkTree(ctx, root, filepath.Join(root, operand), entries); err != nil {
			return err
		}
	}
	return nil
}
func (a *RemovalAdmission) OriginallyPresent(root, path string) bool {
	rel, err := removalRelative(root, path)
	if err != nil {
		return false
	}
	for _, entry := range a.Entries {
		if entry.Path == rel {
			return entry.Identity != nil
		}
	}
	return false
}

// AdmittedRemover validates one bounded immutable record for a serial cleanup
// batch. It avoids rebuilding the full 100,000-entry index for every operand.
// The caller must not mutate the record while this remover is in use.
type AdmittedRemover struct {
	root      string
	admission *RemovalAdmission
	entries   map[string]RemovalEntry
}

func NewAdmittedRemover(root string, a *RemovalAdmission) (*AdmittedRemover, error) {
	if a.Validate() != nil || a.checkRootAncestors(root) != nil {
		return nil, domain.SessionDeletionPending()
	}
	return &AdmittedRemover{root, a, a.entryMap()}, nil
}
func (r *AdmittedRemover) Remove(ctx context.Context, path string) error {
	return removeAdmittedTreeValidated(ctx, r.root, path, r.admission, r.entries, nil, os.Remove, SyncParent)
}

// RemoveAdmittedTree never takes a fresh baseline. Every traversal and unlink
// uses the original record. The final check-to-unlink syscall gap is separate.
func RemoveAdmittedTree(ctx context.Context, root, path string, a *RemovalAdmission) error {
	return removeAdmittedTree(ctx, root, path, a, nil, os.Remove, SyncParent)
}
func removeAdmittedTree(ctx context.Context, root, path string, a *RemovalAdmission, beforeUnlink func(string), remove func(string) error, syncParent func(string) error) error {
	if a.Validate() != nil {
		return domain.SessionDeletionPending()
	}
	return removeAdmittedTreeValidated(ctx, root, path, a, a.entryMap(), beforeUnlink, remove, syncParent)
}
func removeAdmittedTreeValidated(ctx context.Context, root, path string, a *RemovalAdmission, entries map[string]RemovalEntry, beforeUnlink func(string), remove func(string) error, syncParent func(string) error) error {
	rel, err := removalRelative(root, path)
	if err != nil || !slices.Contains(a.Operands, rel) {
		return domain.SessionDeletionPending()
	}
	original := entries[rel]
	if err := a.checkTree(ctx, root, path, entries); err != nil {
		return err
	}
	if original.Identity == nil {
		return nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := a.checkCapturedAncestors(root, path, entries); err != nil {
			return err
		}
		return syncParent(path)
	}

	for i := len(a.Entries) - 1; i >= 0; i-- {
		entry := a.Entries[i]
		if entry.Path != rel && !strings.HasPrefix(entry.Path, rel+string(filepath.Separator)) {
			continue
		}
		p := filepath.Join(root, entry.Path)
		if beforeUnlink != nil {
			beforeUnlink(p)
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if err := a.checkCapturedAncestors(root, p, entries); err != nil {
			// A removed original directory can account for its already missing children.
			if _, statErr := os.Lstat(p); errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return err
		}
		id, err := CaptureNativeIdentity(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || entry.Identity == nil || id != *entry.Identity {
			return domain.SessionDeletionPending()
		}
		if err := remove(p); err != nil {
			return domain.SessionDeletionPending()
		}
	}
	if err := a.checkCapturedAncestors(root, path, entries); err != nil {
		return err
	}
	if err := syncParent(path); err != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
