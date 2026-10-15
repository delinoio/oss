package runmoor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func unsupported() error {
	return problem(ErrPlatform, "This operation is unavailable on Windows.", "Use Windows host or Windows Docker execution.")
}
func currentSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
func privateSecurity() (*windows.SECURITY_DESCRIPTOR, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")")
}
func verifyPrivateHandle(h windows.Handle) error {
	sid, err := currentSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(sid) {
		return os.ErrPermission
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return os.ErrPermission
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, (**windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&ace))); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			return os.ErrPermission
		}
	}
	return nil
}
func rejectReparsePath(path string) error {
	if !filepath.IsAbs(path) || filepath.VolumeName(path) == "" || len(filepath.VolumeName(path)) != 2 {
		return os.ErrPermission
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		ptr, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return os.ErrPermission
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func windowsOpen(path string, access, disposition uint32, sd *windows.SECURITY_DESCRIPTOR) (*os.File, error) {
	if err := rejectReparsePath(path); err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var sa *windows.SecurityAttributes
	if sd != nil {
		sa = &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	}
	h, err := windows.CreateFile(ptr, access|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, sa, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return nil, os.ErrPermission
	}
	return os.NewFile(uintptr(h), path), nil
}
func privateDir(path string) error {
	if err := rejectReparsePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	f, err := windowsOpen(path, windows.WRITE_DAC, windows.OPEN_EXISTING, nil)
	if err != nil {
		return err
	}
	defer f.Close()
	sid, err := currentSID()
	if err != nil {
		return err
	}
	current, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := current.Owner()
	if err != nil || !owner.Equals(sid) {
		return os.ErrPermission
	}
	sd, err := privateSecurity()
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func openPrivate(path string, flags int) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if flags&(os.O_WRONLY|os.O_RDWR) != 0 {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flags&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
		if flags&os.O_EXCL != 0 {
			disposition = windows.CREATE_NEW
		}
	}
	sd, err := privateSecurity()
	if err != nil {
		return nil, err
	}
	f, err := windowsOpen(path, access, disposition, sd)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || verifyPrivateHandle(windows.Handle(f.Fd())) != nil {
		f.Close()
		return nil, problem(ErrPermission, "Private file access is unsafe.", "Use a local regular file owned by the current user with an owner-only ACL.")
	}
	if flags&os.O_TRUNC != 0 {
		err = f.Truncate(0)
	}
	if err == nil && flags&os.O_APPEND != 0 {
		_, err = f.Seek(0, io.SeekEnd)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func readPrivate(path string, limit int64) ([]byte, error) {
	f, e := openPrivate(path, os.O_RDONLY)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, problem(ErrConfig, "Private input is unreadable or too large.", "Use a bounded private file.")
	}
	return b, nil
}
func lockState(path string) (*os.File, error) {
	f, e := openPrivate(path, os.O_RDWR|os.O_CREATE)
	if e != nil {
		return nil, e
	}
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if e != nil {
		f.Close()
		return nil, problem(ErrLocked, "Another process owns this state directory.", "Use the running manager's control commands.")
	}
	return f, nil
}
func unlockState(f *os.File) {
	if f != nil {
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
		_ = f.Close()
	}
}

// Windows does not support fsync on directory handles. File publication uses
// flushed file contents followed by native atomic same-volume renames.
func syncPrivateDir(path string) error { return rejectReparsePath(path) }
func freeDisk(path string) (uint64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	var free uint64
	e = windows.GetDiskFreeSpaceEx(p, &free, nil, nil)
	return free, e
}
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW, HideWindow: true}
}
func interruptProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func tartRunProcessStartIdentity(pid int) (string, error) {
	p, e := windowsProcess(pid)
	return p.Start, e
}
func tartRunProcessAlive(pid int, start string) (bool, error) {
	p, e := windowsProcess(pid)
	if os.IsNotExist(e) {
		return false, nil
	}
	return e == nil && (start == "" || p.Start == start), e
}
func windowsProcess(pid int) (HostProcess, error) {
	if pid <= 0 {
		return HostProcess{}, os.ErrInvalid
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e == windows.ERROR_INVALID_PARAMETER {
		return HostProcess{}, os.ErrNotExist
	}
	if e != nil {
		return HostProcess{}, e
	}
	defer windows.CloseHandle(h)
	var code uint32
	if e = windows.GetExitCodeProcess(h, &code); e != nil {
		return HostProcess{}, e
	}
	if code != 259 {
		return HostProcess{}, os.ErrNotExist
	}
	var token windows.Token
	if e = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); e != nil {
		return HostProcess{}, e
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	sid, se := currentSID()
	if e != nil || se != nil || !user.User.Sid.Equals(sid) {
		return HostProcess{}, os.ErrPermission
	}
	var created, exit, kernel, userTime windows.Filetime
	if e = windows.GetProcessTimes(h, &created, &exit, &kernel, &userTime); e != nil {
		return HostProcess{}, e
	}
	return HostProcess{PID: pid, Group: pid, Start: fmt.Sprintf("windows:%d:%d", created.HighDateTime, created.LowDateTime)}, nil
}
func hostCapacity() (Resources, error) {
	var m struct {
		Length, Load                                                                          uint32
		TotalPhys, AvailPhys, TotalPage, AvailPage, TotalVirtual, AvailVirtual, AvailExtended uint64
	}
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return Resources{}, problem(ErrCapacity, "Cannot inspect Windows memory.", "Specify explicit resource budgets.")
	}
	return Resources{runtime.NumCPU(), int64(m.TotalPhys >> 20)}, nil
}
func init() {
	windowsPlatform = func(ctx context.Context) error {
		v := windows.RtlGetVersion()
		if runtime.GOARCH != "amd64" || v.ProductType != 1 || v.BuildNumber < 22000 {
			return problem(ErrPlatform, "Windows 11 x64 is required.", "Use a supported Windows 11 x64 host; Server and ARM64 are unsupported.")
		}
		return ctx.Err()
	}
}

// Tart is never selected on Windows; these hooks keep portable reconciliation
// buildable without granting Windows authority over macOS VM resources.
func privateVMDirectory(string) (os.FileInfo, error)        { return nil, unsupported() }
func openTartVMDirectory(string) (*os.File, error)          { return nil, unsupported() }
func openTartVMFileAt(*os.File, string) (*os.File, error)   { return nil, unsupported() }
func readTartVMOwnerMarker(*os.File, int64) ([]byte, error) { return nil, unsupported() }
func publishTartVMOwnerMarker(*os.File, []byte) error       { return unsupported() }
func lockTartVMConfig(string) (*os.File, error)             { return nil, unsupported() }
func lockTartVMConfigAt(*os.File) (*os.File, error)         { return nil, unsupported() }
func lockTartCreationHome(string) (*os.File, error)         { return nil, unsupported() }
func renameTartVMNoReplace(string, string) error            { return unsupported() }
