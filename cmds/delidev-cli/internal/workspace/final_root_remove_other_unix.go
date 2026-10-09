// SPDX-License-Identifier: Apache-2.0
//go:build !darwin && !windows

package workspace

import (
	"errors"
	"os"
	"syscall"
)

func finalRootRecoveryPath(private, _ string) (string, error) { return private, nil }
func finalRootUnlinkPath(string) (string, error)              { return "", nil }

func verifyFinalRootAfterUnlink(root, parent *os.File, name, expectedIdentity, _ string) error {
	if _, _, err := directoryIdentityAt(parent, name); err == nil || !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	identity, err := directoryFileIdentity(root)
	if err != nil || identity != expectedIdentity {
		return ResultUncertain()
	}
	info, statErr := root.Stat()
	stat, statOK := info.Sys().(*syscall.Stat_t)
	if statErr != nil || !statOK || stat.Nlink != 0 {
		return ResultUncertain()
	}
	return nil
}
