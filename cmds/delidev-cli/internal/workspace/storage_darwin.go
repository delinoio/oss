package workspace

import "golang.org/x/sys/unix"

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
