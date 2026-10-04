//go:build linux

package runmoor

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

// Linux supplies storage primitives for injected contract fixtures only. The
// production host adapter continues to reject Linux execution.
func hostPrivateInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
func hostFileIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
func hostRenameNoReplace(root *os.Root, from, to string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return unix.Renameat2(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_NOREPLACE)
}
