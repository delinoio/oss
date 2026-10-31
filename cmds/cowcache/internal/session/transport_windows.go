package session

import (
	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	"net"
	"time"
)

func listen() (net.Listener, string, func(), error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, "", func() {}, e
	}
	p := `\\.\pipe\cowc-session-` + uuid.NewString()
	l, e := winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + u.User.Sid.String() + ")"})
	return l, p, func() {}, e
}
func dial(p string) (net.Conn, error) { d := 10 * time.Second; return winio.DialPipe(p, &d) }
func peer(c net.Conn) (int, error) {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return 0, errAuth
	}
	var pid uint32
	e := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid)
	return int(pid), e
}
