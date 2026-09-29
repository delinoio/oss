//go:build darwin

package runmoor

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func tartRunProcessStartIdentity(pid int) (string, error) {
	if pid <= 0 {
		return "", os.ErrNotExist
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", err
	}
	if int(info.Proc.P_pid) != pid {
		return "", os.ErrNotExist
	}
	started := info.Proc.P_starttime
	if started.Sec < 0 || started.Usec < 0 || started.Usec >= 1_000_000 || started.Sec == 0 && started.Usec == 0 {
		return "", fmt.Errorf("process starttime is invalid")
	}
	return fmt.Sprintf("darwin:%d:%d", started.Sec, started.Usec), nil
}
