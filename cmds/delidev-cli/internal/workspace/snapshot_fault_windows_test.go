package workspace

import "golang.org/x/sys/windows"

// Inject the same native error returned by Windows writes, not Unix ENOSPC.
func snapshotDiskFullError() error { return windows.ERROR_DISK_FULL }
