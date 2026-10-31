//go:build linux || darwin

package process

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func privateRoot(_ string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return errors.New("process state root must be owned by this user with mode 0700")
	}
	return nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func listenControl() (net.Listener, string, func(), error) {
	dir, err := os.MkdirTemp("/tmp", "cowc-p-")
	if err != nil {
		return nil, "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	endpoint := filepath.Join(dir, "control")
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		cleanup()
	}
	return l, endpoint, cleanup, err
}
func dialControl(endpoint string) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, 10*time.Second)
}
func sendStreams(c net.Conn, s Streams) error {
	u := c.(*net.UnixConn)
	_, _, err := u.WriteMsgUnix([]byte{1}, unix.UnixRights(int(s.Stdin.Fd()), int(s.Stdout.Fd()), int(s.Stderr.Fd())), nil)
	return err
}
func receiveStreams(c net.Conn) (Streams, error) {
	var b [1]byte
	oob := make([]byte, unix.CmsgSpace(3*4))
	_, n, flags, _, err := c.(*net.UnixConn).ReadMsgUnix(b[:], oob)
	if err != nil {
		return Streams{}, err
	}
	messages, err := unix.ParseSocketControlMessage(oob[:n])
	if err != nil {
		return Streams{}, err
	}
	var fds []int
	for _, m := range messages {
		rights, e := unix.ParseUnixRights(&m)
		if e != nil {
			err = e
			break
		}
		fds = append(fds, rights...)
	}
	if err != nil || flags&unix.MSG_CTRUNC != 0 || len(fds) != 3 {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return Streams{}, ErrUnavailable
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd)
	}
	return Streams{os.NewFile(uintptr(fds[0]), "stdin"), os.NewFile(uintptr(fds[1]), "stdout"), os.NewFile(uintptr(fds[2]), "stderr")}, nil
}
func terminationSignals() (<-chan os.Signal, func()) {
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	return c, func() { signal.Stop(c) }
}
func startUnix(cmd *exec.Cmd) (<-chan int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = 125
			var e *exec.ExitError
			if errors.As(err, &e) {
				code = e.ExitCode()
				if status, ok := e.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					code = 128 + int(status.Signal())
				}
			}
		}
		done <- code
	}()
	return done, nil
}
