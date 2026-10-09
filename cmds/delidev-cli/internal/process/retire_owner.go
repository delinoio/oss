// SPDX-License-Identifier: Apache-2.0
package process

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// RetireCompletedOwnerContext retires only the caller's original unpublished
// owner after all native journals prove completion. Callers fence new admission
// with their original job lock; this does not grant authority over a live child.
func RetireCompletedOwnerContext(ctx context.Context, root string, owner domain.ID, original os.FileInfo) error {
	return retireCompletedOwnerContext(ctx, root, owner, original, os.Remove, security.SyncParent)
}

func retireCompletedOwnerContext(ctx context.Context, root string, owner domain.ID, original os.FileInfo, remove func(string) error, syncParent func(string) error) error {
	if owner.Validate() != nil {
		return ownershipError()
	}
	directory := filepath.Join(root, string(owner))
	before, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		// Absence must not hide a retained recovery lock with unknown ownership.
		if _, err := os.Lstat(filepath.Join(root, string(owner)+".recovery.lock")); !errors.Is(err, os.ErrNotExist) {
			return ownershipError()
		}
		return nil
	}
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || original != nil && !os.SameFile(original, before) {
		return ownershipError()
	}
	if original == nil {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) == 0 {
			return ownershipError()
		}
	}
	if err := ReconcileOwnerContext(ctx, root, owner); err != nil {
		return err
	}
	lockPath := filepath.Join(root, string(owner)+".recovery.lock")
	lockInfo, err := os.Lstat(lockPath)
	if err != nil {
		return ownershipError()
	}
	lock, err := security.TryLockExisting(lockPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(before, current) {
		return ownershipError()
	}
	opened, err := os.Open(directory)
	if err != nil {
		return ownershipError()
	}
	openedInfo, statErr := opened.Stat()
	names, readErr := opened.Readdirnames(1)
	closeErr := opened.Close()
	if statErr != nil || !os.SameFile(before, openedInfo) || len(names) != 0 || readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		return ownershipError()
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	current, err = os.Lstat(directory)
	if err != nil || !os.SameFile(before, current) {
		return ownershipError()
	}
	// Remove, rather than recursive removal, refuses any late unknown journal.
	if err := remove(directory); err != nil {
		return ownershipError()
	}
	if err := syncParent(directory); err != nil {
		return ownershipError()
	}
	if err := lock.Close(); err != nil {
		return ownershipError()
	}
	current, err = os.Lstat(lockPath)
	if err != nil || !os.SameFile(lockInfo, current) {
		return ownershipError()
	}
	if err := remove(lockPath); err != nil {
		return ownershipError()
	}
	if err := syncParent(lockPath); err != nil {
		return ownershipError()
	}
	for _, path := range []string{directory, lockPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return ownershipError()
		}
	}
	return nil
}
