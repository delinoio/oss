//go:build !windows

package core

import (
	"os"

	"golang.org/x/sys/unix"
)

func openCredentialFile(path string) (*os.File, error) {
	// Nonblocking open prevents a regular-file-to-FIFO replacement from hanging
	// before the opened descriptor's type and identity can be checked.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
