//go:build windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openPathIdentity opens path without following reparse points and derives its
// identity from the open handle. Path-based FileInfo values resolve their file
// ID lazily on Windows, so identity is never taken from Lstat.
func openPathIdentity(path string) (*os.File, PathIdentityRecord, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, PathIdentityRecord{}, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
			return nil, PathIdentityRecord{}, os.ErrNotExist
		}
		return nil, PathIdentityRecord{}, e
	}
	record, e := recordFromHandle(h)
	if e != nil {
		windows.CloseHandle(h)
		return nil, PathIdentityRecord{}, e
	}
	f := os.NewFile(uintptr(h), path)
	if record.Kind == ObjectSymlink {
		// Reparse points are never traversed or removed through; keep no handle.
		f.Close()
		return nil, record, nil
	}
	return f, record, nil
}

// recordFromHandle derives the identity of the object behind an open handle.
func recordFromHandle(h windows.Handle) (PathIdentityRecord, error) {
	var data windows.ByHandleFileInformation
	if e := windows.GetFileInformationByHandle(h, &data); e != nil {
		return PathIdentityRecord{}, e
	}
	record := PathIdentityRecord{
		Device:  uint64(data.VolumeSerialNumber),
		Inode:   uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow),
		BirthNS: data.CreationTime.Nanoseconds(),
	}
	switch {
	case data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		record.Kind = ObjectSymlink
	case data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		record.Kind = ObjectDirectory
	default:
		record.Kind = ObjectRegular
		record.Size = int64(data.FileSizeHigh)<<32 | int64(data.FileSizeLow)
		record.ModTimeNS = data.LastWriteTime.Nanoseconds()
	}
	return record, nil
}

// lockRecord describes the object a Lock holds open, read from its handle.
func lockRecord(f *os.File) (PathIdentityRecord, error) {
	conn, e := f.SyscallConn()
	if e != nil {
		return PathIdentityRecord{}, e
	}
	var record PathIdentityRecord
	var recordErr error
	if e := conn.Control(func(fd uintptr) { record, recordErr = recordFromHandle(windows.Handle(fd)) }); e != nil {
		return PathIdentityRecord{}, e
	}
	return record, recordErr
}
