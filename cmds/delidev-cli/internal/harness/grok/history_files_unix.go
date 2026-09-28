//go:build !windows

package grok

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func historyReadFlags() int { return os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK }

func ownedHistoryOpenFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && ownedHistoryEntry("", info)
}

func ownedHistoryEntry(_ string, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	// Native history files may be 0644 under the verified 0700 home. Every
	// component must still belong to this owner and reject shared writes. A
	// hard-linked file could have another reachable name outside that boundary.
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0022 == 0 && (info.IsDir() || (info.Mode().IsRegular() && stat.Nlink == 1))
}
