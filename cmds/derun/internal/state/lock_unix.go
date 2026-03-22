//go:build !windows

package state

import (
	"os"
	"syscall"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
)

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errmsg.Error(errmsg.Runtime("open lock file", err, map[string]any{
			"lock_path": path,
		}), nil)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, errmsg.Error(errmsg.Runtime("flock lock file", err, map[string]any{
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return errmsg.Error(errmsg.Runtime("flock unlock file", err, map[string]any{
			"fd": f.Fd(),
		}), nil)
	}
	return nil
}
