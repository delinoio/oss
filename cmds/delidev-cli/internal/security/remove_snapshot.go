// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// RemovalRoot retains the original destructive operand before its owner is
// validated. Absence is an immutable observation, not permission to adopt a path.
type RemovalRoot struct {
	info     os.FileInfo
	identity string
	absent   bool
	digest   string
}

type RemovalEntry struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
	Mode     uint32 `json:"mode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Digest   string `json:"digest,omitempty"`
}

// RemovalSnapshot is a private original-owner deletion intent. It is not a
// discovery inventory and may never be refreshed from a replacement namespace.
type RemovalSnapshot struct {
	Path    string         `json:"path"`
	Absent  bool           `json:"absent"`
	Entries []RemovalEntry `json:"entries"`
}

func ObserveRemovalRoot(ctx context.Context, root, path string) (RemovalRoot, error) {
	if err := ctx.Err(); err != nil {
		return RemovalRoot{}, domain.SafeError(err)
	}
	if !removalPathWithin(root, path) {
		return RemovalRoot{}, domain.SessionDeletionPending()
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return RemovalRoot{absent: true}, nil
	}
	if err != nil || !info.Mode().IsRegular() && !info.IsDir() {
		return RemovalRoot{}, domain.SessionDeletionPending()
	}
	identity, err := removalFileIdentity(path, info)
	if err != nil {
		return RemovalRoot{}, err
	}
	observed := RemovalRoot{info: info, identity: identity}
	if info.Mode().IsRegular() {
		entry, err := captureRemovalEntry(ctx, path, info)
		if err != nil {
			return RemovalRoot{}, err
		}
		observed.digest = entry.Digest
	}
	return observed, nil
}

func CaptureRemovalSnapshot(ctx context.Context, root, path string, original RemovalRoot) (RemovalSnapshot, error) {
	relative, err := filepath.Rel(root, path)
	snapshot := RemovalSnapshot{Path: relative, Absent: original.absent, Entries: []RemovalEntry{}}
	if err != nil || !removalPathWithin(root, path) {
		return snapshot, domain.SessionDeletionPending()
	}
	actual, err := os.Lstat(path)
	if original.absent {
		if !errors.Is(err, os.ErrNotExist) {
			return snapshot, domain.SessionDeletionPending()
		}
		return snapshot, nil
	}
	if err != nil || !os.SameFile(original.info, actual) || original.info.Mode() != actual.Mode() {
		return snapshot, domain.SessionDeletionPending()
	}
	identity, err := removalFileIdentity(path, actual)
	if err != nil || identity != original.identity {
		return snapshot, domain.SessionDeletionPending()
	}
	// A directly validated file cannot change while native owners are joined.
	// Directory contents may legitimately change during original reconciliation;
	// their immutable final snapshot is taken only after that owner has joined.
	if actual.Mode().IsRegular() && (actual.Size() != original.info.Size() || !actual.ModTime().Equal(original.info.ModTime()) || actual.Mode() != original.info.Mode()) {
		return snapshot, domain.SessionDeletionPending()
	}
	err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return domain.SessionDeletionPending()
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		info, err := entry.Info()
		if err != nil {
			return domain.SessionDeletionPending()
		}
		item, err := captureRemovalEntry(ctx, path, info)
		if err != nil {
			return err
		}
		item.Path, err = filepath.Rel(filepath.Join(root, relative), path)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		snapshot.Entries = append(snapshot.Entries, item)
		if len(snapshot.Entries) > 100000 {
			return domain.SessionDeletionPending()
		}
		return nil
	})
	if err != nil {
		return snapshot, err
	}
	if original.digest != "" && (len(snapshot.Entries) != 1 || snapshot.Entries[0].Digest != original.digest) {
		return snapshot, domain.SessionDeletionPending()
	}
	// Recheck the originally admitted root after the full traversal.
	now, err := os.Lstat(path)
	if err != nil || !os.SameFile(original.info, now) {
		return snapshot, domain.SessionDeletionPending()
	}
	return snapshot, nil
}

func captureRemovalEntry(ctx context.Context, path string, info os.FileInfo) (RemovalEntry, error) {
	entry := RemovalEntry{Mode: uint32(info.Mode()), Size: info.Size(), Modified: info.ModTime().UnixNano()}
	identity, err := removalFileIdentity(path, info)
	if err != nil {
		return entry, err
	}
	entry.Identity = identity
	switch {
	case info.IsDir():
	case info.Mode().IsRegular():
		file, err := openNoFollow(path)
		if err != nil {
			return entry, domain.SessionDeletionPending()
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return entry, domain.SessionDeletionPending()
		}
		digest := sha256.New()
		buffer := make([]byte, 64<<10)
		for {
			if err := ctx.Err(); err != nil {
				return entry, domain.SafeError(err)
			}
			count, err := file.Read(buffer)
			digest.Write(buffer[:count])
			if err == io.EOF {
				break
			}
			if err != nil {
				return entry, domain.SessionDeletionPending()
			}
		}
		entry.Digest = hex.EncodeToString(digest.Sum(nil))
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return entry, domain.SessionDeletionPending()
		}
		digest := sha256.Sum256([]byte(target))
		entry.Digest = hex.EncodeToString(digest[:])
	default:
		return entry, domain.SessionDeletionPending()
	}
	actual, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, actual) || info.Mode() != actual.Mode() || !info.IsDir() && (info.Size() != actual.Size() || !info.ModTime().Equal(actual.ModTime())) {
		return entry, domain.SessionDeletionPending()
	}
	return entry, nil
}

// RemoveSnapshotTree validates the entire retained plan before the first unlink.
// A durable removal intent permits already-unlinked original entries on retry;
// every remaining entry must still have its original identity and bytes.
func RemoveSnapshotTree(ctx context.Context, root string, snapshot RemovalSnapshot, started bool) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if !validRemovalRelative(snapshot.Path) || len(snapshot.Entries) > 100000 {
		return domain.SessionDeletionPending()
	}
	path := filepath.Join(root, snapshot.Path)
	if !removalPathWithin(root, path) {
		return domain.SessionDeletionPending()
	}
	if snapshot.Absent {
		if len(snapshot.Entries) != 0 {
			return domain.SessionDeletionPending()
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		return nil
	}
	if len(snapshot.Entries) == 0 || snapshot.Entries[0].Path != "." {
		return domain.SessionDeletionPending()
	}
	retained := map[string]RemovalEntry{}
	for _, entry := range snapshot.Entries {
		if !validRemovalRelative(entry.Path) && entry.Path != "." || entry.Identity == "" || retained[entry.Path].Identity != "" {
			return domain.SessionDeletionPending()
		}
		retained[entry.Path] = entry
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if !started {
			return domain.SessionDeletionPending()
		}
		return SyncParent(path)
	} else if err != nil {
		return domain.SessionDeletionPending()
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(path, func(current string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return domain.SessionDeletionPending()
		}
		relative, err := filepath.Rel(path, current)
		if err != nil || retained[relative].Identity == "" {
			return domain.SessionDeletionPending()
		}
		info, err := item.Info()
		if err != nil {
			return domain.SessionDeletionPending()
		}
		actual, err := captureRemovalEntry(ctx, current, info)
		if err != nil || !sameRemovalEntry(retained[relative], actual) {
			return domain.SessionDeletionPending()
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if !started && len(seen) != len(retained) {
		return domain.SessionDeletionPending()
	}
	for index := len(snapshot.Entries) - 1; index >= 0; index-- {
		entry := snapshot.Entries[index]
		if !seen[entry.Path] {
			continue
		}
		target := filepath.Join(path, entry.Path)
		// Recheck root and operand against the retained identity, never a fresh
		// remover baseline. Namespace unlink races retain their separate boundary.
		original, err := os.Lstat(path)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		rootIdentity, err := removalFileIdentity(path, original)
		if err != nil || rootIdentity != snapshot.Entries[0].Identity {
			return domain.SessionDeletionPending()
		}
		info, err := os.Lstat(target)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		actual, err := captureRemovalEntry(ctx, target, info)
		if err != nil || !sameRemovalEntry(entry, actual) {
			return domain.SessionDeletionPending()
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if os.Remove(target) != nil {
			return domain.SessionDeletionPending()
		}
	}
	if SyncParent(path) != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}

func sameRemovalEntry(expected, actual RemovalEntry) bool {
	if expected.Identity != actual.Identity || expected.Mode != actual.Mode {
		return false
	}
	if os.FileMode(expected.Mode).IsDir() {
		return true
	}
	return expected.Size == actual.Size && expected.Modified == actual.Modified && expected.Digest == actual.Digest
}

func validRemovalRelative(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
func removalPathWithin(root, path string) bool {
	if !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return false
	}
	parent := filepath.Dir(path)
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		return true
	}
	canonical, err := filepath.EvalSymlinks(parent)
	return err == nil && canonical == parent
}

func (root RemovalRoot) WasAbsent() bool { return root.absent }
