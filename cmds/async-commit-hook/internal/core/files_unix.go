//go:build !windows

package core

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

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
	return os.Chmod(path, 0700)
}
func PrivateFile(path string) error { return os.Chmod(path, 0600) }

func createStateDirectory(path string) error { return os.Mkdir(path, 0700) }

func stateDirectoryPrivate(path string) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return false, err
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Mode&07777 == 0700 && stat.Uid == uint32(os.Geteuid()), nil
}

type Lock struct{ f *os.File }

func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, nil
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}
func (l *Lock) Close() { _ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN); _ = l.f.Close() }
func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	f, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
