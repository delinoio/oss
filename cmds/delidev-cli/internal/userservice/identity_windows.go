package userservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"golang.org/x/sys/windows"
)

func currentUser() (string, error) {
	t, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}
func fileIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", failure()
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var v windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &v) != nil || v.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", failure()
	}
	return fmt.Sprintf("%d:%d:%d", v.VolumeSerialNumber, v.FileIndexHigh, v.FileIndexLow), nil
}
func verifyProcess(s Spec, r runtimeRecord) error {
	p, err := process.ProcessIdentity(r.PID)
	if err != nil || p.Birth != r.Birth {
		return failure()
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(r.PID))
	if err != nil {
		return failure()
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token) != nil {
		return failure()
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil || u.User.Sid.String() != s.User {
		return failure()
	}
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil || verifyProcessImage(s, windows.UTF16ToString(buf[:n])) != nil {
		return failure()
	}
	p, err = process.ProcessIdentity(r.PID)
	if err != nil || p.Birth != r.Birth {
		return failure()
	}
	return nil
}

func verifyProcessImage(s Spec, image string) error {
	// Win32 can report an 8.3 spelling from process creation, while installation
	// retains EvalSymlinks' long spelling. Resolve both observations under the
	// same path contract, then require the original file identity as well.
	if !filepath.IsAbs(image) {
		return failure()
	}
	canonical, err := filepath.EvalSymlinks(image)
	if err != nil || !strings.EqualFold(canonical, s.Binary) {
		return failure()
	}
	identity, err := fileIdentity(canonical)
	if err != nil || identity != s.BinaryIdentity {
		return failure()
	}
	return nil
}

func processAbsent(pid int) bool {
	_, err := process.ProcessIdentity(pid)
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
}
