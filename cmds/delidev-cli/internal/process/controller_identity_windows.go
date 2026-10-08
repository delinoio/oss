// SPDX-License-Identifier: Apache-2.0
package process

import (
	"errors"
	"golang.org/x/sys/windows"
)

func controllerBirth(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	return processBirth(handle)
}
func controllerAbsent(_ int, err error) bool { return errors.Is(err, windows.ERROR_INVALID_PARAMETER) }
func controllerPresent(identity ControllerIdentity) ControllerObservation {
	pid := identity.PID
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if controllerAbsent(pid, err) {
			return ControllerExited
		}
		return ControllerUnknown
	}
	defer windows.CloseHandle(handle)
	// Compare again on the same retained handle that supplies exit state. A
	// PID reused between the first birth read and this open is still the old
	// original's exit, never authority over the replacement process.
	birth, err := processBirth(handle)
	if err != nil {
		return ControllerUnknown
	}
	if !(ControllerIdentity{PID: pid, Birth: birth}).Valid() {
		return ControllerUnknown
	}
	if birth != identity.Birth {
		return ControllerExited
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return ControllerUnknown
	}
	if state == windows.WAIT_OBJECT_0 {
		return ControllerExited
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		return ControllerAlive
	}
	return ControllerUnknown
}
