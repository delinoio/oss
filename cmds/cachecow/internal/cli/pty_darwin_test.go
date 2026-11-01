package cli

import (
	"golang.org/x/sys/unix"
	"os"
	"testing"
	"unsafe"
)

func terminalInput(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	m, e := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, request := range []uintptr{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), request, 0)
		if e != 0 {
			t.Fatal(e)
		}
	}
	var name [128]byte
	_, _, err := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if err != 0 {
		t.Fatal(err)
	}
	s, e := os.OpenFile(unix.ByteSliceToString(name[:]), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}
