// SPDX-License-Identifier: Apache-2.0
//go:build darwin

package security

import (
	"fmt"
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func captureUnixNativeIdentity(path string) (NativeIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return NativeIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 || stat.Birthtimespec.Sec == 0 && stat.Birthtimespec.Nsec == 0 {
		return NativeIdentity{}, domain.SessionDeletionPending()
	}
	return NativeIdentity{
		Device:     uint64(stat.Dev),
		File:       uint64(stat.Ino),
		Kind:       uint32(info.Mode().Type()),
		Generation: fmt.Sprintf("%d:%d", stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec),
	}, nil
}
