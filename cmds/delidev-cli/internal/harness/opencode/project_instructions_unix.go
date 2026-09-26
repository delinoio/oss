//go:build !windows

package opencode

import (
	"os"

	"golang.org/x/sys/unix"
)

func projectInstructionReadFlags() int { return os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK }
