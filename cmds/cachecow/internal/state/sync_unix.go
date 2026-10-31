//go:build linux || darwin

package state

import (
	"os"
	"path/filepath"
)

func syncParent(path string) error {
	f, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
