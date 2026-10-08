// SPDX-License-Identifier: Apache-2.0
package process

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func controllerAbsent(pid int, err error) bool {
	// Missing /proc data alone may reflect unavailable or hidden procfs. The
	// kernel's signal-zero ESRCH is the independent definitive absence check.
	// Signal zero performs no delivery and grants no termination authority.
	return errors.Is(err, os.ErrNotExist) && errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}
