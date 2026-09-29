package workspace

import (
	"errors"
	"golang.org/x/sys/windows"
)

func storageRenameNoReplace(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
}
func storageCapacity(path string) (*uint64, *uint64) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil
	}
	var available, capacity, free uint64
	if windows.GetDiskFreeSpaceEx(name, &available, &capacity, &free) != nil {
		return nil, nil
	}
	return &capacity, &available
}

func storageFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
