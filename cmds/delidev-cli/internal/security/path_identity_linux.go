//go:build linux

// SPDX-License-Identifier: Apache-2.0
package security

import "golang.org/x/sys/unix"

// birthTimeNS reads the inode creation time through statx. It is zero when the
// filesystem does not report one.
func birthTimeNS(fd int, _ *unix.Stat_t) int64 {
	var sx unix.Statx_t
	if e := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_BTIME, &sx); e != nil || sx.Mask&unix.STATX_BTIME == 0 {
		return 0
	}
	return sx.Btime.Sec*1_000_000_000 + int64(sx.Btime.Nsec)
}
