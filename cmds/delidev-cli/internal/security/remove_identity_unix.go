// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package security

import (
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"syscall"
)

func removalPathIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return "", domain.SessionDeletionPending()
	}
	return fmt.Sprintf("%x:%x:%x", uint64(stat.Dev), stat.Ino, uint32(info.Mode().Type())), nil
}
