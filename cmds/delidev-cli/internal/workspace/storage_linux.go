// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"errors"
	"golang.org/x/sys/unix"
)

func storageRenameNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
func storageCapacity(path string) (*uint64, *uint64) {
	var stat unix.Statfs_t
	if unix.Statfs(path, &stat) != nil {
		return nil, nil
	}
	capacity := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return &capacity, &free
}

func storageFull(err error) bool { return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT) }
