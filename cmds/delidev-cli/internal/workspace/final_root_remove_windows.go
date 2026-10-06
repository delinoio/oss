// SPDX-License-Identifier: Apache-2.0
//go:build windows

package workspace

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// removeVerifiedFinalRoot marks the verified directory handle for deletion.
// Unlike a name-based Remove, the disposition remains bound to the opened
// native file object if a writer renames that object after verification.
func removeVerifiedFinalRoot(path, expectedIdentity string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		name,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}

	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return ResultUncertain()
	}
	defer file.Close()
	identity, identityErr := directoryFileIdentity(file)
	if identityErr != nil || identity != expectedIdentity {
		return ResultUncertain()
	}

	type dispositionInformation struct {
		Flags uint32
	}
	disposition := dispositionInformation{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS}
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInformationEx,
		(*byte)(unsafe.Pointer(&disposition)),
		uint32(unsafe.Sizeof(disposition)),
	)
}
