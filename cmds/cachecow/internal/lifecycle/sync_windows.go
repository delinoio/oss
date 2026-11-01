package lifecycle

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func syncTree(path string) error {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		return syncStagedFile(p)
	})
}

func syncStagedFile(path string) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Clone restores source permissions before returning. Windows requires a
	// writable handle for FlushFileBuffers, so clear only the read-only bit on
	// this private staging file and restore it even if open or flush fails.
	if info.Mode().Perm()&0200 == 0 {
		if err = os.Chmod(path, info.Mode().Perm()|0200); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, os.Chmod(path, info.Mode().Perm())) }()
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

// Windows does not provide directory fsync through os.File. The journal remains
// durable, but sudden storage loss still requires native filesystem acceptance.
func syncParent(string) error { return nil }
