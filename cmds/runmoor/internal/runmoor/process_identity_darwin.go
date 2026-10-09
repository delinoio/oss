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
	// An exited PID has a successful empty result on Darwin. The single-record
	// helper converts that result to EIO, which cannot prove process absence.
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	return tartDarwinProcessStartIdentity(pid, entries, err)
}

func tartDarwinProcessStartIdentity(pid int, entries []unix.KinfoProc, err error) (string, error) {
	if errors.Is(err, unix.ESRCH) || err == nil && len(entries) == 0 {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || int(entries[0].Proc.P_pid) != pid {
		return "", fmt.Errorf("process identity result is invalid")
	}
	started := entries[0].Proc.P_starttime
	if started.Sec < 0 || started.Usec < 0 || started.Usec >= 1_000_000 || started.Sec == 0 && started.Usec == 0 {
		return "", fmt.Errorf("process starttime is invalid")
	}
	return fmt.Sprintf("darwin:%d:%d", started.Sec, started.Usec), nil
}
