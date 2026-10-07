//go:build linux

package runmoor

import "golang.org/x/sys/unix"

func renameServiceDefinitionNoReplace(first, second string) error {
	return unix.Renameat2(unix.AT_FDCWD, first, unix.AT_FDCWD, second, unix.RENAME_NOREPLACE)
}
