//go:build darwin

package runmoor

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameTartVMNoReplace(oldPath, newPath string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL)
}

func renameTartVMAtNoReplace(dir *os.File, oldName, newName string) error {
	return unix.RenameatxNp(int(dir.Fd()), oldName, int(dir.Fd()), newName, unix.RENAME_EXCL)
}
