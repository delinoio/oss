//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package shim

import (
	"fmt"
	"os"
	"syscall"
)

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("shim directory %s must be owned by the current user with mode 0700; select or create a private directory", dir)
	}
	return nil
}
