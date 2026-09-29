//go:build darwin

package runmoor

import "golang.org/x/sys/unix"

func renameTartVMNoReplace(oldPath, newPath string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL)
}
