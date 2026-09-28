//go:build !windows

package opencode

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func checkpointReadFlags() int { return os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK }

func ownedCheckpointOpenFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && ownedCheckpointEntry("", info)
}

func ownedCheckpointEntry(_ string, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	// Native files can be 0644 inside the verified private runtime. Preserve
	// their modes, but forbid shared writers and externally reachable hard links.
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0022 == 0 && (info.IsDir() || info.Mode().IsRegular() && stat.Nlink == 1)
}
