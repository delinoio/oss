// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io/fs"
	"os"
)

// CleanupRuntime removes only the identity-checked private runtime after the
// caller independently joins its original native process and descendants.
func CleanupRuntime(home string, original os.FileInfo) error {
	if err := security.CheckPrivateDir(home); err != nil {
		return Invalid()
	}
	current, err := os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return Invalid()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return Invalid()
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			_ = root.Close()
		}
	}()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(anchored, original) {
		return Invalid()
	}
	count, total := 0, int64(0)
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return Invalid()
		}
		if entry.IsDir() {
			return nil
		}
		count++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || count > 2048 {
			return Invalid()
		}
		total += info.Size()
		if total > 64<<20 {
			return Invalid()
		}
		return nil
	}); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return Invalid()
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return Invalid()
		}
	}
	// Windows does not allow removing a directory while its OpenRoot handle is
	// still live. Close the anchored traversal before checking and unlinking the
	// original runtime directory; the deferred close remains for error paths.
	if err := root.Close(); err != nil {
		return Invalid()
	}
	rootClosed = true
	current, err = os.Stat(home)
	if err != nil || !os.SameFile(current, original) {
		return Invalid()
	}
	if err := os.Remove(home); err != nil {
		return Invalid()
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		return Invalid()
	}
	if err := security.SyncParent(home); err != nil {
		return Invalid()
	}
	return nil
}
