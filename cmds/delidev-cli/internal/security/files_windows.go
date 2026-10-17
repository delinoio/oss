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
	// Pin the owner as well as the DACL. Elevated tokens can otherwise assign
	// a default group owner independently of the user-only access rule.
	sid := u.User.Sid.String()
	return windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")")
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
	return checkPrivateDescriptor(sd)
}

// checkPrivateHandle applies the owner-only contract to an open handle, so the
// check cannot be redirected by a path replacement. One security-descriptor read
// returns both the owner and the DACL: an object owned by another principal can
// rewrite its own DACL (WRITE_DAC is implicit for the owner), so an owner-only
// ACE list alone does not make the object private.
func checkPrivateHandle(f *os.File, _ os.FileInfo) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	allowed, err := currentPrincipalSIDs()
	if err != nil {
		return err
	}
	if err := checkOwnerDescriptor(sd, allowed); err != nil {
		return err
	}
	return checkPrivateDescriptor(sd)
}

// currentPrincipalSIDs lists the principals that may own an object this process
// treats as its own: the process user and the default owner of objects the
// process creates. They differ when the token is elevated, because an elevated
// token creates objects owned by the Administrators group; both are the
// principal the process runs as, never another account.
func currentPrincipalSIDs() ([]*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sids := []*windows.SID{user.User.Sid}
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size); err != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, errors.Join(errors.New("token owner size query failed"), err)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size); err != nil {
		return nil, err
	}
	// TOKEN_OWNER is a single SID pointer into the same buffer.
	owner := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0])).Owner
	if owner != nil {
		sids = append(sids, owner)
	}
	return sids, nil
}

// checkOwnerDescriptor requires the descriptor's owner to be one of allowed. A
// missing owner fails closed.
func checkOwnerDescriptor(sd *windows.SECURITY_DESCRIPTOR, allowed []*windows.SID) error {
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !ownerAllowed(owner, allowed) {
		return domain.Fail(domain.PermissionDenied, "State ownership is not the current user.", "Choose a DeliDev-created private data directory.")
	}
	return nil
}

func ownerAllowed(owner *windows.SID, allowed []*windows.SID) bool {
	if owner == nil {
		return false
	}
	for _, sid := range allowed {
		if sid != nil && owner.Equals(sid) {
			return true
		}
	}
	return false
}

func checkPrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
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
