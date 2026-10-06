// SPDX-License-Identifier: Apache-2.0
//go:build darwin

package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin keeps an opened removed directory's link count at 2. F_GETPATH tracks
// a rename but remains stale after unlink, so comparing it with the canonical
// path captured before unlink detects a retained-parent move of the opened
// original before the replacement name was removed.
func finalRootUnlinkPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func verifyFinalRootAfterUnlink(root, parent *os.File, name, expectedIdentity, expectedPath string) error {
	if _, _, err := directoryIdentityAt(parent, name); err == nil || !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	identity, err := directoryFileIdentity(root)
	if err != nil || identity != expectedIdentity {
		return ResultUncertain()
	}
	var buffer [1024]byte
	if _, err := unix.FcntlInt(root.Fd(), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buffer[0])))); err != nil {
		return ResultUncertain()
	}
	end := bytes.IndexByte(buffer[:], 0)
	if end < 0 || string(buffer[:end]) != expectedPath {
		return ResultUncertain()
	}
	return nil
}
