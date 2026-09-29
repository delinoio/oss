//go:build darwin || linux

package runmoor

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
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
func privateVMDirectory(path string) (os.FileInfo, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, problem(ErrOwnership, "Tart VM directory is not an owned directory.", "Preserve the VM and its Runmoor record; do not adopt or delete it by name.")
	}
	return st, nil
}
func openTartVMDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, os.ErrPermission
	}
	return f, nil
}
func readTartVMOwnerMarker(dir *os.File, limit int64) ([]byte, error) {
	fd, err := unix.Openat(int(dir.Fd()), vmOwnerMarkerName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), vmOwnerMarkerName)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, os.ErrPermission
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, os.ErrInvalid
	}
	return b, nil
}
func publishTartVMOwnerMarker(dir *os.File, contents []byte) error {
	name := ".runmoor-owner-" + newID() + ".tmp"
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	tmp := os.NewFile(uintptr(fd), name)
	_, writeErr := tmp.Write(contents)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = unix.Unlinkat(int(dir.Fd()), name, 0)
		return writeErr
	}
	if syncErr != nil {
		_ = unix.Unlinkat(int(dir.Fd()), name, 0)
		return syncErr
	}
	if closeErr != nil {
		_ = unix.Unlinkat(int(dir.Fd()), name, 0)
		return closeErr
	}
	if err = unix.Linkat(int(dir.Fd()), name, int(dir.Fd()), vmOwnerMarkerName, 0); err != nil {
		_ = unix.Unlinkat(int(dir.Fd()), name, 0)
		return err
	}
	return unix.Unlinkat(int(dir.Fd()), name, 0)
}
func syncPrivateDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
func lockTartVMConfig(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, os.ErrPermission
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(io.SeekStart)}
	if err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func lockTartVMConfigAt(dir *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), "config.json", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "config.json")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, os.ErrPermission
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(io.SeekStart)}
	if err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func lockTartCreationHome(home string) (*os.File, error) {
	f, err := openPrivate(filepath.Join(home, ".runmoor-creation.lock"), os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, problem(ErrRetry, "Tart VM creation is still in progress.", "Wait for the current creation or cleanup operation to finish; its reservation remains held.")
	}
	return f, nil
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
