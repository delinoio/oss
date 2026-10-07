//go:build !windows

package opencode

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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
	if ok && (stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0) {
		domain.ObserveOwnership(domain.OwnershipResource, domain.NewID())
	}
	return ok && (info.IsDir() || info.Mode().IsRegular() && stat.Nlink == 1)
}
