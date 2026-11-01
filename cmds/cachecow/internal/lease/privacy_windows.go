//go:build windows

package lease

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// The registry parent must exist. Creation applies the private ACL atomically;
// existing state is validated without taking ownership or repairing permissions.
func privateDir(path string) error {
	if err := createStateDirectory(path); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe lease registry directory")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !owner.Equals(user.User.Sid) {
		return ErrUnverified
	}
	ok, err := stateDirectoryPrivate(path)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnverified
	}
	return nil
}
func privateDatabase(path string) error {
	// New SQLite files inherit the protected current-user-only directory ACL.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		return f.Close()
	}
	if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnverified
	}
	// Require the existing file's owner and effective ACL to match the directory.
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !owner.Equals(user.User.Sid) {
		return ErrUnverified
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil || acl.AceCount != 1 {
		return ErrUnverified
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		return err
	}
	expected, err := stateSecurityDescriptor()
	if err != nil {
		return err
	}
	expectedACL, _, err := expected.DACL()
	if err != nil {
		return err
	}
	var want *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(expectedACL, 0, &want); err != nil {
		return err
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != want.Mask || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
		return ErrUnverified
	}
	return nil
}
func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

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
