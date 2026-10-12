// SPDX-License-Identifier: Apache-2.0
//go:build linux

package security

import (
	"errors"
	"fmt"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func captureUnixNativeIdentity(path string) (NativeIdentity, error) {
	var stat unix.Statx_t
	mask := unix.STATX_BASIC_STATS | unix.STATX_BTIME
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, mask, &stat); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NativeIdentity{}, err
		}
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	required := uint32(unix.STATX_TYPE | unix.STATX_INO | unix.STATX_BTIME)
	if stat.Mask&required != required || stat.Ino == 0 {
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	kind, ok := removalKindFromUnix(stat.Mode)
	if !ok {
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	return NativeIdentity{
		Device:     unix.Mkdev(stat.Dev_major, stat.Dev_minor),
		File:       stat.Ino,
		Kind:       kind,
		Generation: fmt.Sprintf("%d:%d", stat.Btime.Sec, stat.Btime.Nsec),
	}, nil
}

func removalKindFromUnix(mode uint16) (uint32, bool) {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return 0, true
	case unix.S_IFDIR:
		return uint32(os.ModeDir), true
	case unix.S_IFLNK:
		return uint32(os.ModeSymlink), true
	case unix.S_IFIFO:
		return uint32(os.ModeNamedPipe), true
	case unix.S_IFSOCK:
		return uint32(os.ModeSocket), true
	case unix.S_IFBLK:
		return uint32(os.ModeDevice), true
	case unix.S_IFCHR:
		return uint32(os.ModeDevice | os.ModeCharDevice), true
	default:
		return 0, false
	}
}
