//go:build windows

package state

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
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
		return nil, fmt.Errorf("lock lock file: %w", err)
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
		return fmt.Errorf("unlock lock file: %w", err)
	}

	return nil
}
