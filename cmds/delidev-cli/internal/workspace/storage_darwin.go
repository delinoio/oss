// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"errors"
	"golang.org/x/sys/unix"
)

func storageRenameNoReplace(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }
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
