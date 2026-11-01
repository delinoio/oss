package session

import (
	"golang.org/x/sys/unix"
	"net"
)

func peer(c net.Conn) (int, error) {
	r, e := c.(*net.UnixConn).SyscallConn()
	if e != nil {
		return 0, e
	}
	var pid int
	var inner error
	e = r.Control(func(fd uintptr) {
		v, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		inner = err
		if err == nil {
			pid = int(v.Pid)
		}
	})
	if e != nil {
		return 0, e
	}
	return pid, inner
}
