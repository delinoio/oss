// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package security

import (
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Unix Lstat obtains no-follow metadata without requiring read access or an
// openable file type. In particular, Unix sockets cannot be opened as files.
func CaptureNativeIdentity(path string) (NativeIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return NativeIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	return NativeIdentity{Device: uint64(stat.Dev), File: uint64(stat.Ino), Kind: uint32(info.Mode().Type())}, nil
}
