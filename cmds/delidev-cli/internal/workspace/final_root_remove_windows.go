// SPDX-License-Identifier: Apache-2.0
//go:build windows

package workspace

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// removeVerifiedFinalRoot marks the verified directory handle for deletion.
// Unlike a name-based Remove, the disposition remains bound to the opened
// native file object if a writer renames that object after verification.
func removeVerifiedFinalRoot(path, expectedIdentity string, beforeUnlink, afterIdentityCheck func() error) error {
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
	if beforeUnlink != nil {
		if err := beforeUnlink(); err != nil {
			return err
		}
	}
	if afterIdentityCheck != nil {
		if err := afterIdentityCheck(); err != nil {
			return err
		}
	}

	type dispositionInformation struct {
		Flags uint32
	}
	disposition := dispositionInformation{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_FORCE_IMAGE_SECTION_CHECK | windows.FILE_DISPOSITION_POSIX_SEMANTICS | windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE}
	err = windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfoEx,
		(*byte)(unsafe.Pointer(&disposition)),
		uint32(unsafe.Sizeof(disposition)),
	)
	if err == nil {
		return nil
	}
	// POSIX disposition is unavailable on older Windows versions and some
	// file systems. The legacy disposition still binds deletion to this
	// identity-checked handle, so it preserves the replacement safety of this
	// operation while retaining compatibility with those environments.
	if !errors.Is(err, windows.ERROR_INVALID_FUNCTION) && !errors.Is(err, windows.ERROR_INVALID_PARAMETER) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return err
	}
	legacyDisposition := struct{ DeleteFile uint8 }{DeleteFile: 1}
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&legacyDisposition)),
		uint32(unsafe.Sizeof(legacyDisposition)),
	)
}
