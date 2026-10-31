package lifecycle

import (
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
		f, e := os.OpenFile(p, os.O_RDWR, 0)
		if e != nil {
			return e
		}
		defer f.Close()
		return f.Sync()
	})
}

// Windows does not provide directory fsync through os.File. The journal remains
// durable, but sudden storage loss still requires native filesystem acceptance.
func syncParent(string) error { return nil }
