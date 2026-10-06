// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"io/fs"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	RuntimeFileLimit = 2048
	RuntimeByteLimit = 64 << 20
)

type CleanupStage string

const (
	CleanupNativeProcess CleanupStage = "native-process"
	CleanupAuthFile      CleanupStage = "auth-file"
	CleanupValidation    CleanupStage = "runtime-validation"
	CleanupInventory     CleanupStage = "runtime-inventory"
	CleanupRemoval       CleanupStage = "runtime-removal"
	CleanupSync          CleanupStage = "runtime-sync"
)

type CleanupReason string

const (
	CleanupUnconfirmed CleanupReason = "unconfirmed"
	CleanupReadFailed  CleanupReason = "read-failed"
	CleanupMismatch    CleanupReason = "bundle-mismatch"
	CleanupFilesystem  CleanupReason = "filesystem"
	CleanupIdentity    CleanupReason = "identity"
	CleanupSymlink     CleanupReason = "symlink"
	CleanupNotRegular  CleanupReason = "non-regular"
	CleanupFileLimit   CleanupReason = "file-limit"
	CleanupByteLimit   CleanupReason = "byte-limit"
)

// RuntimeCleanupError retains only closed classifications and bounded counters.
// Its public error remains the original authentication recovery failure; no
// filesystem error, path or file content escapes into diagnostics or logs.
type RuntimeCleanupError struct {
	Stage     CleanupStage
	Reason    CleanupReason
	FileCount int
	Bytes     int64
}

func (e *RuntimeCleanupError) Error() string { return Invalid().Error() }
func (e *RuntimeCleanupError) Unwrap() error { return Invalid() }

// CleanupRuntime removes only the identity-checked private runtime after the
// caller independently joins its original native process and descendants.
func CleanupRuntime(home string, original os.FileInfo) error {
	count, total := 0, int64(0)
	fail := func(stage CleanupStage, reason CleanupReason) error {
		return &RuntimeCleanupError{Stage: stage, Reason: reason, FileCount: count, Bytes: total}
	}
	if err := security.CheckPrivateDir(home); err != nil {
		return fail(CleanupValidation, CleanupFilesystem)
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return fail(CleanupValidation, CleanupIdentity)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fail(CleanupValidation, CleanupFilesystem)
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			_ = root.Close()
		}
	}()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(anchored, original) {
		return fail(CleanupValidation, CleanupIdentity)
	}
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fail(CleanupInventory, CleanupFilesystem)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fail(CleanupInventory, CleanupSymlink)
		}
		if entry.IsDir() {
			return nil
		}
		count++
		info, err := entry.Info()
		if err != nil {
			return fail(CleanupInventory, CleanupFilesystem)
		}
		if !info.Mode().IsRegular() {
			return fail(CleanupInventory, CleanupNotRegular)
		}
		if count > RuntimeFileLimit {
			return fail(CleanupInventory, CleanupFileLimit)
		}
		// Saturate each inspected size above the limit. Sparse files cannot
		// overflow this diagnostic counter and bypass the existing byte bound.
		total += min(info.Size(), RuntimeByteLimit+1)
		if total > RuntimeByteLimit {
			return fail(CleanupInventory, CleanupByteLimit)
		}
		return nil
	}); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return fail(CleanupRemoval, CleanupFilesystem)
		}
	}
	// Windows does not allow removing a directory while its OpenRoot handle is
	// still live. Close the anchored traversal before checking and unlinking the
	// original runtime directory; the deferred close remains for error paths.
	if err := root.Close(); err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	rootClosed = true
	current, err = os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return fail(CleanupRemoval, CleanupIdentity)
	}
	if err := os.Remove(home); err != nil {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		return fail(CleanupRemoval, CleanupFilesystem)
	}
	if err := security.SyncParent(home); err != nil {
		return fail(CleanupSync, CleanupFilesystem)
	}
	return nil
}
