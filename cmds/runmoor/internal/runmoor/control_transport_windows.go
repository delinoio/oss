package runmoor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func controlPipe(c Config) string {
	// Include the user and normalized storage root; independent installations
	// cannot accidentally share an endpoint through a case variant of a path.
	sid, _ := currentSID()
	return fmt.Sprintf(`\\.\pipe\runmoor-%x`, sha256.Sum256([]byte(sid.String()+":"+strings.ToLower(filepath.Clean(c.Storage.State)))))
}
func listenControl(c Config) (net.Listener, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(controlPipe(c), &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid.String() + ")", InputBufferSize: 65536, OutputBufferSize: 65536})
}
func checkControlEndpoint(Config) error { return nil }
func dialControl(ctx context.Context, c Config) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, controlPipe(c))
	if err != nil {
		return nil, err
	}
	pid, err := controlPeerPID(conn)
	if err == nil {
		_, err = windowsProcess(pid)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
func controlPeerPID(conn net.Conn) (int, error) {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, osPipeError()
	}
	var pid uint32
	r, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeServerProcessId").Call(f.Fd(), uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return 0, err
	}
	return int(pid), nil
}
func osPipeError() error {
	return problem(ErrControl, "Cannot verify the local pipe owner.", "Restart the owned Runmoor manager.")
}
