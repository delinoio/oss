//go:build windows

package core

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func restrict(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func PrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return E("unsafe-directory", "owned directory is not a real directory", 3)
	}
	return restrict(path)
}
func PrivateFile(path string) error { return restrict(path) }

func stateSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
}

func createStateDirectory(path string) error {
	sd, err := stateSecurityDescriptor()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Apply privacy at creation, never by replacing the ACL of an existing path.
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	return windows.CreateDirectory(p, &sa)
}

func stateDirectoryPrivate(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return false, err
	}
	expected, err := stateSecurityDescriptor()
	if err != nil {
		return false, err
	}
	expectedACL, _, err := expected.DACL()
	if err != nil {
		return false, err
	}
	var actual, want *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &actual); err != nil {
		return false, err
	}
	if err := windows.GetAce(expectedACL, 0, &want); err != nil {
		return false, err
	}
	if actual.Header != want.Header || actual.Mask != want.Mask {
		return false, nil
	}
	return (*windows.SID)(unsafe.Pointer(&actual.SidStart)).Equals((*windows.SID)(unsafe.Pointer(&want.SidStart))), nil
}

type Lock struct {
	f    *os.File
	over windows.Overlapped
}

func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	l := &Lock{f: f}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.over)
	if err != nil {
		f.Close()
		if err == windows.ERROR_LOCK_VIOLATION {
			return nil, nil
		}
		return nil, err
	}
	return l, nil
}
func (l *Lock) Close() {
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.over)
	_ = l.f.Close()
}
func replaceFile(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
