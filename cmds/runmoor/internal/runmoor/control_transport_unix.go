//go:build !windows

package runmoor

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

func listenControl(c Config) (net.Listener, error) {
	path := filepath.Join(c.Storage.State, "control.sock")
	if st, e := os.Lstat(path); e == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, problem(ErrPermission, "Control socket path contains another kind of file.", "Preserve the file and choose a clean state directory.")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		return nil, problem(ErrControl, "Cannot listen on the private control socket.", "Check state path permissions and Unix socket length.")
	}
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		return nil, e
	}
	return listener, nil
}
func checkControlEndpoint(c Config) error {
	path := filepath.Join(c.Storage.State, "control.sock")
	st, e := os.Lstat(path)
	if e != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0077 != 0 {
		return problem(ErrControl, "No private manager socket is available.", "Start the manager.")
	}
	return nil
}
func dialControl(ctx context.Context, c Config) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(c.Storage.State, "control.sock"))
}
