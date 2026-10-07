// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package codex

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"syscall"
)

// A swapped FIFO must not block a bounded snapshot after a regular-file check.
func forkNonblockFlag() int { return syscall.O_NONBLOCK }

func forkPrivateRolloutNode(_ string, info os.FileInfo) bool {
	if info.Mode().Perm()&0022 != 0 {
		domain.ObserveOwnership(domain.OwnershipResource, domain.NewID())
	}
	return true
}
