// SPDX-License-Identifier: Apache-2.0
//go:build windows

package workspace

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func directoryFileIdentity(file *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", ResultUncertain()
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
