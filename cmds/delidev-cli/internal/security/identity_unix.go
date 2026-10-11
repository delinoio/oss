// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package security

import (
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Unix Lstat contains an eager native identity even for a symlink. For regular
// files and directories retain the identity returned by the no-follow handle.
func CaptureNativeIdentity(path string) (NativeIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return NativeIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		f, err := openNoFollow(path)
		if err != nil {
			return NativeIdentity{}, err
		}
		info, err = f.Stat()
		closeErr := f.Close()
		if err != nil {
			return NativeIdentity{}, err
		}
		if closeErr != nil {
			return NativeIdentity{}, closeErr
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	return NativeIdentity{Device: uint64(stat.Dev), File: uint64(stat.Ino), Kind: uint32(info.Mode().Type())}, nil
}
