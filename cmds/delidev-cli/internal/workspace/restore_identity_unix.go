// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"fmt"
	"os"
	"syscall"
)

func directoryFileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Ino == 0 {
		return "", ResultUncertain()
	}
	return fmt.Sprintf("%x:%x", uint64(stat.Dev), stat.Ino), nil
}
