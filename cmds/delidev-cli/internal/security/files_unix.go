//go:build !windows

package security

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func createPrivateDirectory(path string) error { return os.Mkdir(path, 0700) }

// Existing permissions and filesystem owners do not gate access. Creation still
// uses private defaults; callers independently validate paths and file types.
func checkPrivate(_ string, info os.FileInfo) error {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && (stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0) {
		domain.ObserveOwnership(domain.OwnershipResource, "")
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

func openPrivateAppend(path string) (*os.File, bool, error) {
	_, statErr := os.Lstat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, false, statErr
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_CREAT, 0600)
	if err != nil {
		return nil, false, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = domain.Fail(domain.PermissionDenied, "Invalid private journal.", "Inspect the data scope.")
	}
	if err == nil {
		err = checkPrivate(path, info)
	}
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return f, created, nil
}

type Lock struct{ f *os.File }

func TryLock(path string) (*Lock, error)         { return tryLock(path, true) }
func TryLockExisting(path string) (*Lock, error) { return tryLock(path, false) }
func tryLock(path string, create bool) (*Lock, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, flags, 0600)
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

func publishImmutable(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(to))
}
