// SPDX-License-Identifier: Apache-2.0
//go:build windows

package skills

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func preparationDirectoryIdentity(file *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", unavailable()
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
