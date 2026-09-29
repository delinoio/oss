//go:build !windows

package codex

import "syscall"

// A swapped FIFO must not block a bounded snapshot after a regular-file check.
func forkNonblockFlag() int { return syscall.O_NONBLOCK }
