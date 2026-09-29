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

// RemoveOwnedTree is used only after independently stopping original owners.
// It never follows links, rejects replaced roots and observes cancellation and
// an explicit traversal bound before each unlink. It makes no disk-space claim.
func RemoveOwnedTree(ctx context.Context, root, path string) error {
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
