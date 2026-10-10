// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package security

import (
	"fmt"
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func removalFileIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return "", domain.SessionDeletionPending()
	}
	return fmt.Sprintf("%x:%x", uint64(stat.Dev), stat.Ino), nil
}
