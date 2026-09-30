//go:build !windows

package userservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"golang.org/x/sys/unix"
)

func currentUser() (string, error) { return strconv.Itoa(os.Geteuid()), nil }
func fileIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", failure()
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", failure()
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func verifyProcess(s Spec, r runtimeRecord) error {
	p, err := process.ProcessIdentity(r.PID)
	if err != nil || p.Birth != r.Birth {
		return failure()
	}
	binary, user, err := processOwner(r.PID)
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil || binary != s.Binary || user != s.User {
		return failure()
	}
	p, err = process.ProcessIdentity(r.PID)
	if err != nil || p.Birth != r.Birth {
		return failure()
	}
	return nil
}

func processAbsent(pid int) bool {
	_, err := process.ProcessIdentity(pid)
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH)
}
