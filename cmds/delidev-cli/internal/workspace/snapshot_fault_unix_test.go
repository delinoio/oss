//go:build !windows

package workspace

import "syscall"

func snapshotDiskFullError() error { return syscall.ENOSPC }
