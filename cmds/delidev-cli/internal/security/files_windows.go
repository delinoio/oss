//go:build windows

package security

import (
	"errors"
	"io"
	"os"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
)

func descriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")")
}
func createPrivateDirectory(path string) error {
	sd, err := descriptor()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	return windows.CreateDirectory(p, &sa)
}
func checkPrivate(path string, _ os.FileInfo) error {
	// Existing ACLs do not gate access. New directories retain private defaults.
	return nil
}

func replaceFile(from, to string) error {
	a, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func syncDirectory(string) error { return nil } // MoveFileEx uses WRITE_THROUGH.
func openNoFollow(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func openPrivateAppend(path string) (*os.File, bool, error) {
	_, statErr := os.Lstat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, false, statErr
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, false, err
	}
	f := os.NewFile(uintptr(h), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = domain.Fail(domain.PermissionDenied, "Invalid private journal.", "Inspect the data scope.")
	}
	if err == nil {
		err = checkPrivate(path, info)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekEnd)
	}
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return f, created, nil
}

type Lock struct {
	f    *os.File
	over windows.Overlapped
}

func TryLock(path string) (*Lock, error)         { return tryLock(path, true) }
func TryLockExisting(path string) (*Lock, error) { return tryLock(path, false) }
func tryLock(path string, create bool) (*Lock, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, domain.Fail(domain.PermissionDenied, "Invalid lock file.", "Inspect the data scope.")
	}
	var f *os.File
	var err error
	if create {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	} else {
		var p *uint16
		p, err = windows.UTF16PtrFromString(path)
		if err == nil {
			var h windows.Handle
			h, err = windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err == nil {
				f = os.NewFile(uintptr(h), path)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	l := &Lock{f: f}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = domain.Fail(domain.PermissionDenied, "Invalid lock file.", "Inspect the data scope.")
	}
	if err == nil {
		err = checkPrivate(path, info)
	}
	if err == nil {
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.over)
	}
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, domain.Fail(domain.Conflict, "Another process owns this data scope.", "Use the running server or stop it explicitly.")
		}
		return nil, err
	}
	return l, nil
}
func (l *Lock) Close() error {
	// Windows may defer releasing a byte-range lock after handle close when a
	// controller exits. Unlock it explicitly so a lifecycle observer does not
	// see a transient access-denied result during a legitimate handoff.
	unlockErr := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.over)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func publishImmutable(from, to string) error {
	a, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH)
}
