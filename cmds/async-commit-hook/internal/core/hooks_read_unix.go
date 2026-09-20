//go:build !windows

package core

import (
	"os"

	"golang.org/x/sys/unix"
)

func openNativeHook(root *os.Root, path string) (*os.File, error) {
	// A concurrent FIFO replacement must not stall the identity revalidation.
	return root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
