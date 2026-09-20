package core

import (
	"os"
	"path/filepath"
)

// A configurable state path is not proof of ownership. Never repair an existing
// directory's permissions: it may be the user's home or a shared directory.
func stateDirectory(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := createStateDirectory(path); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return E("unsafe-directory", "state directory must be a real directory", 3)
	}
	private, err := stateDirectoryPrivate(path)
	if err != nil {
		return err
	}
	if !private {
		return E("unsafe-state-permissions", "state directory requires account-only permissions; choose a new dedicated directory or correct its permissions yourself", 3)
	}
	return nil
}
