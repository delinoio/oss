// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// removeVerifiedFinalRoot keeps the final unlink relative to an opened private
// parent directory. POSIX has no portable unlink-by-directory-handle primitive.
// After the initial identity check, remove search permission from the opened
// root. A source writer that retains only the root handle or cwd can no longer
// reach the private parent through .. and move that root between the final
// identity check and unlinkat. The final check then rejects a replacement
// installed by a writer that already retained the private parent.
func removeVerifiedFinalRoot(path, expectedIdentity string, beforeUnlink func() error) error {
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

	if beforeUnlink != nil {
		if err := beforeUnlink(); err != nil {
			return err
		}
	}
	if err := root.Chmod(0); err != nil {
		return err
	}
	lockedIdentity, lockedErr := directoryFileIdentity(root)
	currentIdentity, currentMode, currentErr := directoryIdentityAt(parent, filepath.Base(path))
	if lockedErr != nil || currentErr != nil || lockedIdentity != expectedIdentity || currentIdentity != lockedIdentity || currentMode.Perm() != 0 {
		return ResultUncertain()
	}

	if err := unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR); err != nil {
		// A retained writer can make the directory non-empty after the final
		// identity check. Restore the exact verified mode before returning so a
		// retry can reopen the private root and continue the original transition.
		if restoreErr := root.Chmod(0700); restoreErr != nil {
			return fmt.Errorf("unlink final root: %w; restore mode: %v", err, restoreErr)
		}
		if syncErr := root.Sync(); syncErr != nil {
			return fmt.Errorf("unlink final root: %w; sync restored mode: %v", err, syncErr)
		}
		return err
	}
	return nil
}

func directoryIdentityAt(parent *os.File, name string) (string, os.FileMode, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", 0, err
	}
	mode := uint32(stat.Mode)
	if mode&unix.S_IFMT != unix.S_IFDIR {
		return "", 0, ResultUncertain()
	}
	return fmt.Sprintf("%x:%x", uint64(stat.Dev), stat.Ino), os.ModeDir | os.FileMode(mode&07777), nil
}
