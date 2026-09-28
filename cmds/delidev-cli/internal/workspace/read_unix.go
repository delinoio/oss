//go:build darwin || linux

package workspace

import (
	"os"
	"syscall"
)

// A raced regular-file-to-FIFO replacement must not block before fstat can
// reject it. O_NONBLOCK has no effect on ordinary regular-file reads.
func openWorkspaceEntry(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
