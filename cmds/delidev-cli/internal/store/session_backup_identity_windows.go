// SPDX-License-Identifier: Apache-2.0
//go:build windows

package store

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func sessionBackupFileIdentity(file *os.File, _ os.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", backupUnavailable()
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
