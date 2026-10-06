//go:build darwin

package runmoor

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func controlPeerPID(conn net.Conn) (int, error) {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, os.ErrInvalid
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var inspectErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			inspectErr = e
			return
		}
		if cred.Uid != uint32(os.Geteuid()) {
			inspectErr = os.ErrPermission
			return
		}
		pid, inspectErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	if err != nil {
		return 0, err
	}
	if inspectErr != nil {
		return 0, inspectErr
	}
	if pid <= 0 {
		return 0, os.ErrPermission
	}
	return pid, nil
}
