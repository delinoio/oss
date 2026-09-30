package userservice

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func processOwner(pid int) (string, string, error) {
	root := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(root)
	if err != nil {
		return "", "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", failure()
	}
	binary, err := os.Readlink(filepath.Join(root, "exe"))
	return binary, strconv.FormatUint(uint64(st.Uid), 10), err
}
func renameExclusive(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
