//go:build linux

package runmoor

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameTartVMNoReplace(oldPath, newPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
}

func renameTartVMAtNoReplace(dir *os.File, oldName, newName string) error {
	return unix.Renameat2(int(dir.Fd()), oldName, int(dir.Fd()), newName, unix.RENAME_NOREPLACE)
}
