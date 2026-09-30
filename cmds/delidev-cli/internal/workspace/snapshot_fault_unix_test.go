//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package workspace

import "syscall"

func snapshotDiskFullError() error { return syscall.ENOSPC }
