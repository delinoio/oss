//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
)

// removeTreeAfterOpenHook runs after the root is opened and compared, before any
// descendant is unlinked. Tests use it to swap the path deterministically; it is
// nil in production.
var removeTreeAfterOpenHook func()

// removeTree removes expected at path through file descriptors. The parent
// directory is opened once; the object is opened relative to it and compared
// with the captured identity; descendants are listed, opened and unlinked
// relative to their already-open parent directory, never by path. A directory
// that is replaced after it was opened therefore cannot redirect the removal,
// and every directory stays on the root's device.
//
// Residual window: a check-then-unlink pair for a single name is not atomic on
// POSIX. Between the identity comparison and unlinkat a hostile process that can
// write the parent directory could swap that one name, losing the replacement
// (a file, or an empty directory). Directory contents are never at risk because
// unlinkat(AT_REMOVEDIR) refuses non-empty directories.
func removeTree(ctx context.Context, path string, expected PathIdentity) error {
	parentFD, e := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(e, unix.ENOENT) {
		return nil
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	defer unix.Close(parentFD)
	name := filepath.Base(path)
	fd, e := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(e, unix.ENOENT) {
		return nil // the validated original already vanished
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	defer unix.Close(fd)
	current, e := recordFromFD(fd)
	if e != nil || !expected.record.sameObject(current) {
		return domain.SessionDeletionPending()
	}
	if removeTreeAfterOpenHook != nil {
		removeTreeAfterOpenHook()
	}
	count := 0
	flags := 0
	if current.Kind == ObjectDirectory {
		if e := removeDirectoryContents(ctx, fd, current.Device, 1, &count); e != nil {
			return e
		}
		flags = unix.AT_REMOVEDIR
	}
	// Confirm the name still resolves to the object that was just emptied.
	var st unix.Stat_t
	if e := unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); errors.Is(e, unix.ENOENT) {
		return nil
	} else if e != nil || uint64(st.Dev) != current.Device || uint64(st.Ino) != current.Inode {
		return domain.SessionDeletionPending()
	}
	if e := ctx.Err(); e != nil {
		return domain.SafeError(e)
	}
	if e := unix.Unlinkat(parentFD, name, flags); e != nil && !errors.Is(e, unix.ENOENT) {
		return domain.SessionDeletionPending()
	}
	return nil
}

// removeDirectoryContents unlinks every entry of the open directory dirFD.
func removeDirectoryContents(ctx context.Context, dirFD int, device uint64, depth int, count *int) error {
	if depth > maxRemoveDepth {
		return domain.SessionDeletionPending()
	}
	listing, e := unix.Dup(dirFD)
	if e != nil {
		return domain.SessionDeletionPending()
	}
	dir := os.NewFile(uintptr(listing), "")
	names, e := dir.Readdirnames(-1)
	dir.Close()
	if e != nil {
		return domain.SessionDeletionPending()
	}
	for _, name := range names {
		if e := ctx.Err(); e != nil {
			return domain.SafeError(e)
		}
		if *count++; *count > maxRemoveEntries {
			return domain.SessionDeletionPending()
		}
		var st unix.Stat_t
		if e := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); errors.Is(e, unix.ENOENT) {
			continue
		} else if e != nil {
			return domain.SessionDeletionPending()
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if e := unix.Unlinkat(dirFD, name, 0); e != nil && !errors.Is(e, unix.ENOENT) {
				return domain.SessionDeletionPending()
			}
			continue
		}
		child, e := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(e, unix.ENOENT) {
			continue
		}
		if e != nil {
			return domain.SessionDeletionPending()
		}
		record, e := recordFromFD(child)
		if e != nil || record.Kind != ObjectDirectory || record.Device != device || record.Inode != uint64(st.Ino) {
			unix.Close(child)
			return domain.SessionDeletionPending()
		}
		e = removeDirectoryContents(ctx, child, device, depth+1, count)
		unix.Close(child)
		if e != nil {
			return e
		}
		if e := unix.Unlinkat(dirFD, name, unix.AT_REMOVEDIR); e != nil && !errors.Is(e, unix.ENOENT) {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
