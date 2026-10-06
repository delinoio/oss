//go:build darwin

package runmoor

import "golang.org/x/sys/unix"

func exchangeServiceFiles(first, second string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, first, unix.AT_FDCWD, second, unix.RENAME_SWAP)
}
