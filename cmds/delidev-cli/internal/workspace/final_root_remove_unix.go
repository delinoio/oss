// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// removeVerifiedFinalRoot keeps the final unlink relative to an opened private
// parent directory. POSIX has no portable unlink-by-directory-handle primitive;
// the caller therefore performs the identity check through the same private
// namespace immediately before this anchored unlink. Windows uses its stronger
// handle-disposition operation in final_root_remove_windows.go.
func removeVerifiedFinalRoot(path, expectedIdentity string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()

	root, err := os.Open(path)
	if err != nil {
		return err
	}
	identity, identityErr := directoryFileIdentity(root)
	if identityErr != nil || identity != expectedIdentity {
		root.Close()
		return ResultUncertain()
	}
	defer root.Close()

	return unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR)
}
