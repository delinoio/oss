//go:build windows

package state

import (
	"errors"

	"golang.org/x/sys/windows"
)

var (
	openProcess         = windows.OpenProcess
	waitForSingleObject = windows.WaitForSingleObject
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	processHandle, err := openProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true
		}
		return false
	}
	defer windows.CloseHandle(processHandle)

	waitStatus, err := waitForSingleObject(processHandle, 0)
	if err != nil {
		return false
	}
	return waitStatus == uint32(windows.WAIT_TIMEOUT)
}
