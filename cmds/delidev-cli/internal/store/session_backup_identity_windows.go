// SPDX-License-Identifier: Apache-2.0
//go:build windows

package store

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

// FILE_BASIC_INFO includes the change generation that LastWriteTime alone
// cannot prove when a writer restores the original visible modification time.
func sessionBackupFileIdentity(file *os.File) (string, error) {
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", backupUnavailable()
	}
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes                                          uint32
		Reserved                                                uint32 // Keep the native FILE_BASIC_INFO layout at 40 bytes on each supported architecture.
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", err
	}
	if basic.ChangeTime == 0 {
		return "", backupUnavailable()
	}
	return fmt.Sprintf("%x:%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, basic.ChangeTime), nil
}
