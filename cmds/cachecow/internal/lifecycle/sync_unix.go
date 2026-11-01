//go:build linux || darwin

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
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		return f.Sync()
	})
}
func syncParent(path string) error {
	f, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
