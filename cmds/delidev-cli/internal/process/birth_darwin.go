package process

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func preciseBirth(pid int) (string, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if p.Proc.P_pid != int32(pid) {
		return "", os.ErrNotExist
	}
	return fmt.Sprintf("%d.%06d", p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec), nil
}
