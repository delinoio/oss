//go:build windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// removeTree removes expected at path. Windows has no descriptor-relative
// unlink, so this is a path walk that re-binds the root to its captured identity
// before every unlink. That narrows, but does not close, the window in which a
// directory swapped in at the root path could lose its descendants: the guarantee
// is a per-unlink root check, not handle-anchored removal.
func removeTree(ctx context.Context, path string, expected PathIdentity) error {
	rootStillCaptured := func() error {
		f, current, e := openPathIdentity(path)
		if errors.Is(e, os.ErrNotExist) {
			return os.ErrNotExist
		}
		if e != nil {
			return domain.SessionDeletionPending()
		}
		if f != nil {
			f.Close()
		}
		if !expected.record.sameObject(current) {
			return domain.SessionDeletionPending()
		}
		return nil
	}
	if e := rootStillCaptured(); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	type entry struct {
		path string
		info os.FileInfo
	}
	entries := []entry{}
	e := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
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
		if len(entries) > maxRemoveEntries {
			return domain.SessionDeletionPending()
		}
		return nil
	})
	if e != nil {
		return domain.SessionDeletionPending()
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		if e := rootStillCaptured(); errors.Is(e, os.ErrNotExist) {
			return nil
		} else if e != nil {
			return e
		}
		current, e := os.Lstat(entries[i].path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !os.SameFile(current, entries[i].info) {
			return domain.SessionDeletionPending()
		}
		if i == 0 {
			// The held handle would keep the root delete-pending.
			expected.Release()
		}
		if e := os.Remove(entries[i].path); e != nil {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
