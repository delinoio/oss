// SPDX-License-Identifier: Apache-2.0
package process

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func controllerAbsent(pid int, err error) bool {
	// SysctlKinfoProc reports a size error for an empty kernel result. Confirm
	// that exact PID with the slice API, rather than classifying that error alone.
	if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.EIO) {
		return false
	}
	rows, readErr := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	return readErr == nil && len(rows) == 0
}
