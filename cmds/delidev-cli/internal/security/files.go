package security

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// PrivateDir owns only its final component. Existing shared directories are
// rejected rather than silently changing another application's permissions.
func PrivateDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := createPrivateDirectory(path); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return domain.Fail(domain.PermissionDenied, "The data scope is not a real directory.", "Choose a private directory without a symlink at its root.")
	}
	if err := checkPrivate(path, info); err != nil {
		return err
	}
	return nil
}

func RegularPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return domain.Fail(domain.PermissionDenied, "A private state file is not a regular file.", "Restore the state file from a trusted backup.")
	}
	return checkPrivate(path, info)
}

func WriteAtomic(path string, contents []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(contents)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := replaceFile(name, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func ReadPrivate(path string, max int64) ([]byte, error) {
	if err := RegularPrivate(path); err != nil {
		return nil, err
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, domain.Fail(domain.PermissionDenied, "Invalid private file.", "Restore the state file.")
	}
	if err := checkPrivate(path, info); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, domain.Fail(domain.ResourceExhausted, "Private file exceeds its size limit.", "Inspect the private file without exposing its contents.")
	}
	return b, nil
}
