//go:build darwin || linux

package lease

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return privatePath(path, true)
}
func privateDatabase(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		if err = f.Close(); err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	return privatePath(path, false)
}
func privatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || (!directory && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
		return fmt.Errorf("lease state is not a private owned %s; select a current-user-only registry", path)
	}
	return nil
}
func pathContains(parent, child string) bool {
	// APFS may be case insensitive. Conservative case folding also covers absent
	// cache leaves without creating probes in a cache before it is protected.
	if runtime.GOOS == "darwin" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
