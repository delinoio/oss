// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package codex

import (
	"os"
	"syscall"
)

// A swapped FIFO must not block a bounded snapshot after a regular-file check.
func forkNonblockFlag() int { return syscall.O_NONBLOCK }

func forkPrivateRolloutNode(_ string, info os.FileInfo) bool {
	return info.Mode().Perm()&0022 == 0
}
