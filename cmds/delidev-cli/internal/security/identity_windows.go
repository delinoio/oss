// SPDX-License-Identifier: Apache-2.0
//go:build windows

package security

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Capture native values while the no-follow handle is open. A path-based
// Windows FileInfo must never serve as durable removal identity.
func CaptureNativeIdentity(path string) (NativeIdentity, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return NativeIdentity{}, err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return NativeIdentity{}, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	err = errors.Join(err, windows.CloseHandle(h))
	if err != nil {
		return NativeIdentity{}, err
	}
	kind := os.FileMode(0)
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		kind = os.ModeSymlink
	} else if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		kind = os.ModeDir
	}
	return NativeIdentity{Device: uint64(info.VolumeSerialNumber), File: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), Kind: uint32(kind)}, nil
}
