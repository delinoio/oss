package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	// Existing parents may belong to a repository, agent client, or package manager.
	// Creating a file must never chmod an unrelated directory such as /usr/local/bin.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ach-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(name, path)
}
func ReadOwned(root, relative string, limit int64) ([]byte, error) {
	if !SafeRelative(relative) {
		return nil, E("unsafe-path", "expected an owned relative file", 2)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(relative)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, E("unsafe-file", "evidence must be a regular file", 3)
	}
	if info.Size() > limit {
		return nil, E("evidence-too-large", fmt.Sprintf("evidence exceeds %d bytes", limit), 3)
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
