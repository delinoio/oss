//go:build windows

package store

import "golang.org/x/sys/windows"

func volumeSpace(path string) (uint64, uint64, error) {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, capacity, free uint64
	err = windows.GetDiskFreeSpaceEx(value, &available, &capacity, &free)
	return capacity, available, err
}
