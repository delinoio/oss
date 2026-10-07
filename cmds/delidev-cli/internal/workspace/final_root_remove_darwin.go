// SPDX-License-Identifier: Apache-2.0
//go:build darwin

package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

func finalRootRecoveryPath(private, quarantine string) (string, error) {
	_, privateErr := os.Lstat(private)
	_, quarantineErr := os.Lstat(quarantine)
	privatePresent := privateErr == nil
	quarantinePresent := quarantineErr == nil
	if privateErr != nil && !errors.Is(privateErr, os.ErrNotExist) || quarantineErr != nil && !errors.Is(quarantineErr, os.ErrNotExist) {
		return "", ResultUncertain()
	}
	if privatePresent && quarantinePresent {
		return "", ResultUncertain()
	}
	if privatePresent {
		return private, nil
	}
	if quarantinePresent {
		return quarantine, nil
	}
	return private, nil
}

// Darwin keeps an opened removed directory's link count at 2. F_GETPATH tracks
// a rename but remains stale after unlink, so comparing it with the canonical
// path captured before unlink detects a retained-parent move of the opened
// original before the replacement name was removed.
func finalRootUnlinkPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// Darwin cannot unlink a directory by its open handle. Move the verified root
// into a fresh operation-private namespace before the final unlink. A writer
// that retained the old private parent can still replace the old name, but it
// cannot change the newly opened quarantine parent or the claimed object there.
func removeVerifiedFinalRoot(path, quarantinePath, expectedIdentity string, beforeUnlink, afterIdentityCheck func() error) error {
	sourceParent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer sourceParent.Close()
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
	if beforeUnlink != nil {
		if err := beforeUnlink(); err != nil {
			return err
		}
	}
	contents, readErr := root.Readdirnames(1)
	if len(contents) != 0 || readErr != io.EOF {
		return ResultUncertain()
	}
	quarantineParent, err := os.Open(filepath.Dir(quarantinePath))
	if err != nil {
		return err
	}
	defer quarantineParent.Close()
	lockedIdentity, lockedErr := directoryFileIdentity(root)
	currentIdentity, currentMode, currentErr := directoryIdentityAt(sourceParent, filepath.Base(path))
	if lockedErr != nil || currentErr != nil || lockedIdentity != expectedIdentity || currentIdentity != lockedIdentity || currentMode.Perm() == 0 {
		return restoreFinalRootMode(root, ResultUncertain())
	}
	if afterIdentityCheck != nil {
		if err := afterIdentityCheck(); err != nil {
			return restoreFinalRootMode(root, err)
		}
	}
	lockedIdentity, lockedErr = directoryFileIdentity(root)
	currentIdentity, currentMode, currentErr = directoryIdentityAt(sourceParent, filepath.Base(path))
	if lockedErr != nil || currentErr != nil || lockedIdentity != expectedIdentity || currentIdentity != lockedIdentity || currentMode.Perm() == 0 {
		return restoreFinalRootMode(root, ResultUncertain())
	}
	if path != quarantinePath {
		if _, _, err := directoryIdentityAt(quarantineParent, filepath.Base(quarantinePath)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return ResultUncertain()
		}
		if err := unix.RenameatxNp(int(sourceParent.Fd()), filepath.Base(path), int(quarantineParent.Fd()), filepath.Base(quarantinePath), unix.RENAME_EXCL); err != nil {
			return ResultUncertain()
		}
	}
	currentIdentity, currentMode, currentErr = directoryIdentityAt(quarantineParent, filepath.Base(quarantinePath))
	if currentErr != nil || currentIdentity != expectedIdentity || currentMode.Perm() == 0 {
		return restoreFinalRootMode(root, ResultUncertain())
	}
	if err := root.Chmod(0); err != nil {
		return err
	}
	currentIdentity, currentMode, currentErr = directoryIdentityAt(quarantineParent, filepath.Base(quarantinePath))
	if currentErr != nil || currentIdentity != expectedIdentity || currentMode.Perm() != 0 {
		return restoreFinalRootMode(root, ResultUncertain())
	}
	expectedPath, err := finalRootUnlinkPath(quarantinePath)
	if err != nil {
		return restoreFinalRootMode(root, ResultUncertain())
	}
	if err := unix.Unlinkat(int(quarantineParent.Fd()), filepath.Base(quarantinePath), unix.AT_REMOVEDIR); err != nil {
		return restoreFinalRootMode(root, err)
	}
	return verifyFinalRootAfterUnlink(root, quarantineParent, filepath.Base(quarantinePath), expectedIdentity, expectedPath)
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
