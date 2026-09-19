//go:build darwin || linux

package runmoor

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func privateDir(path string) error {
	// Reject symlinks in existing ancestors instead of chmod-ing an operator's
	// unrelated target. Parent directories can be shared; the owned leaf cannot.
	for p := filepath.Clean(path); p != "/" && p != "."; p = filepath.Dir(p) {
		if st, e := os.Lstat(p); e == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return problem(ErrPermission, "Private storage cannot traverse symlinks.", "Use a direct owner-controlled directory.")
			}
		} else if !os.IsNotExist(e) {
			return problem(ErrPermission, "Cannot inspect private storage.", "Check parent directory permissions.")
		}
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return problem(ErrPermission, "Cannot create private directory.", "Check ownership and parent directory permissions.")
	}
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return problem(ErrPermission, "Private directory is unavailable.", "Use an owner-controlled directory.")
	}
	if st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return problem(ErrPermission, "Private directory belongs to another user.", "Use your own state and data directories.")
	}
	if err = os.Chmod(path, 0700); err != nil {
		return problem(ErrPermission, "Cannot restrict private directory.", "Check filesystem permission support.")
	}
	return nil
}
func openPrivate(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, problem(ErrPermission, "Cannot securely open a private file.", "Use an owner-only regular file, without a symlink.")
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, problem(ErrPermission, "File ownership, type or permissions are unsafe.", "Use a regular file owned by your user with mode 0600.")
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
		return nil, problem(ErrConfig, "Private input exceeds its size limit or cannot be read.", "Use a bounded configuration or credential file.")
	}
	return b, nil
}
func lockState(path string) (*os.File, error) {
	f, e := openPrivate(path, os.O_RDWR|os.O_CREATE)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, problem(ErrLocked, "Another process owns this state directory.", "Use the running manager's control commands or wait for image preparation to finish.")
	}
	return f, nil
}
func unlockState(f *os.File) {
	if f != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
func freeDisk(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, problem(ErrDisk, "Cannot inspect free disk space.", "Check the state and data filesystems.")
	}
	return st.Bavail * uint64(st.Bsize), nil
}
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func interruptProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
