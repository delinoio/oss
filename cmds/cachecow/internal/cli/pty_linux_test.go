package cli

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

func terminalInput(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	m, e := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); e != nil {
		t.Fatal(e)
	}
	n, e := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if e != nil {
		t.Fatal(e)
	}
	s, e := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}
