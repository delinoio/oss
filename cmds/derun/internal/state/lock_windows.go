//go:build windows

package state

import "os"

func lockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

func unlockFile(f *os.File) error {
	if f == nil {
		return nil
	}
	return f.Close()
}
