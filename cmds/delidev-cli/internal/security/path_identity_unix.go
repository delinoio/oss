//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func kindFromMode(mode uint32) ObjectKind {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return ObjectRegular
	case unix.S_IFDIR:
		return ObjectDirectory
	case unix.S_IFLNK:
		return ObjectSymlink
	default:
		return ObjectOther
	}
}

func recordFromStat(st *unix.Stat_t, birthNS int64) PathIdentityRecord {
	record := PathIdentityRecord{Kind: kindFromMode(uint32(st.Mode)), Device: uint64(st.Dev), Inode: uint64(st.Ino), BirthNS: birthNS}
	if record.Kind == ObjectRegular {
		record.Size = st.Size
		record.ModTimeNS = st.Mtim.Nano()
	}
	return record
}

// openPathIdentity opens path without following links and returns its identity.
// A symlink cannot be opened without following it, so it is described from
// lstat with no handle; callers never traverse or remove through it.
func openPathIdentity(path string) (*os.File, PathIdentityRecord, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(e, unix.ELOOP) {
		var st unix.Stat_t
		if e := unix.Lstat(path, &st); e != nil {
			return nil, PathIdentityRecord{}, pathError(e)
		}
		return nil, recordFromStat(&st, 0), nil
	}
	if e != nil {
		return nil, PathIdentityRecord{}, pathError(e)
	}
	record, e := recordFromFD(fd)
	if e != nil {
		unix.Close(fd)
		return nil, PathIdentityRecord{}, e
	}
	return os.NewFile(uintptr(fd), path), record, nil
}

func recordFromFD(fd int) (PathIdentityRecord, error) {
	var st unix.Stat_t
	if e := unix.Fstat(fd, &st); e != nil {
		return PathIdentityRecord{}, e
	}
	return recordFromStat(&st, birthTimeNS(fd, &st)), nil
}

func pathError(e error) error {
	if errors.Is(e, unix.ENOENT) {
		return os.ErrNotExist
	}
	return e
}

// lockRecord describes the object a Lock holds open, read from its handle.
func lockRecord(f *os.File) (PathIdentityRecord, error) {
	conn, e := f.SyscallConn()
	if e != nil {
		return PathIdentityRecord{}, e
	}
	var record PathIdentityRecord
	var recordErr error
	if e := conn.Control(func(fd uintptr) { record, recordErr = recordFromFD(int(fd)) }); e != nil {
		return PathIdentityRecord{}, e
	}
	return record, recordErr
}
