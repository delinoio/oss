//go:build !windows

package security

import (
	"errors"
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func createPrivateDirectory(path string) error { return os.Mkdir(path, 0700) }
func checkPrivate(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return domain.Fail(domain.PermissionDenied, "State must be accessible only by its owner.", "Choose an owner-only data directory (0700) and files (0600).")
	}
	return nil
}
func replaceFile(from, to string) error { return os.Rename(from, to) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func openNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

type Lock struct{ f *os.File }

func TryLock(path string) (*Lock, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = domain.Fail(domain.PermissionDenied, "Invalid lock file.", "Inspect the data scope.")
	}
	if err == nil {
		err = checkPrivate(path, info)
	}
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, domain.Fail(domain.Conflict, "Another process owns this data scope.", "Use the running server or stop it explicitly.")
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}
func (l *Lock) Close() error { return l.f.Close() }
