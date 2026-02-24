//go:build windows

package state

import "golang.org/x/sys/windows"

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	processHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(processHandle)

	waitStatus, err := windows.WaitForSingleObject(processHandle, 0)
	if err != nil {
		return false
	}
	return waitStatus == windows.WAIT_TIMEOUT
}
