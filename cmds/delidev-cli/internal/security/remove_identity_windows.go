// SPDX-License-Identifier: Apache-2.0
//go:build windows

package security

import (
	"fmt"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
)

func removalFileIdentity(path string, original os.FileInfo) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", domain.SessionDeletionPending()
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", domain.SessionDeletionPending()
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(original, actual) {
		return "", domain.SessionDeletionPending()
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", domain.SessionDeletionPending()
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
