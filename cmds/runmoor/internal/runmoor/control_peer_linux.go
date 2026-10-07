//go:build linux

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
	var cred *unix.Ucred
	var inspectErr error
	err = raw.Control(func(fd uintptr) { cred, inspectErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if err != nil {
		return 0, err
	}
	if inspectErr != nil {
		return 0, inspectErr
	}
	if cred.Uid != uint32(os.Geteuid()) || cred.Pid <= 0 {
		return 0, os.ErrPermission
	}
	return int(cred.Pid), nil
}
