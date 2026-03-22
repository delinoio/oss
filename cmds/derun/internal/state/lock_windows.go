//go:build windows

package state

import (
	"os"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
	"golang.org/x/sys/windows"
)

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errmsg.Error(errmsg.Runtime("open lock file", err, map[string]any{
			"lock_path": path,
		}), nil)
	}

	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&windows.Overlapped{},
	); err != nil {
		_ = f.Close()
		return nil, errmsg.Error(errmsg.Runtime("lock lock file", err, map[string]any{
			"lock_path": path,
		}), nil)
	}

	return f, nil
}

func unlockFile(f *os.File) error {
	if f == nil {
		return nil
	}
	defer f.Close()

	if err := windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		1,
		0,
		&windows.Overlapped{},
	); err != nil {
		return errmsg.Error(errmsg.Runtime("unlock lock file", err, map[string]any{
			"fd": f.Fd(),
		}), nil)
	}

	return nil
}
