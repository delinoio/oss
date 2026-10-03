// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func snapshotSymlinkKind(info os.FileInfo) (snapshotLinkKind, error) {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || info.Mode()&os.ModeSymlink == 0 {
		return "", ResultUncertain()
	}
	if attributes.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return snapshotDirectoryLink, nil
	}
	return snapshotFileLink, nil
}

// Windows links retain a directory/file reparse type independently of their
// target's existence. os.Symlink infers it from that target and corrupts forward
// and dangling directory links in a sorted copy; select the captured type here.
func createSnapshotSymlink(link, target string, kind snapshotLinkKind) error {
	flags := uint32(2) // SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE on supported Windows.
	switch kind {
	case snapshotDirectoryLink:
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	case snapshotFileLink:
	default:
		return snapshotUnsupported()
	}
	if !filepath.IsAbs(target) {
		return ResultUncertain()
	}
	if !strings.HasPrefix(target, `\\?\`) {
		if strings.HasPrefix(target, `\\`) {
			target = `\\?\UNC\` + strings.TrimPrefix(target, `\\`)
		} else {
			target = `\\?\` + target
		}
	}
	newName, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	oldName, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	return windows.CreateSymbolicLink(newName, oldName, flags)
}
