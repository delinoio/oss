// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// PathIdentity is the validated state of one managed path: either the exact
// filesystem object that ownership validation observed, or proven absence. It
// carries no content, only the object identity needed to reject a replacement.
type PathIdentity struct {
	present bool
	info    os.FileInfo
}

// CapturePathIdentity records what currently exists at path without following
// links. A missing path is captured as proven absence.
func CapturePathIdentity(path string) (PathIdentity, error) {
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return PathIdentity{}, nil
	}
	if e != nil {
		return PathIdentity{}, domain.SessionDeletionPending()
	}
	return PathIdentity{present: true, info: info}, nil
}

// Present reports whether the captured path existed.
func (i PathIdentity) Present() bool { return i.present }

// matches reports whether current is the same object that was captured. Device
// and inode identity bind directories; regular files additionally bind size and
// modification time so a recreated file that reuses an inode is still foreign.
func (i PathIdentity) matches(current os.FileInfo) bool {
	if !i.present || current == nil || !os.SameFile(i.info, current) || i.info.Mode().Type() != current.Mode().Type() {
		return false
	}
	if i.info.Mode().IsRegular() {
		return i.info.Size() == current.Size() && i.info.ModTime().Equal(current.ModTime())
	}
	return true
}

// RemoveOwnedTree is used only after independently stopping original owners.
// It takes its own baseline of whatever exists at path, so it is only for
// callers that have no earlier validated identity to bind. Callers that validated
// ownership first must use RemoveOwnedTreeIdentity.
func RemoveOwnedTree(ctx context.Context, root, path string) error {
	return removeOwnedTree(ctx, root, path, nil)
}

// RemoveOwnedTreeIdentity removes path only when it is still exactly the object
// captured during ownership validation. A path that was absent at validation
// must still be absent, and a different object at the path (including a
// same-content replacement) is left untouched and reported as pending cleanup.
// A validated object that has since vanished counts as already removed.
func RemoveOwnedTreeIdentity(ctx context.Context, root, path string, expected PathIdentity) error {
	return removeOwnedTree(ctx, root, path, &expected)
}

// removeOwnedTree never follows links, rejects replaced roots and observes
// cancellation and an explicit traversal bound before each unlink. It makes no
// disk-space claim.
func removeOwnedTree(ctx context.Context, root, path string, expected *PathIdentity) error {
	parent := filepath.Dir(path)
	if _, e := os.Lstat(parent); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return domain.SessionDeletionPending()
	}
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent || !strings.HasPrefix(parent, root+string(filepath.Separator)) {
		return domain.SessionDeletionPending()
	}
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return SyncParent(path)
	} else if e != nil || info.Mode()&os.ModeSymlink != 0 {
		return domain.SessionDeletionPending()
	}
	// Reject a replacement before any traversal or unlink. The walk below only
	// detects changes after its own baseline, so identity must come from the
	// validated object.
	if expected != nil && !expected.matches(info) {
		return domain.SessionDeletionPending()
	}
	type entry struct {
		path string
		info os.FileInfo
	}
	entries := []entry{}
	e = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		entries = append(entries, entry{p, info})
		if len(entries) > 100000 {
			return domain.SessionDeletionPending()
		}
		return nil
	})
	if e != nil {
		return domain.SessionDeletionPending()
	}
	// The root could be swapped between the check above and the walk.
	if expected != nil && (len(entries) == 0 || !expected.matches(entries[0].info)) {
		return domain.SessionDeletionPending()
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		current, e := os.Lstat(entries[i].path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !os.SameFile(current, entries[i].info) {
			return domain.SessionDeletionPending()
		}
		if e := os.Remove(entries[i].path); e != nil {
			return domain.SessionDeletionPending()
		}
	}
	if e := SyncParent(path); e != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
