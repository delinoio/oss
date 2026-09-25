//go:build windows

package security

import (
	"errors"
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
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	want, err := descriptor()
	if err != nil {
		return err
	}
	wantACL, _, err := want.DACL()
	if err != nil {
		return err
	}
	if acl == nil || acl.AceCount != 1 {
		return domain.Fail(domain.PermissionDenied, "State permissions are not owner-only.", "Choose a DeliDev-created private data directory.")
	}
	var actual, expected *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &actual); err != nil {
		return err
	}
	if err := windows.GetAce(wantACL, 0, &expected); err != nil {
		return err
	}
	if actual.Header.AceType != expected.Header.AceType || actual.Mask != expected.Mask || !(*windows.SID)(unsafe.Pointer(&actual.SidStart)).Equals((*windows.SID)(unsafe.Pointer(&expected.SidStart))) {
		return domain.Fail(domain.PermissionDenied, "State permissions are not owner-only.", "Choose a DeliDev-created private data directory.")
	}
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

type Lock struct {
	f    *os.File
	over windows.Overlapped
}

func TryLock(path string) (*Lock, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, domain.Fail(domain.PermissionDenied, "Invalid lock file.", "Inspect the data scope.")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	l := &Lock{f: f}
	info, err := f.Stat()
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
func (l *Lock) Close() error { return l.f.Close() }
