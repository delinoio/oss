// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func sessionBackupFileIdentity(_ *os.File, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Ino == 0 {
		return "", backupUnavailable()
	}
	return fmt.Sprintf("%x:%x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
