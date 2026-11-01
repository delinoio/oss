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
	e = r.Control(func(fd uintptr) { pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	if e != nil {
		return 0, e
	}
	return pid, inner
}
